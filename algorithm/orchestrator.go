package algorithm

import (
	"sort"
	"strings"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/result"
)

// solverNames are the built-in solvers in registration order.
var solverNames = []string{"grid", "extreme_points", "homogeneous_blocks", "layer", "maximal_spaces", "exact_small"}

// SolverOrchestrator runs the portfolio: every solver over every distinct item order, each
// start on its own slice of the deadline, sequentially and in a fixed order, so the ranked
// answer never depends on scheduling.
type SolverOrchestrator struct {
	customConstraints []constraint.PlacementConstraint
	customOrders      []ItemOrderStrategy
	customSolvers     []SingleContainerSolver
	customScorer      CandidateScorer
	customSelector    ContainerSelector
	scorer            CandidateScorer
	reasons           *unpackedReasons
}

// NewSolverOrchestrator runs the built-in portfolio plus the registry's extensions.
func NewSolverOrchestrator(extensions *ExtensionRegistry) *SolverOrchestrator {
	scorer := extensions.CandidateScorer()
	if scorer == nil {
		scorer = DefaultCandidateScorer{}
	}
	constraints := extensions.PlacementConstraints()
	return &SolverOrchestrator{
		customConstraints: constraints, customOrders: extensions.ItemOrderStrategies(),
		customSolvers: extensions.Solvers(), customScorer: extensions.CandidateScorer(),
		customSelector: extensions.ContainerSelector(), scorer: scorer,
		reasons: &unpackedReasons{constraints, scorer},
	}
}

// SolvePortfolio runs every start, capped by the effort budget's restart limit.
func (o *SolverOrchestrator) SolvePortfolio(items []domain.ItemInstance, containers []*domain.Container,
	cfg *config.PackingConfig, deadline *Deadline) PortfolioRun {
	return o.SolvePortfolioFrom(items, containers, cfg, deadline, nil)
}

// SolvePortfolioFrom is SolvePortfolio continuing from preloaded: the containers a request's
// fixed placements name, each holding only its fixed items, in the order they open. Every
// start fills them first and keeps them, whatever else it does.
func (o *SolverOrchestrator) SolvePortfolioFrom(items []domain.ItemInstance, containers []*domain.Container,
	cfg *config.PackingConfig, deadline *Deadline, preloaded []*domain.PackedContainer) PortfolioRun {
	starts := o.starts(items, cfg)
	budget := cfg.EffortBudget()
	if budget != nil && budget.MaxRestarts != nil && *budget.MaxRestarts < int64(len(starts)) {
		starts = starts[:*budget.MaxRestarts]
	}
	return o.solveSequential(items, containers, cfg, deadline, starts, budget, preloaded)
}

func (o *SolverOrchestrator) solveSequential(items []domain.ItemInstance, containers []*domain.Container,
	cfg *config.PackingConfig, deadline *Deadline, starts []start, budget *config.EffortBudget,
	preloaded []*domain.PackedContainer) PortfolioRun {
	var results []RawSolution
	completed := map[string]start{}
	records := make([]result.StartRecord, len(starts))
	for index, current := range starts {
		records[index] = result.StartRecord{ID: current.id()}
	}
	for position, current := range starts {
		if deadline.Expired() {
			break
		}
		if isRefinement(current.orderName) {
			refined, eligible := refinementOf(current, results, completed, cfg)
			if !eligible {
				continue
			}
			current = refined
		}
		stats := &SearchStats{}
		slice := deadline.Slice(int64(len(starts) - position))
		if budget != nil {
			slice = slice.WithEffort(budget, stats)
		}
		startConfig := cfg
		if strings.HasSuffix(current.orderName, ":greedy") || strings.Contains(current.orderName, ":late_") {
			startConfig = greedyConfig(cfg)
		}
		outcome := o.acrossContainers(current.solver, current.order, containers, startConfig, stats, slice, preloaded)
		effortReached := slice.EffortExceeded()
		wallReached := outcome.reached && slice.RemainingNS() <= 0
		unpacked := outcome.unpacked
		if effortReached {
			unpacked = relabelEffort(unpacked)
		}
		results = append(results, RawSolution{current.id(), outcome.packed, unpacked, stats, wallReached, effortReached,
			outcome.proven})
		completed[current.id()] = current
		records[position] = result.StartRecord{ID: current.id(), Started: true, Completed: !outcome.reached,
			Truncated: outcome.reached}
		// A single type's densest packing is the lattice just tried: once it placed everything,
		// no default-scored start can do better, so the rest would only spend the deadline. That
		// holds only when the lattice arithmetic really ran, and never for a custom scorer,
		// which asks for a placement behaviour the lattice cannot honour.
		if outcome.dominantLattice && len(unpacked) == 0 && o.customScorer == nil {
			break
		}
	}
	return o.finishPortfolio(items, containers, deadline, results, records, preloaded)
}

