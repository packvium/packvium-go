package constraint

import (
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/units"
)

// Expected values come from the v1.0.0 reference engine.

func box(x, y, z, l, w, h int64) domain.Box {
	return domain.NewBox(domain.MustPoint(x, y, z), domain.MustDimensions(l, w, h))
}

var scene = []domain.Box{box(0, 0, 0, 10, 10, 10), box(10, 0, 0, 10, 10, 10), box(5, 0, 10, 10, 10, 10),
	box(0, 0, 20, 7, 10, 5), box(20, 0, 0, 3, 3, 3)}

func int64Pointer(value int64) *int64 { return &value }

func sceneUnits() []LoadUnit {
	weights := []int64{1000, 1000, 1001, 333, 5}
	out := make([]LoadUnit, len(scene))
	for index, b := range scene {
		out[index] = LoadUnit{Box: b, WeightTicks: weights[index], Label: "u" + string(rune('0'+index))}
	}
	out[0].MaxTopLoadTicks = int64Pointer(500)
	out[1].MaxStackedItems = int64Pointer(1)
	return out
}

func TestContactGraphFindsFaceContactInIndexOrder(t *testing.T) {
	graph := NewContactGraph(scene, 1)
	if len(graph.Supporters(0)) != 0 || len(graph.Children(4)) != 0 {
		t.Fatalf("unexpected edges: %v %v", graph.Supporters(0), graph.Children(4))
	}
	assertEqual(t, []ContactEdge{{0, 50}, {1, 50}}, graph.Supporters(2))
	assertEqual(t, []ContactEdge{{2, 20}}, graph.Supporters(3))
	assertEqual(t, []int{2}, graph.Children(0))
	assertEqual(t, []int{3}, graph.Children(2))
}

func TestAppendingABoxMatchesAFreshBuild(t *testing.T) {
	incremental := NewContactGraph(scene[:2], 10)
	for _, b := range scene[2:] {
		incremental = incremental.WithBox(b)
	}
	fresh := NewContactGraph(scene, 1)
	for index := range scene {
		assertEqual(t, fresh.Supporters(index), incremental.Supporters(index))
		assertEqual(t, fresh.Children(index), incremental.Children(index))
	}
	all := sceneUnits()
	grown := NewLoadSupportGraph(all[:4], 10).WithUnit(all[4], 10)
	built := NewLoadSupportGraph(all, 1)
	for index := range scene {
		assertEqual(t, built.Supporters(index), grown.Supporters(index))
	}
}

func TestLoadsPropagateWithTheRemainderOnTheLastSupporter(t *testing.T) {
	all := sceneUnits()
	assertEqual(t, []int64{667, 667, 333, 0, 0}, TopLoads(all))
	assertEqual(t, [][]int{{2, 3}, {2, 3}, {3}, nil, nil}, RestingAbove(all))
	assertEqual(t, []int64{2, 2, 1, 0, 0}, StackedCounts(all))
	assertEqual(t, &Violation{"top_load_exceeded", "u0"}, Overloaded(all))
	assertEqual(t, &Violation{"stacked_item_limit_exceeded", "u1"}, StackLimitExceeded(all))
	assertEqual(t, &Violation{"unloading_order_violation", "u0 blocked by u3"},
		RouteOrderViolated(all, []float64{2, math.Inf(1), 1, 3, math.Inf(1)}))
	assertEqual(t, &Violation{"stack_density_exceeded", "u0"}, StackDensityExceeded(all, int64Pointer(1)))
	if Crushed(all) != nil {
		t.Fatal("nothing declares a pressure limit")
	}
}

func TestAxleReactionsAndBalancedOriginsAreExact(t *testing.T) {
	front, rear := units.MustWeight(5000), units.MustWeight(4000)
	axles := [2]domain.Axle{domain.NewAxle(units.MustLength(1000), &front), domain.NewAxle(units.MustLength(9000), &rear)}
	reactions := Reactions(axles, sceneUnits(), 700, 10_000)
	assertBig(t, 16_000, reactions.Denominator)
	assertBig(t, 65_639_434, reactions.FrontNumerator)
	assertBig(t, -1_015_434, reactions.RearNumerator)
	if failure := AxleLoadExceeded(axles, sceneUnits(), 700, 10_000); failure != nil {
		t.Fatalf("unexpected %v", failure)
	}
	assertEqual(t, []int64{6569, 11_793}, AxleBalancedOrigins(axles, sceneUnits(), 700, 10_000, 3001, 77))
}

func TestSupportPolygonCollapsesInteriorAndCollinearPoints(t *testing.T) {
	points := []Point2D{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {5, 5}, {10, 0}, {5, 0}}
	hull := ConvexHull(points)
	assertEqual(t, []Point2D{{0, 0}, {10, 0}, {10, 10}, {0, 10}}, hull)
	assertEqual(t, []Point2D{{0, 0}, {10, 10}}, ConvexHull([]Point2D{{0, 0}, {5, 5}, {10, 10}}))
	if !PointInHull(Point2D{10, 5}, hull) || PointInHull(Point2D{11, 5}, hull) {
		t.Fatal("boundary point must be inside and an outside point outside")
	}
}

func assertEqual(t *testing.T, expected, actual interface{}) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("expected %#v, got %#v", expected, actual)
	}
}

func assertBig(t *testing.T, expected int64, actual *big.Int) {
	t.Helper()
	if actual.Cmp(big.NewInt(expected)) != 0 {
		t.Fatalf("expected %d, got %s", expected, actual)
	}
}
