package algorithm

import (
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
)

// ExtremePointSolver runs the candidate beam over the item order it is given.
type ExtremePointSolver struct {
	constraints []constraint.PlacementConstraint
	scorer      CandidateScorer
}

// NewExtremePointSolver runs the built-in pipeline plus constraints, ranked by scorer.
func NewExtremePointSolver(constraints []constraint.PlacementConstraint, scorer CandidateScorer) *ExtremePointSolver {
	return &ExtremePointSolver{append([]constraint.PlacementConstraint(nil), constraints...), scorer}
}

func (*ExtremePointSolver) Name() string           { return "extreme_points" }
func (*ExtremePointSolver) OrderInsensitive() bool { return false }

// PackOne packs items in the given order.
func (s *ExtremePointSolver) PackOne(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) SingleContainerSolution {
	return BeamPack(container, sequence, items, cfg, defaultConstraints(cfg.MinimumSupportRatio(), s.constraints),
		stats, deadline, s.scorer)
}

// LayerSolver runs the candidate beam over a footprint-first order.
type LayerSolver struct {
	constraints []constraint.PlacementConstraint
	scorer      CandidateScorer
}

// NewLayerSolver runs the built-in pipeline plus constraints, ranked by scorer.
func NewLayerSolver(constraints []constraint.PlacementConstraint, scorer CandidateScorer) *LayerSolver {
	return &LayerSolver{append([]constraint.PlacementConstraint(nil), constraints...), scorer}
}

func (*LayerSolver) Name() string { return "layer" }

// OrderInsensitive: every layer start re-sorts by a total key ending in the instance id, so
// one ordering is enough.
func (*LayerSolver) OrderInsensitive() bool { return true }

// PackOne sorts by footprint, then volume, then weight, all descending, behind the shared
// lead keys.
func (s *LayerSolver) PackOne(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) SingleContainerSolution {
	ordered := sortInstances(items, func(item domain.ItemInstance) SortKey {
		dimensions := item.Item().Dimensions()
		return leadKey(item, cfg, Int(-dimensions.BaseArea()), Big(negated(dimensions.Volume())),
			Int(-item.Item().Weight().Ticks()), Str(item.ID()))
	})
	return BeamPack(container, sequence, ordered, cfg, defaultConstraints(cfg.MinimumSupportRatio(), s.constraints),
		stats, deadline, s.scorer)
}
