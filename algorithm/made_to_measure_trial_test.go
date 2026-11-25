package algorithm

import (
	"testing"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
)

// scriptedSolver places the first item on its first call and runs out of time on every later
// one, or on every call when cutAtOnce is set.
type scriptedSolver struct {
	calls     int
	cutAtOnce bool
}

func (*scriptedSolver) Name() string           { return "scripted" }
func (*scriptedSolver) OrderInsensitive() bool { return false }

func (s *scriptedSolver) PackOne(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) SingleContainerSolution {
	s.calls++
	if s.cutAtOnce || s.calls > 1 {
		panic(TimeLimitReached{"packing time limit reached"})
	}
	state := NewContainerState(container, sequence)
	dimensions := items[0].Item().Dimensions()
	state.Add(domain.NewPlacement(items[0], domain.Point{}, domain.LWH, dimensions, domain.Point{}, dimensions, 1))
	return SingleContainerSolution{State: state, Unpacked: items[1:]}
}

func madeToMeasureScene(t *testing.T) (*domain.Container, []domain.ItemInstance, *config.PackingConfig) {
	t.Helper()
	machine, err := domain.NewMadeToMeasure([3]int64{mm(60), mm(60), mm(60)}, [3]int64{mm(25), mm(25), mm(25)}, 0, 0,
		[3]int64{})
	if err != nil {
		t.Fatal(err)
	}
	container, err := domain.NewContainer(domain.ContainerSpec{ID: "machine", MadeToMeasure: machine,
		InnerDimensions: domain.MustDimensions(mm(1510), mm(1510), mm(1510))})
	if err != nil {
		t.Fatal(err)
	}
	spec := domain.NewItemSpec("cube", domain.MustDimensions(mm(100), mm(100), mm(100)))
	spec.Quantity = 8
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.New(config.Default(config.Fast))
	if err != nil {
		t.Fatal(err)
	}
	return container, item.Instances(), cfg
}

// A time limit before any rung kept an answer unwinds the trial, as a declared container's
// solve would.
func TestATrialCutBeforeAnyAnswerUnwinds(t *testing.T) {
	container, items, cfg := madeToMeasureScene(t)
	planner := &containerPlanner{DefaultContainerSelector{}, nil}
	if !untilTimeLimit(func() {
		planner.trial(&scriptedSolver{cutAtOnce: true}, container, 1, items, cfg, &SearchStats{},
			DeadlineOfMilliseconds(60_000))
	}) {
		t.Fatal("the trial swallowed the time limit")
	}
}

// A time limit after a rung placed part of the round keeps that rung, cut to its contents
// and marked as stopped by the limit.
func TestATrialCutLaterKeepsTheBestRungSoFar(t *testing.T) {
	container, items, cfg := madeToMeasureScene(t)
	planner := &containerPlanner{DefaultContainerSelector{}, nil}
	one, solves := planner.trial(&scriptedSolver{}, container, 1, items, cfg, &SearchStats{},
		DeadlineOfMilliseconds(60_000))
	if solves != 1 || !one.TimeLimitReached || one.State.PlacementCount() != 1 {
		t.Fatalf("solves %d, limit %v, placed %d", solves, one.TimeLimitReached, one.State.PlacementCount())
	}
	if got := one.State.Container.InnerDimensions(); got != domain.MustDimensions(mm(110), mm(110), mm(110)) {
		t.Fatalf("box %v, want the 110 mm cube the contents call for", got)
	}
}
