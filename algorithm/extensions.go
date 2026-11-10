package algorithm

import (
	"math/big"

	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
)

// CandidateScorer ranks feasible placements; lower sorts first. It takes the origin and
// envelope rather than a Candidate so the finder need not build a throwaway value per point
// and rotation.
type CandidateScorer interface {
	Score(state *ContainerState, point domain.Point, envelope domain.Dimensions) SortKey
}

// ContainerSelector ranks one container's packing against the other offered containers;
// lower wins.
type ContainerSelector interface {
	Score(container *domain.Container, solution SingleContainerSolution) SortKey
}

// DefaultCandidateScorer prefers low, tight placements. Every component is an exact integer.
type DefaultCandidateScorer struct{}

// Score is (z, new stack height, leftover x*y + z, y, x).
func (DefaultCandidateScorer) Score(state *ContainerState, point domain.Point, envelope domain.Dimensions) SortKey {
	inner := state.Container.InnerDimensions()
	top := point.Z() + envelope.Height().Ticks()
	rx := inner.Length().Ticks() - (point.X() + envelope.Length().Ticks())
	ry := inner.Width().Ticks() - (point.Y() + envelope.Width().Ticks())
	rz := inner.Height().Ticks() - top
	return SortKey{Int(point.Z()), Int(ints.Max(state.MaxZ, top)), Int(ints.AddExact(ints.MulExact(rx, ry), rz)),
		Int(point.Y()), Int(point.X())}
}

// DefaultContainerSelector prefers the container that holds the most items, then the
// cheapest, then the tightest.
type DefaultContainerSelector struct{}

// Score is (-placed, cost, leftover volume, id).
func (DefaultContainerSelector) Score(container *domain.Container, solution SingleContainerSolution) SortKey {
	state := solution.State
	var used *big.Int
	if state.LatticeSummary != nil {
		used = state.LatticeSummary.UsedVolume()
	} else {
		used = domain.UsedVolume(state.Placements)
	}
	leftover := new(big.Int).Sub(container.InnerDimensions().Volume(), used)
	return SortKey{Int(-state.PlacementCount()), Int(container.CostMinor()), Big(leftover), Str(container.ID())}
}

// ExtensionRegistry is caller-supplied constraints, orderings, solvers and scorers; empty
// means the built-ins only. A nil scorer or selector is the built-in one.
type ExtensionRegistry struct {
	placementConstraints []constraint.PlacementConstraint
	itemOrderStrategies  []ItemOrderStrategy
	solvers              []SingleContainerSolver
	candidateScorer      CandidateScorer
	containerSelector    ContainerSelector
}

// NewExtensionRegistry freezes copies of the caller's extensions.
func NewExtensionRegistry(constraints []constraint.PlacementConstraint, orders []ItemOrderStrategy,
	solvers []SingleContainerSolver, scorer CandidateScorer, selector ContainerSelector) *ExtensionRegistry {
	return &ExtensionRegistry{
		placementConstraints: append([]constraint.PlacementConstraint(nil), constraints...),
		itemOrderStrategies:  append([]ItemOrderStrategy(nil), orders...),
		solvers:              append([]SingleContainerSolver(nil), solvers...),
		candidateScorer:      scorer, containerSelector: selector,
	}
}

// NoExtensions is the built-ins only.
func NoExtensions() *ExtensionRegistry { return &ExtensionRegistry{} }

// ExtensionsWithConstraints adds placement constraints and nothing else.
func ExtensionsWithConstraints(constraints []constraint.PlacementConstraint) *ExtensionRegistry {
	return NewExtensionRegistry(constraints, nil, nil, nil, nil)
}

func (r *ExtensionRegistry) PlacementConstraints() []constraint.PlacementConstraint {
	return append([]constraint.PlacementConstraint(nil), r.placementConstraints...)
}
func (r *ExtensionRegistry) ItemOrderStrategies() []ItemOrderStrategy {
	return append([]ItemOrderStrategy(nil), r.itemOrderStrategies...)
}
func (r *ExtensionRegistry) Solvers() []SingleContainerSolver {
	return append([]SingleContainerSolver(nil), r.solvers...)
}
func (r *ExtensionRegistry) CandidateScorer() CandidateScorer     { return r.candidateScorer }
func (r *ExtensionRegistry) ContainerSelector() ContainerSelector { return r.containerSelector }

// defaultConstraints is the built-in pipeline plus the caller's constraints, fresh for one
// solve because some of them cache per-state work.
func defaultConstraints(minimumSupportRatio float64, custom []constraint.PlacementConstraint) []constraint.PlacementConstraint {
	constraints, err := constraint.Defaults(minimumSupportRatio, nil, custom...)
	if err != nil {
		refuse(err)
	}
	return constraints
}
