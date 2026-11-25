package algorithm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/units"
)

func kitchenOrder(t *testing.T) ([]domain.ItemInstance, []*domain.Container) {
	t.Helper()
	var instances []domain.ItemInstance
	for _, spec := range []struct {
		id                    string
		length, width, height int64
		grams, quantity       int64
	}{{"kettle", 310, 220, 140, 2000, 5}, {"toaster", 250, 250, 250, 3000, 4}, {"knife-block", 400, 150, 100, 1000, 6},
		{"scale", 180, 120, 90, 500, 8}} {
		itemSpec := domain.NewItemSpec(spec.id, domain.MustDimensions(mm(spec.length), mm(spec.width), mm(spec.height)))
		itemSpec.Weight, itemSpec.Quantity = grams(spec.grams), spec.quantity
		item, err := domain.NewItem(itemSpec)
		if err != nil {
			t.Fatal(err)
		}
		instances = append(instances, item.Instances()...)
	}
	var containers []*domain.Container
	for _, spec := range []struct {
		id                    string
		length, width, height int64
		cost                  int64
	}{{"small", 450, 350, 300, 100}, {"medium", 600, 400, 400, 160}, {"large", 800, 600, 500, 240}} {
		container, err := domain.NewContainer(domain.ContainerSpec{ID: spec.id,
			InnerDimensions: domain.MustDimensions(mm(spec.length), mm(spec.width), mm(spec.height)), CostMinor: spec.cost})
		if err != nil {
			t.Fatal(err)
		}
		containers = append(containers, container)
	}
	return instances, containers
}

// The start ids the reference's quality portfolio records for this scene at v1.6.0: after the
// greedy starts, one neighbourhood per item type and the beam, each re-running the best
// extreme-point answer found before it.
func TestTheQualityPortfolioRefinesTheBestExtremePointAnswer(t *testing.T) {
	items, containers := kitchenOrder(t)
	// Eight orders, as a JSON request that names the quality profile and nothing else gets.
	options := config.Default(config.Quality)
	options.TimeLimitMS, options.MultiStartOrders = 60_000, 8
	cfg, err := config.New(options)
	if err != nil {
		t.Fatal(err)
	}
	run := NewSolverOrchestrator(NoExtensions()).SolvePortfolio(items, containers, cfg, DeadlineOfMilliseconds(60_000))
	var ids []string
	for _, record := range run.Starts {
		ids = append(ids, record.ID)
	}
	expected := []string{"homogeneous_blocks:small_edge"}
	for _, solver := range []string{"extreme_points", "maximal_spaces"} {
		for _, order := range []string{"small_edge", "small_volume", "volume", "base_area", "seeded_4", "seeded_5",
			"seeded_6", "seeded_7"} {
			expected = append(expected, solver+":"+order+":greedy")
		}
	}
	expected = append(expected, "layer:small_edge:greedy",
		"extreme_points:volume:greedy:late_kettle", "extreme_points:volume:greedy:late_knife-block",
		"extreme_points:volume:greedy:late_scale", "extreme_points:volume:greedy:late_toaster",
		"extreme_points:volume:greedy:beam")
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("starts\n got %v\nwant %v", ids, expected)
	}
}

// Without the extreme-point solver there is nothing to refine: the quality portfolio runs
// its greedy starts only, as the reference's does.
func TestAQualityPortfolioWithoutTheExtremePointSolverRunsOnlyGreedyStarts(t *testing.T) {
	items, containers := kitchenOrder(t)
	options := config.Default(config.Quality)
	options.TimeLimitMS, options.Solvers = 60_000, []string{"maximal_spaces"}
	cfg, err := config.New(options)
	if err != nil {
		t.Fatal(err)
	}
	run := NewSolverOrchestrator(NoExtensions()).SolvePortfolio(items, containers, cfg, DeadlineOfMilliseconds(60_000))
	for _, record := range run.Starts {
		if !strings.HasSuffix(record.ID, ":greedy") {
			t.Fatalf("start %s is not greedy", record.ID)
		}
	}
	if _, eligible := refinementOf(start{orderName: bestBeam}, nil, map[string]start{}, cfg); eligible {
		t.Fatal("a refinement with no answer before it was run")
	}
}

// balanced adds the block start last on a multi-type load. The tag's Python records
// these starts for this scene and keeps its own extreme-point answer, which the block start
// does not strictly beat.
func TestBalancedRunsTheBlockStartLast(t *testing.T) {
	items, containers := kitchenOrder(t)
	options := config.Default(config.Balanced)
	options.TimeLimitMS = 60_000
	cfg, err := config.New(options)
	if err != nil {
		t.Fatal(err)
	}
	run := NewSolverOrchestrator(NoExtensions()).SolvePortfolio(items, containers, cfg, DeadlineOfMilliseconds(60_000))
	var ids []string
	for _, record := range run.Starts {
		ids = append(ids, record.ID)
	}
	expected := []string{"extreme_points:volume", "extreme_points:base_area", "extreme_points:seeded_2",
		"extreme_points:seeded_3", "extreme_points:seeded_4", "extreme_points:seeded_5", "extreme_points:seeded_6",
		"extreme_points:seeded_7", "layer:volume", "homogeneous_blocks:volume"}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("starts\n got %v\nwant %v", ids, expected)
	}
	for _, solution := range run.Solutions {
		if solution.SolverName == "homogeneous_blocks:volume" {
			t.Fatal("a block answer that only ties was kept")
		}
	}
}

