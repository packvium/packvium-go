// Package packer solves a packing request: it runs the solver portfolio, validates and
// scores every answer, and returns the best with its ranked alternatives.
package packer

import (
	"fmt"
	"sort"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/result"
	"github.com/packvium/packvium-go/validation"
)

// Packer is an immutable solver for one configuration and set of extensions.
type Packer struct {
	cfg        *config.PackingConfig
	extensions *algorithm.ExtensionRegistry
	scorer     algorithm.SolutionScorer
	clock      algorithm.Clock
}

// New is a packer with the configured objective and the monotonic clock; nil extensions are
// the built-ins only.
func New(cfg *config.PackingConfig, extensions *algorithm.ExtensionRegistry) *Packer {
	return NewWith(cfg, extensions, nil, nil)
}

// NewWith also takes a solution scorer overriding the configured objective, and a clock; nil
// for either is the default.
func NewWith(cfg *config.PackingConfig, extensions *algorithm.ExtensionRegistry, scorer algorithm.SolutionScorer,
	clock algorithm.Clock) *Packer {
	if extensions == nil {
		extensions = algorithm.NoExtensions()
	}
	return &Packer{cfg, extensions, scorer, clock}
}

// UnratedWeightError is a finished answer whose billed weight the tariff cannot price.
type UnratedWeightError struct{ Message string }

func (e *UnratedWeightError) Error() string { return e.Message }

// Pack solves the request. A malformed request, an unpriceable objective or answer, and an
// arithmetic overflow are returned as errors, never panics.
func (p *Packer) Pack(items []*domain.Item, containers []*domain.Container) (packed *result.PackingResult, err error) {
	defer algorithm.RecoverRefusal(&err)
	request, err := domain.NewPackingRequest(items, containers)
	if err != nil {
		return nil, err
	}
	if err := p.refuseUnpriceableObjectives(request); err != nil {
		return nil, err
	}
	clock := p.clock
	if clock == nil {
		clock = algorithm.MonotonicClock()
	}
	deadline := algorithm.DeadlineWithClock(p.cfg.TimeLimitMS(), clock)
	portfolio := algorithm.NewSolverOrchestrator(p.extensions).SolvePortfolio(request.Instances(),
		request.Containers(), p.cfg, deadline)
	scorer := p.scorer
	if scorer == nil {
		scorer = algorithm.ResolveObjective(p.cfg.Objective(), p.cfg)
	}
	ranked := make([]*result.PackingResult, 0, len(portfolio.Solutions))
	for _, raw := range portfolio.Solutions {
		one, err := p.rank(raw, request, portfolio.Starts, scorer, deadline)
		if err != nil {
			return nil, err
		}
		ranked = append(ranked, one)
	}
	ranked = sortResults(ranked)
	var valid []*result.PackingResult
	for _, one := range ranked {
		if one.Status != result.InvalidResult {
			valid = append(valid, one)
		}
	}
	selected := ranked
	if len(valid) > 0 {
		selected = valid
	}
	best := selected[0]
	if unpriceable := algorithm.UnpriceableContainer(best.Containers, p.cfg); unpriceable != nil {
		return nil, &UnratedWeightError{fmt.Sprintf(
			"container '%s' bills at %d g, above its rate table's last bracket (%d g); the shipment has no published price",
			unpriceable.ContainerID, unpriceable.BilledGrams, unpriceable.LastBracketG)}
	}
	// The unpriceable sentinel is a search device, never an answer, for runner-ups too: filter
	// before slicing to top-k.
	var alternatives []*result.PackingResult
	for _, one := range selected[1:] {
		if int64(len(alternatives)) >= p.cfg.TopK()-1 {
			break
		}
		if algorithm.UnpriceableContainer(one.Containers, p.cfg) == nil {
			alternatives = append(alternatives, one)
		}
	}
	return best.WithAlternatives(alternatives), nil
}

// sortResults ranks valid answers first, then by score, then by start id, ties stable.
func sortResults(ranked []*result.PackingResult) []*result.PackingResult {
	keys := make([]algorithm.SortKey, len(ranked))
	for index, one := range ranked {
		invalid := int64(0)
		if one.Status == result.InvalidResult {
			invalid = 1
		}
		keys[index] = algorithm.Key(algorithm.Int(invalid), algorithm.Nested(algorithm.Ints(one.Score)),
			algorithm.Str(one.Algorithm.Solver))
	}
	order := make([]int, len(ranked))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool { return keys[order[i]].Less(keys[order[j]]) })
	out := make([]*result.PackingResult, len(ranked))
	for position, index := range order {
		out[position] = ranked[index]
	}
	return out
}

