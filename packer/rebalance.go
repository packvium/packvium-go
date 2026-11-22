package packer

import (
	"sort"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/units"
	"github.com/packvium/packvium-go/validation"
)

// WeightMove is one item relocated from one already-packed container to another.
type WeightMove struct {
	ItemID          string
	FromContainerID string
	ToContainerID   string
}

// RebalanceResult is the rebalanced containers and the moves that produced them, in order.
type RebalanceResult struct {
	Containers []*domain.PackedContainer
	Moves      []WeightMove
}

// Improved reports whether any move was made.
func (r RebalanceResult) Improved() bool { return len(r.Moves) > 0 }

// RebalanceOptions bound the pass: at most MaxMoves committed moves, and TimeLimitMS of wall
// clock for the whole pass. A zero MaxMoves makes no move.
type RebalanceOptions struct {
	MaxMoves    int
	TimeLimitMS int64
}

// DefaultRebalanceOptions are the reference's defaults: 64 moves within one second.
func DefaultRebalanceOptions() RebalanceOptions {
	return RebalanceOptions{MaxMoves: 64, TimeLimitMS: 1000}
}

// rebalanceDeadline starts the pass's clock; a test replaces it to stop the search midway.
var rebalanceDeadline = algorithm.DeadlineOfMilliseconds

// RebalanceWeight redistributes payload weight across an already-solved multi-container
// packing. It is an explicit, opt-in pass to call after Pack, never run inside it. unpacked
// should be what Pack returned, so that the validator accounts for every requested instance,
// not only the ones currently in a container. A nil cfg is the balanced default.
//
// Every candidate move is simulated in full and the whole resulting packing is checked by the
// independent validator before it is committed, so an item is never dropped, duplicated or
// left resting on a gap. Greedy and best-effort, not globally optimal: each round moves at
// most one item, from the heaviest container, trying its placements heaviest first against
// the other containers lightest first, and commits the first move that fits and strictly
// narrows the gap between the heaviest and the lightest payload. The spread is a strictly
// decreasing sequence of non-negative integers, so the pass ends on its own; MaxMoves bounds
// it again. Each round is O(p * c) candidate moves for p placements in the heaviest container
// and c containers, each one candidate search plus one validator pass over the packing.
//
// A container held as a compact lattice is expanded to its placements first: a move must
// see every item already in its destination.
func RebalanceWeight(request *domain.PackingRequest, containers []*domain.PackedContainer,
	unpacked []domain.UnpackedItem, cfg *config.PackingConfig, options RebalanceOptions) (RebalanceResult, error) {
	if cfg == nil {
		balanced, err := config.New(config.Default(config.Balanced))
		if err != nil {
			return RebalanceResult{}, err
		}
		cfg = balanced
	}
	if err := refuseUnpriceableObjectives(cfg, request); err != nil {
		return RebalanceResult{}, err
	}
	// A packing the tariff cannot price is refused here for the reason Pack refuses one on the
	// way out: rebalancing it would hand back a shipment with no published price.
	if unpriceable := algorithm.UnpriceableContainer(containers, cfg); unpriceable != nil {
		return RebalanceResult{}, unratedWeight(unpriceable)
	}
	constraints, err := constraint.Defaults(cfg.MinimumSupportRatio(), nil)
	if err != nil {
		return RebalanceResult{}, err
	}
	pass := rebalancePass{request, unpacked, cfg, constraints, rebalanceDeadline(options.TimeLimitMS)}
	working := explicitPlacements(containers)
	var moves []WeightMove
	for round := 0; round < options.MaxMoves; round++ {
		if len(working) < 2 || pass.deadline.Expired() {
			break
		}
		trial, move, found := pass.bestMove(working)
		if !found {
			break
		}
		moves = append(moves, move)
		working = trial
	}
	return RebalanceResult{Containers: working, Moves: moves}, nil
}

// rebalancePass is what every round of one RebalanceWeight call shares.
type rebalancePass struct {
	request     *domain.PackingRequest
	unpacked    []domain.UnpackedItem
	cfg         *config.PackingConfig
	constraints []constraint.PlacementConstraint
	deadline    *algorithm.Deadline
}

func explicitPlacements(containers []*domain.PackedContainer) []*domain.PackedContainer {
	working := make([]*domain.PackedContainer, len(containers))
	for index, packed := range containers {
		working[index] = packed
		if packed.LatticeSummary() != nil {
			working[index] = domain.NewPackedContainer(packed.Container(), packed.Sequence(), packed.ExpandPlacements())
		}
	}
	return working
}

