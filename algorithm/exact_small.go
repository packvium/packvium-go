package algorithm

import (
	"math/big"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
)

// ExactSmallSolver is a depth-first branch and bound over whole group batches. It is exact
// only for the discrete candidate model and the item-count objective, so it publishes no
// global optimality claim. O(product of per-batch candidate counts) in the worst case, which
// is why it is gated on exactItemLimit.
type ExactSmallSolver struct {
	constraints []constraint.PlacementConstraint
	scorer      CandidateScorer
}

// NewExactSmallSolver runs the built-in pipeline plus constraints, ranked by scorer.
func NewExactSmallSolver(constraints []constraint.PlacementConstraint, scorer CandidateScorer) *ExactSmallSolver {
	return &ExactSmallSolver{append([]constraint.PlacementConstraint(nil), constraints...), scorer}
}

func (*ExactSmallSolver) Name() string           { return "exact_small" }
func (*ExactSmallSolver) OrderInsensitive() bool { return false }

// placedVolume is each item's own physical volume, not the state's used volume: that one
// deliberately removes nesting overlap and is a different quantity.
func placedVolume(state *ContainerState) *big.Int {
	volume := new(big.Int)
	for _, placement := range state.Placements {
		volume.Add(volume, placement.Dimensions().Volume())
	}
	return volume
}

// IsBetterState reports whether candidate should replace incumbent: more items placed wins,
// then more placed volume. Without the second key an equal-count but worse arrangement found
// later replaced a better one, so more search time made the answer worse.
func IsBetterState(candidate, incumbent *ContainerState) bool {
	if len(candidate.Placements) != len(incumbent.Placements) {
		return len(candidate.Placements) > len(incumbent.Placements)
	}
	return placedVolume(candidate).Cmp(placedVolume(incumbent)) > 0
}

func landedCharge(state *ContainerState, cfg *config.PackingConfig) int64 {
	return ChargeOrUnpriceable(state.Container, BilledTicksForConfig(state.Container, state.PayloadTicks, cfg))
}

func isBetterForObjective(candidate *ContainerState, candidateVolume *big.Int, incumbent *ContainerState,
	incumbentVolume *big.Int, cfg *config.PackingConfig) bool {
	if len(candidate.Placements) != len(incumbent.Placements) {
		return len(candidate.Placements) > len(incumbent.Placements)
	}
	if cfg.Objective() == LandedCostObjective {
		if candidateCharge, incumbentCharge := landedCharge(candidate, cfg), landedCharge(incumbent, cfg); candidateCharge != incumbentCharge {
			return candidateCharge < incumbentCharge
		}
	}
	return candidateVolume.Cmp(incumbentVolume) > 0
}

// PackOne refuses more items than the configured limit.
func (s *ExactSmallSolver) PackOne(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) SingleContainerSolution {
	if int64(len(items)) > cfg.ExactItemLimit() {
		refuse(ExactSmallLimitError{})
	}
	search := newExactSearch(s, container, sequence, items, cfg,
		defaultConstraints(cfg.MinimumSupportRatio(), s.constraints), stats, deadline)
	// A cut leaves the best state so far standing; the deadline flag below reports it.
	untilTimeLimit(func() { search.dfs(0, NewContainerState(container, sequence), len(items), new(big.Int)) })
	packed := map[string]bool{}
	for _, placement := range search.best.Placements {
		packed[placement.Instance().ID()] = true
	}
	var unpacked []domain.ItemInstance
	for _, item := range items {
		if !packed[item.ID()] {
			unpacked = append(unpacked, item)
		}
	}
	// Proves only the maximum item count reachable through the generated points; it stops at
	// the first full packing and does not minimise the rest of the objective vector.
	return SingleContainerSolution{State: search.best, Unpacked: unpacked, TimeLimitReached: deadline.Expired()}
}

// exactSearch is one branch-and-bound run; its incumbent is mutable per-call state.
type exactSearch struct {
	solver        *ExactSmallSolver
	batches       [][]domain.ItemInstance
	batchVolumes  []*big.Int
	suffixVolumes []*big.Int
	cfg           *config.PackingConfig
	constraints   []constraint.PlacementConstraint
	stats         *SearchStats
	deadline      *Deadline
	total         int
	best          *ContainerState
	bestVolume    *big.Int
}

func newExactSearch(solver *ExactSmallSolver, container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, constraints []constraint.PlacementConstraint, stats *SearchStats,
	deadline *Deadline) *exactSearch {
	batches := Batches(items)
	volumes := make([]*big.Int, len(batches))
	for index, batch := range batches {
		volumes[index] = new(big.Int)
		for _, item := range batch {
			volumes[index].Add(volumes[index], item.Item().Dimensions().Volume())
		}
	}
	suffix := make([]*big.Int, len(batches)+1)
	suffix[len(batches)] = new(big.Int)
	for index := len(batches) - 1; index >= 0; index-- {
		suffix[index] = new(big.Int).Add(volumes[index], suffix[index+1])
	}
	return &exactSearch{solver, batches, volumes, suffix, cfg, constraints, stats, deadline, len(items),
		NewContainerState(container, sequence), new(big.Int)}
}

func (s *exactSearch) dfs(index int, state *ContainerState, reachable int, stateVolume *big.Int) {
	if s.deadline.Expired() {
		return
	}
	s.stats.SearchNodesExpanded++
	stateCount := len(state.Placements)
	if isBetterForObjective(state, stateVolume, s.best, s.bestVolume, s.cfg) {
		s.best, s.bestVolume = state, stateVolume
	}
	if index >= len(s.batches) {
		return
	}
	potentialCount, bestCount := stateCount+reachable, len(s.best.Placements)
	if potentialCount < bestCount {
		return
	}
	if s.cfg.Objective() != LandedCostObjective && potentialCount == bestCount &&
		new(big.Int).Add(stateVolume, s.suffixVolumes[index]).Cmp(s.bestVolume) <= 0 {
		return
	}
	batch := s.batches[index]
	remaining := reachable - len(batch)
	childVolume := new(big.Int).Add(stateVolume, s.batchVolumes[index])
	for _, child := range placeBatch(state, batch, s.cfg, s.constraints, s.stats, s.deadline, s.solver.scorer, unlimited) {
		s.dfs(index+1, child, remaining, childVolume)
		if len(s.best.Placements) == s.total || s.deadline.Expired() {
			return
		}
	}
	s.dfs(index+1, state, remaining, stateVolume)
}