func relabelEffort(unpacked []domain.UnpackedItem) []domain.UnpackedItem {
	out := make([]domain.UnpackedItem, len(unpacked))
	for index, item := range unpacked {
		if item.Reason == "time_limit" {
			item = domain.NewUnpackedItem(item.Instance, "effort_limit", item.Details)
		}
		out[index] = item
	}
	return out
}

func (o *SolverOrchestrator) finishPortfolio(items []domain.ItemInstance, containers []*domain.Container,
	deadline *Deadline, results []RawSolution, records []result.StartRecord,
	preloaded []*domain.PackedContainer) PortfolioRun {
	globalDeadline := deadline.Expired()
	finished := make([]result.StartRecord, len(records))
	for index, record := range records {
		record.GlobalDeadlineReached = globalDeadline
		finished[index] = record
	}
	solutions := append([]RawSolution(nil), results...)
	if len(solutions) == 0 {
		fallbackID := "portfolio:fallback"
		unpacked := make([]domain.UnpackedItem, len(items))
		for index, item := range items {
			found := o.reasons.reason(item, containers, true)
			unpacked[index] = domain.NewUnpackedItem(item, found.code, found.details)
		}
		solutions = append(solutions, RawSolution{SolverName: fallbackID,
			Containers: append([]*domain.PackedContainer(nil), preloaded...), Unpacked: unpacked, Stats: &SearchStats{},
			TimeLimitReached: true})
		finished = append(finished, result.StartRecord{ID: fallbackID, Started: true, Completed: true,
			GlobalDeadlineReached: globalDeadline})
	}
	return PortfolioRun{solutions, finished}
}

func (o *SolverOrchestrator) starts(items []domain.ItemInstance, cfg *config.PackingConfig) []start {
	orders := ItemOrders(items, cfg, o.customOrders)
	var starts []start
	for _, solver := range o.solvers(items, cfg) {
		// A lattice ignores item order, so repeating it per ordering only burns budget.
		chosen := orders
		if solver.OrderInsensitive() && len(orders) > 1 {
			chosen = orders[:1]
		}
		for _, order := range chosen {
			starts = append(starts, start{solver, order.Name, order.Order})
		}
	}
	if cfg.ContainerPlanBeamWidth() <= 1 || cfg.Profile() != config.Quality {
		return starts
	}
	withGreedy := make([]start, 0, len(starts))
	var extreme *start
	for index, current := range starts {
		if current.solver.Name() != "homogeneous_blocks" {
			current.orderName += ":greedy"
		}
		withGreedy = append(withGreedy, current)
		if extreme == nil && current.solver.Name() == "extreme_points" {
			extreme = &starts[index]
		}
	}
	if extreme == nil {
		return withGreedy
	}
	// The refinements are placeholders until they run: each re-runs the best eligible
	// extreme-point answer found before it (refinementOf), so its solver and order are only
	// known then. One neighbourhood start per item type, then the beam over the best answer.
	for _, itemType := range sortedItemTypes(items) {
		withGreedy = append(withGreedy, start{extreme.solver, neighbourhoodPrefix + itemType, extreme.order})
	}
	return append(withGreedy, start{extreme.solver, bestBeam, extreme.order})
}

const (
	neighbourhoodPrefix = "neighborhood:"
	bestBeam            = "best:beam"
)

func sortedItemTypes(items []domain.ItemInstance) []string {
	seen := map[string]bool{}
	var types []string
	for _, item := range items {
		if id := item.Item().ID(); !seen[id] {
			seen[id] = true
			types = append(types, id)
		}
	}
	sort.Strings(types)
	return types
}

func isRefinement(orderName string) bool {
	return strings.HasPrefix(orderName, neighbourhoodPrefix) || strings.HasSuffix(orderName, ":beam")
}