// bestMove is the first sound move out of the heaviest container that strictly narrows the
// payload spread, with the container list it produces.
func (p rebalancePass) bestMove(working []*domain.PackedContainer) ([]*domain.PackedContainer, WeightMove, bool) {
	weights := make([]int64, len(working))
	for index, packed := range working {
		weights[index] = packed.PayloadWeight().Ticks()
	}
	spread := spreadOf(weights)
	if spread <= 0 {
		return nil, WeightMove{}, false
	}
	sourceIndex := heaviest(weights)
	source := working[sourceIndex]
	placements := source.Placements()
	for _, placementIndex := range heaviestPlacementsFirst(placements) {
		moving := placements[placementIndex]
		weight := moving.Instance().Item().Weight().Ticks()
		if moving.Fixed() || weight <= 0 {
			continue
		}
		for _, destinationIndex := range lightestOthersFirst(weights, sourceIndex) {
			projected := append([]int64(nil), weights...)
			projected[sourceIndex] -= weight
			projected[destinationIndex] += weight
			if spreadOf(projected) >= spread {
				continue
			}
			trial := p.attemptMove(working, sourceIndex, destinationIndex, placementIndex)
			// A move that prices the destination past its tariff is not an improvement: the
			// unpriceable sentinel must never ride out through a rebalanced packing.
			if trial == nil || algorithm.UnpriceableContainer(trial, p.cfg) != nil {
				continue
			}
			return trial, WeightMove{moving.Instance().ID(), source.ID(), working[destinationIndex].ID()}, true
		}
	}
	return nil, WeightMove{}, false
}

// attemptMove relocates one placement and returns the new container list if the whole
// packing is sound, else nil. Both containers are replaced together in the returned list, so
// there is no state in which the item belongs to neither or to both. A deadline reached
// during the candidate search declines the move rather than failing the pass.
func (p rebalancePass) attemptMove(working []*domain.PackedContainer, sourceIndex, destinationIndex,
	placementIndex int) (trial []*domain.PackedContainer) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(algorithm.TimeLimitReached); !ok {
				panic(recovered)
			}
			trial = nil
		}
	}()
	source, destination := working[sourceIndex], working[destinationIndex]
	sourcePlacements, destinationPlacements := source.Placements(), destination.Placements()
	moving := sourcePlacements[placementIndex]
	state := algorithm.NewContainerState(destination.Container(), destination.Sequence())
	for _, placement := range destinationPlacements {
		state.Add(placement)
	}
	candidates := algorithm.FindCandidates(state, moving.Instance(), p.cfg, p.constraints, &algorithm.SearchStats{},
		p.deadline, algorithm.DefaultCandidateScorer{}, 1, nil)
	if len(candidates) == 0 {
		return nil
	}
	candidate := candidates[0]
	placed := domain.NewPlacement(moving.Instance(), candidate.Position, candidate.Rotation, candidate.Dimensions,
		candidate.Point, candidate.EnvelopeDimensions, algorithm.SupportRatio(state, moving.Instance(), candidate))
	remaining := append(append([]domain.Placement(nil), sourcePlacements[:placementIndex]...),
		sourcePlacements[placementIndex+1:]...)
	trial = append([]*domain.PackedContainer(nil), working...)
	trial[sourceIndex] = domain.NewPackedContainer(source.Container(), source.Sequence(), withRefreshedTopLoads(remaining))
	trial[destinationIndex] = domain.NewPackedContainer(destination.Container(), destination.Sequence(),
		withRefreshedTopLoads(append(destinationPlacements, placed)))
	clearance := p.cfg.Clearance()
	report := validation.ValidateWithUnpacked(p.request, trial, p.cfg.MinimumSupportRatio(), &clearance, p.unpacked)
	if !report.Valid() {
		return nil
	}
	return trial
}

// withRefreshedTopLoads re-derives every placement's reported top load from the current
// stack: moving an item changes what rests on what.
func withRefreshedTopLoads(placements []domain.Placement) []domain.Placement {
	loads := constraint.TopLoads(constraint.Units(placements))
	refreshed := make([]domain.Placement, len(placements))
	for index, placement := range placements {
		refreshed[index] = placement.WithTopLoad(units.MustWeight(loads[index]))
	}
	return refreshed
}

func spreadOf(weights []int64) int64 {
	lightest, heaviestWeight := weights[0], weights[0]
	for _, weight := range weights[1:] {
		if weight < lightest {
			lightest = weight
		}
		if weight > heaviestWeight {
			heaviestWeight = weight
		}
	}
	return heaviestWeight - lightest
}

// heaviest is the first index holding the largest weight.
func heaviest(weights []int64) int {
	chosen := 0
	for index, weight := range weights {
		if weight > weights[chosen] {
			chosen = index
		}
	}
	return chosen
}

// heaviestPlacementsFirst orders placement indices by descending item weight, ties in place.
func heaviestPlacementsFirst(placements []domain.Placement) []int {
	order := make([]int, len(placements))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool {
		return placements[order[i]].Instance().Item().Weight().Ticks() > placements[order[j]].Instance().Item().Weight().Ticks()
	})
	return order
}

// lightestOthersFirst orders every container but source by ascending weight, ties in place.
func lightestOthersFirst(weights []int64, source int) []int {
	order := make([]int, 0, len(weights)-1)
	for index := range weights {
		if index != source {
			order = append(order, index)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return weights[order[i]] < weights[order[j]] })
	return order
}