// refuseUnpriceableObjectives refuses, before any solver runs, a cost objective without a
// divisor and a landed-cost objective over a container without a tariff: both are static
// properties of the request. There is no library-chosen default divisor.
func (p *Packer) refuseUnpriceableObjectives(request *domain.PackingRequest) error {
	objective := p.cfg.Objective()
	priced := objective == "shipping_cost" || objective == algorithm.LandedCostObjective
	if priced && p.cfg.DimensionalWeightDivisor() == nil {
		return &algorithm.UnknownObjectiveError{Message: "the " + objective +
			" objective requires configuration.dimensional_weight_divisor"}
	}
	if objective != algorithm.LandedCostObjective {
		return nil
	}
	for _, container := range request.Containers() {
		if container.RateTable() == nil {
			return &algorithm.UnknownObjectiveError{Message: "the lowest_landed_cost objective requires a rate_table " +
				"on every container; '" + container.ID() + "' has none"}
		}
	}
	return nil
}

func (p *Packer) rank(raw algorithm.RawSolution, request *domain.PackingRequest, records []result.StartRecord,
	scorer algorithm.SolutionScorer, deadline *algorithm.Deadline) (*result.PackingResult, error) {
	score := []int64(scorer.Score(raw))
	status := statusOf(raw)
	var warnings []string
	if p.cfg.ValidateResult() {
		if report := p.validate(raw, request); !report.Valid() {
			status = result.InvalidResult
			for _, issue := range report.Issues() {
				warnings = append(warnings, issue.Code+": "+issue.Detail)
			}
		}
	}
	termination, err := result.AggregateTermination(markWinner(records, raw.SolverName), status == result.InvalidResult)
	if err != nil {
		return nil, err
	}
	if raw.EffortLimitReached && !raw.TimeLimitReached {
		termination = termination.WithCode("effort_limit")
	}
	globalDeadline, _ := termination.Attribute("global_deadline_reached")
	report := result.AlgorithmReport{
		Profile: string(p.cfg.Profile()), Solver: raw.SolverName, DurationMS: deadline.ElapsedMS(), Seed: p.cfg.Seed(),
		TimeLimitReached: globalDeadline == true || raw.TimeLimitReached, EffortLimitReached: raw.EffortLimitReached,
		CandidatesEvaluated: raw.Stats.CandidatesEvaluated, PlacementsAttempted: raw.Stats.PlacementsAttempted,
		Metrics: raw.Stats.Metrics(),
	}
	return result.NewPackingResult(status, raw.Containers, raw.Unpacked, report, score, warnings, termination,
		p.cfg.Objective()), nil
}

func statusOf(raw algorithm.RawSolution) result.PackingStatus {
	if len(raw.Unpacked) > 0 {
		if raw.TimeLimitReached {
			return result.TimeLimit
		}
		return result.BestFound
	}
	if raw.Exhaustive {
		return result.Optimal
	}
	return result.Feasible
}

// validate re-derives every guarantee from per-item placements, so a compact lattice container
// is expanded just for the check; the returned result stays compact.
func (p *Packer) validate(raw algorithm.RawSolution, request *domain.PackingRequest) validation.ValidationReport {
	expanded := make([]*domain.PackedContainer, len(raw.Containers))
	for index, container := range raw.Containers {
		expanded[index] = container
		if container.LatticeSummary() != nil {
			expanded[index] = domain.NewPackedContainer(container.Container(), container.Sequence(),
				container.ExpandPlacements())
		}
	}
	clearance := p.cfg.Clearance()
	return validation.ValidateWithUnpacked(request, expanded, p.cfg.MinimumSupportRatio(), &clearance, raw.Unpacked)
}

// markWinner selects the first start record carrying the winner's id.
func markWinner(records []result.StartRecord, solverName string) []result.StartRecord {
	marked := make([]result.StartRecord, len(records))
	winnerMarked := false
	for index, record := range records {
		record.Selected = !winnerMarked && record.ID == solverName
		winnerMarked = winnerMarked || record.Selected
		marked[index] = record
	}
	return marked
}