// refinementOf turns a placeholder refinement into the start it runs: the best answer so far
// from the built-in extreme-point solver (a greedy one, for a neighbourhood), ranked by the
// configured objective and then by start id. A neighbourhood re-runs that answer's order with
// one item type moved to the end; the beam re-runs it as it was, with the beams on. False
// when no answer is eligible, and the start is skipped. O(r * c) for r answers so far.
func refinementOf(placeholder start, results []RawSolution, completed map[string]start,
	cfg *config.PackingConfig) (start, bool) {
	neighbourhood := strings.HasPrefix(placeholder.orderName, neighbourhoodPrefix)
	scorer := ResolveObjective(cfg.Objective(), cfg)
	var best *RawSolution
	var bestScore ObjectiveScore
	for index := range results {
		candidate := &results[index]
		if _, builtIn := completed[candidate.SolverName].solver.(*ExtremePointSolver); !builtIn {
			continue
		}
		if neighbourhood && !strings.HasSuffix(candidate.SolverName, ":greedy") {
			continue
		}
		score := scorer.Score(*candidate)
		if best == nil {
			best, bestScore = candidate, score
			continue
		}
		if order := score.Compare(bestScore); order < 0 || (order == 0 && candidate.SolverName < best.SolverName) {
			best, bestScore = candidate, score
		}
	}
	if best == nil {
		return start{}, false
	}
	base := completed[best.SolverName]
	if !neighbourhood {
		return start{base.solver, base.orderName + ":beam", base.order}, true
	}
	itemType := strings.TrimPrefix(placeholder.orderName, neighbourhoodPrefix)
	late := make([]domain.ItemInstance, 0, len(base.order))
	var moved []domain.ItemInstance
	for _, item := range base.order {
		if item.Item().ID() == itemType {
			moved = append(moved, item)
		} else {
			late = append(late, item)
		}
	}
	return start{base.solver, base.orderName + ":late_" + itemType, append(late, moved...)}, true
}

// greedyConfig is the same configuration with the beams switched off, for the cheap greedy
// pass of a quality start.
func greedyConfig(cfg *config.PackingConfig) *config.PackingConfig {
	options := cfg.Options()
	options.MaxCandidatesPerItem, options.ContainerPlanBeamWidth, options.ContainerPlanNodeLimit = 1, 1, 1
	greedy, err := config.New(options)
	if err != nil {
		refuse(err)
	}
	return greedy
}

func (o *SolverOrchestrator) solvers(items []domain.ItemInstance, cfg *config.PackingConfig) []SingleContainerSolver {
	var base []SingleContainerSolver
	if names := cfg.Solvers(); len(names) > 0 {
		base = o.namedSolvers(names)
	} else {
		base = o.defaultSolvers(items, cfg)
	}
	return append(append([]SingleContainerSolver(nil), o.customSolvers...), base...)
}

