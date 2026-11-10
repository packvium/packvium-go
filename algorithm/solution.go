package algorithm

import (
	"github.com/toxakara/packvium-go/config"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/result"
)

// Candidate is one feasible placement: envelope corner, physical position, orientation and
// its score.
type Candidate struct {
	Point              domain.Point
	Position           domain.Point
	Rotation           domain.Rotation
	Dimensions         domain.Dimensions
	EnvelopeDimensions domain.Dimensions
	Score              SortKey
}

// SingleContainerSolution is one container's packing from a single-container solver.
// Exhaustive is true only when the search space was explored to the end; DominantLattice only
// when the grid really built the container from its lattice arithmetic, since a delegated
// answer is not the provably dominant one the portfolio short-circuit assumes.
type SingleContainerSolution struct {
	State            *ContainerState
	Unpacked         []domain.ItemInstance
	Exhaustive       bool
	TimeLimitReached bool
	DominantLattice  bool
}

func plainSolution(state *ContainerState, unpacked []domain.ItemInstance) SingleContainerSolution {
	return SingleContainerSolution{State: state, Unpacked: unpacked}
}

// SingleContainerSolver packs as much of an item list as it can into one container.
type SingleContainerSolver interface {
	Name() string
	// OrderInsensitive is true for a solver that re-sorts its input, so one ordering is all the
	// portfolio needs to try.
	OrderInsensitive() bool
	PackOne(container *domain.Container, sequence int64, items []domain.ItemInstance, cfg *config.PackingConfig,
		stats *SearchStats, deadline *Deadline) SingleContainerSolution
}

// RawSolution is one start's answer before it is scored, validated and ranked.
type RawSolution struct {
	SolverName         string
	Containers         []*domain.PackedContainer
	Unpacked           []domain.UnpackedItem
	Stats              *SearchStats
	TimeLimitReached   bool
	EffortLimitReached bool
	Exhaustive         bool
}

// PortfolioRun is every start's answer and how each start ended.
type PortfolioRun struct {
	Solutions []RawSolution
	Starts    []result.StartRecord
}

// start is one portfolio start: a solver, the name of the item order it runs on, and that
// order.
type start struct {
	solver    SingleContainerSolver
	orderName string
	order     []domain.ItemInstance
}

func (s start) id() string { return s.solver.Name() + ":" + s.orderName }
