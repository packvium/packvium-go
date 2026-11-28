package algorithm

import (
	"strconv"
	"testing"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/units"
)

// After the reference's tests/test_block_lattice.py and tests/test_solvers.py at v1.7.0. The
// reference draws its scenes from its own generator; these come from the shared one, so they
// are other scenes held to the same two properties.

func lookaheadScene(t *testing.T, seed int64) (*domain.Container, []domain.ItemInstance) {
	t.Helper()
	random := NewDeterministicRandom(seed)
	between := func(low, high int64) int64 { return low + random.NextInt(high-low+1) }
	spec := domain.ContainerSpec{ID: "box",
		InnerDimensions: domain.MustDimensions(mm(between(150, 400)), mm(between(120, 300)), mm(between(100, 260)))}
	if random.NextInt(3) == 0 {
		limit := between(5, 40)
		spec.MaxItems = &limit
	}
	if random.NextInt(3) == 0 {
		payload := units.MustWeight(between(5, 60) * 1000 * 8_000_000)
		spec.MaxPayload = &payload
	}
	box, err := domain.NewContainer(spec)
	if err != nil {
		t.Fatal(err)
	}
	var items []domain.ItemInstance
	types := between(2, 4)
	for index := int64(0); index < types; index++ {
		item := domain.NewItemSpec("t"+strconv.FormatInt(index, 10),
			domain.MustDimensions(mm(between(20, 90)), mm(between(20, 90)), mm(between(20, 90))))
		item.Weight = units.MustWeight(between(1, 3) * 1000 * 8_000_000)
		item.Quantity = between(1, 30)
		built, err := domain.NewItem(item)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, built.Instances()...)
	}
	return box, items
}

func blockConfig(t *testing.T, profile config.SolverProfile, nodeLimit int64) *config.PackingConfig {
	t.Helper()
	options := config.Default(profile)
	options.ContainerPlanNodeLimit = nodeLimit
	return blockTestConfig(t, options)
}

func blockAnswer(box *domain.Container, items []domain.ItemInstance, cfg *config.PackingConfig) SingleContainerSolution {
	return NewHomogeneousBlockSolver(nil).PackOne(box, 1, items, cfg, &SearchStats{}, DeadlineOfMilliseconds(600_000))
}

func TestTheQualityLookaheadNeverRanksBelowTheGreedyPass(t *testing.T) {
	improved := 0
	for seed := int64(0); seed < 24; seed++ {
		box, items := lookaheadScene(t, seed)
		looked := blockAnswer(box, items, blockConfig(t, config.Quality, 100_000))
		greedy := blockAnswer(box, items, blockConfig(t, config.Balanced, 100_000))
		order := blockSolutionKey(looked, false).Compare(blockSolutionKey(greedy, false))
		if order > 0 {
			t.Errorf("seed %d: the lookahead ranks below the greedy pass", seed)
		}
		if order < 0 {
			improved++
		}
	}
	// Counted, so a lookahead that never ran cannot pass as one that never lost.
	if improved == 0 {
		t.Error("the lookahead improved no scene")
	}
}

func TestAxlePointsAlreadyOfferedAreNotOfferedAgain(t *testing.T) {
	point := domain.MustPoint
	ordinary := []domain.Point{point(0, 0, 0), point(5, 0, 0), point(0, 5, 0)}
	axle := []domain.Point{point(7, 0, 0), point(5, 0, 0), point(7, 5, 0), point(7, 0, 0), point(0, 5, 0)}
	got := appendUnseen(append([]domain.Point(nil), ordinary...), axle)
	want := append(ordinary, point(7, 0, 0), point(7, 5, 0))
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%v", got)
		}
	}
}

// Seed -7 reduces to its residue modulo 2^31, as in the reference.
func TestANegativeSeedShufflesAsTheReferenceDoes(t *testing.T) {
	want := []int{5, 0, 3, 4, 8, 7, 6, 9, 2, 1}
	for _, seed := range []int64{-7, 1<<31 - 7} {
		got := NewDeterministicRandom(seed).Permutation(10)
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("seed %d: %v", seed, got)
			}
		}
	}
}
