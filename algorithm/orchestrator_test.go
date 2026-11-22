package algorithm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
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

// The start ids the reference's quality portfolio records for this scene at v1.5.0: after the
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