// A late-type refinement searches wide again, at most four wide; the late-type
// neighbourhood itself stays greedy.
func TestALateTypeRefinementSearchesFourWide(t *testing.T) {
	options := config.Default(config.Quality)
	cfg, err := config.New(options)
	if err != nil {
		t.Fatal(err)
	}
	refinement := startConfigFor("extreme_points:volume:greedy:late_kettle:beam", cfg)
	if refinement.MaxCandidatesPerItem() != 4 || refinement.ContainerPlanBeamWidth() != 4 ||
		refinement.ContainerPlanNodeLimit() != cfg.ContainerPlanNodeLimit() {
		t.Fatalf("refinement searches %d x %d", refinement.MaxCandidatesPerItem(), refinement.ContainerPlanBeamWidth())
	}
	neighbourhood := startConfigFor("extreme_points:volume:greedy:late_kettle", cfg)
	if neighbourhood.MaxCandidatesPerItem() != 1 || neighbourhood.ContainerPlanBeamWidth() != 1 {
		t.Fatal("a late-type neighbourhood is not greedy")
	}
	if startConfigFor("extreme_points:volume:greedy:beam", cfg) != cfg {
		t.Fatal("a plain refinement does not keep the profile's width")
	}
}

// An item that overloads an axle wherever it stands alone is left out for the axle, and one a
// fresh container would take when none is left for the inventory.
func TestAnAxleAdmitsAnItemAloneOnlyWhereItsMomentFits(t *testing.T) {
	limit := grams(100)
	axles := [2]domain.Axle{domain.NewAxle(units.MustLength(mm(0)), &limit), domain.NewAxle(units.MustLength(mm(1000)), nil)}
	// Doubled centre 2x + 100 with the front reaction (1000 - centre) * w / 1000 <= 100 g.
	if !constraint.AxleAdmitsAlone(axles, 0, mm(1000), grams(200).Ticks(), mm(100)) {
		t.Fatal("an item that can stand near the rear axle is refused")
	}
	if constraint.AxleAdmitsAlone(axles, 0, mm(1000), grams(200).Ticks(), mm(1001)) {
		t.Fatal("an item longer than the container is admitted")
	}
	both := [2]domain.Axle{domain.NewAxle(units.MustLength(mm(0)), &limit), domain.NewAxle(units.MustLength(mm(1000)), &limit)}
	if constraint.AxleAdmitsAlone(both, 0, mm(1000), grams(300).Ticks(), mm(100)) {
		t.Fatal("an item heavier than both axles together is admitted")
	}
}

// maximum_volume leads with the share of the requested volume left behind, rounded up so an
// incomplete answer never scores zero; the piece count only follows.
func TestMaximumVolumeRanksTheVolumeLeftBehindFirst(t *testing.T) {
	small, large := itemOf(t, "small", 10), itemOf(t, "large", 20)
	container, err := domain.NewContainer(domain.ContainerSpec{ID: "box",
		InnerDimensions: domain.MustDimensions(mm(30), mm(30), mm(30))})
	if err != nil {
		t.Fatal(err)
	}
	place := func(instance domain.ItemInstance) *domain.PackedContainer {
		dimensions := instance.Item().Dimensions()
		return domain.NewPackedContainer(container, 1, []domain.Placement{domain.NewPlacement(instance, domain.Point{},
			domain.LWH, dimensions, domain.Point{}, dimensions, 1)})
	}
	keptLarge := RawSolution{Containers: []*domain.PackedContainer{place(large)},
		Unpacked: []domain.UnpackedItem{domain.NewUnpackedItem(small, "x", nil)}, Stats: &SearchStats{}}
	keptSmall := RawSolution{Containers: []*domain.PackedContainer{place(small)},
		Unpacked: []domain.UnpackedItem{domain.NewUnpackedItem(large, "x", nil)}, Stats: &SearchStats{}}
	scorer := MaximumVolumeSolutionScorer{}
	// ceil(1000 * 10^6 / 9000) and ceil(8000 * 10^6 / 9000).
	if got := scorer.Score(keptLarge)[0]; got != 111_112 {
		t.Fatalf("share %d, want 111112", got)
	}
	if got := scorer.Score(keptSmall)[0]; got != 888_889 {
		t.Fatalf("share %d, want 888889", got)
	}
	if scorer.Score(keptLarge).Compare(scorer.Score(keptSmall)) >= 0 {
		t.Fatal("leaving the large piece behind ranks ahead")
	}
}

func itemOf(t *testing.T, id string, side int64) domain.ItemInstance {
	t.Helper()
	item, err := domain.NewItem(domain.NewItemSpec(id, domain.MustDimensions(mm(side), mm(side), mm(side))))
	if err != nil {
		t.Fatal(err)
	}
	return item.Instances()[0]
}