func (o *SolverOrchestrator) namedSolvers(names []string) []SingleContainerSolver {
	var unknown []string
	for _, name := range names {
		if !containsName(solverNames, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		refuse(&UnknownSolverError{"unknown solver name(s) " + pythonList(unknown) + "; expected one of " +
			pythonList(sortedStrings(solverNames))})
	}
	solvers := make([]SingleContainerSolver, len(names))
	for index, name := range names {
		solvers[index] = o.solver(name)
	}
	return solvers
}

func containsName(names []string, wanted string) bool {
	for _, name := range names {
		if name == wanted {
			return true
		}
	}
	return false
}

func (o *SolverOrchestrator) solver(name string) SingleContainerSolver {
	switch name {
	case "grid":
		return GridSolver{}
	case "extreme_points":
		return NewExtremePointSolver(o.customConstraints, o.scorer)
	case "homogeneous_blocks":
		return NewHomogeneousBlockSolver(o.customConstraints)
	case "layer":
		return NewLayerSolver(o.customConstraints, o.scorer)
	case "maximal_spaces":
		return NewMaximalSpaceSolver(o.customConstraints, o.scorer)
	}
	return NewExactSmallSolver(o.customConstraints, o.scorer)
}

func (o *SolverOrchestrator) defaultSolvers(items []domain.ItemInstance, cfg *config.PackingConfig) []SingleContainerSolver {
	grid := GridSolver{}
	if grid.Supports(items) && len(o.customConstraints) == 0 {
		if cfg.Profile() == config.Fast {
			return []SingleContainerSolver{grid}
		}
		return []SingleContainerSolver{grid, NewExtremePointSolver(nil, o.scorer)}
	}
	if cfg.Profile() == config.Fast {
		return []SingleContainerSolver{NewLayerSolver(o.customConstraints, o.scorer)}
	}
	small := int64(len(items)) <= cfg.ExactItemLimit()
	if cfg.Profile() == config.ExactSmall && small {
		return []SingleContainerSolver{NewExactSmallSolver(o.customConstraints, o.scorer),
			NewExtremePointSolver(o.customConstraints, o.scorer)}
	}
	if cfg.Profile() == config.Quality {
		var quality []SingleContainerSolver
		if small {
			quality = append(quality, NewExactSmallSolver(o.customConstraints, o.scorer))
		}
		return append(quality, NewHomogeneousBlockSolver(o.customConstraints),
			NewExtremePointSolver(o.customConstraints, o.scorer), NewMaximalSpaceSolver(o.customConstraints, o.scorer),
			NewLayerSolver(o.customConstraints, o.scorer))
	}
	return []SingleContainerSolver{NewExtremePointSolver(o.customConstraints, o.scorer),
		NewLayerSolver(o.customConstraints, o.scorer)}
}

// recordRootBound computes the request-level lower bound once, at the root of an exact_small
// or plan-beam start, for the default objective only. Nothing downstream reads it yet.
func recordRootBound(solver SingleContainerSolver, beam bool, items []domain.ItemInstance,
	containers []*domain.Container, cfg *config.PackingConfig, stats *SearchStats) {
	if stats.objectiveLowerBound != nil || (!beam && solver.Name() != "exact_small") || cfg.Objective() != "default" {
		return
	}
	stats.SetObjectiveLowerBound(ComputeObjectiveBounds(items, containers))
}

func (o *SolverOrchestrator) acrossContainers(solver SingleContainerSolver, items []domain.ItemInstance,
	containers []*domain.Container, cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline,
	preloaded []*domain.PackedContainer) planOutcome {
	_, defaultSelector := o.customSelector.(DefaultContainerSelector)
	beam := cfg.ContainerPlanBeamWidth() > 1 && (o.customSelector == nil || defaultSelector)
	recordRootBound(solver, beam, items, containers, cfg, stats)
	selector := o.customSelector
	if selector == nil {
		selector = DefaultContainerSelector{}
	}
	planner := &containerPlanner{selector, o.reasons}
	opened, reached := openPreloaded(solver, preloaded, items, containers, cfg, stats, deadline)
	if beam {
		return planner.plans(solver, items, opened, reached, containers, cfg, stats, deadline)
	}
	return planner.greedy(solver, items, opened, reached, containers, cfg, stats, deadline)
}

// openPreloaded fills every container holding fixed items, first, and keeps it whatever it
// gets, returning the plan every start continues from. A container that search cannot fill
// (the deadline has passed, or the solver hit it) is kept with its fixed items alone:
// dropping it would drop items the request says are already loaded. O(f * S) for f fixed
// containers.
func openPreloaded(solver SingleContainerSolver, preloaded []*domain.PackedContainer, items []domain.ItemInstance,
	containers []*domain.Container, cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) (plan, bool) {
	current := plan{nil, items, newInventory(containers), map[string]int64{}, true, true}
	reached := false
	for _, fixed := range preloaded {
		seeded := fixed.Container().WithPreloaded(fixed.Placements())
		var one *SingleContainerSolution
		if !reached && !deadline.Expired() {
			var solved SingleContainerSolution
			if !untilTimeLimit(func() {
				solved = solver.PackOne(seeded, fixed.Sequence(), current.remaining, cfg, stats, deadline)
			}) {
				one = &solved
			}
		}
		if one == nil || one.TimeLimitReached {
			reached = true
		}
		state := NewContainerState(seeded, fixed.Sequence())
		if one != nil {
			state = one.State
		}
		committed, ids := commitState(state)
		sequences := current.copySequences()
		sequences[seeded.ID()] = fixed.Sequence()
		current = plan{append(append([]*domain.PackedContainer(nil), current.packed...), committed),
			withoutIDs(current.remaining, ids), current.stock.taken(seeded.ID()), sequences,
			current.exhaustive && one != nil && one.Exhaustive, current.dominant && one != nil && one.DominantLattice}
	}
	return current, reached
}
