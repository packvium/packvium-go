package sequence

import (
	"reflect"
	"testing"

	"github.com/packvium/packvium-go/domain"
)

// Expected values come from the v1.0.0 reference engine.

func box(x, y, z, l, w, h int64) domain.Box {
	return domain.NewBox(domain.MustPoint(x, y, z), domain.MustDimensions(l, w, h))
}

var (
	scene = []domain.Box{box(0, 0, 0, 10, 10, 10), box(10, 0, 0, 10, 10, 10), box(5, 0, 10, 10, 10, 10),
		box(0, 0, 20, 7, 10, 5), box(20, 0, 0, 3, 3, 3)}
	container = domain.MustDimensions(30, 10, 30)
	all       = AllDirections()
)

func stops(values ...int64) []*int64 {
	out := make([]*int64, len(values))
	for index := range values {
		if values[index] >= 0 {
			out[index] = &values[index]
		}
	}
	return out
}

func must(t *testing.T, order []int, err error) []int {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func equal(t *testing.T, expected, actual interface{}) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("expected %#v, got %#v", expected, actual)
	}
}

func TestOrdersMatchTheReference(t *testing.T) {
	order, err := SafeRemovalOrder(scene, container, all)
	equal(t, []int{3, 2, 0, 1, 4}, must(t, order, err))
	order, err = SafeLoadingOrder(scene, container, all)
	equal(t, []int{4, 1, 0, 2, 3}, must(t, order, err))
	order, err = SafeRemovalOrder(scene, container, []string{"-x"})
	equal(t, []int{3, 2, 0, 1, 4}, must(t, order, err))
	order, err = SafeRouteRemovalOrder(scene, stops(3, 4, 2, 1, 0), container, []string{"+z", "+x"})
	equal(t, []int{4, 3, 2, 0, 1}, must(t, order, err))
}

func TestEvidenceNamesDependenciesAndDirections(t *testing.T) {
	removal, err := SafeRemovalOrderWithEvidence(scene, container, all)
	if err != nil {
		t.Fatal(err)
	}
	loading, err := SafeLoadingOrderWithEvidence(scene, container, all)
	if err != nil {
		t.Fatal(err)
	}
	expectedRemoval := []SequenceStep{{3, "+x", nil}, {2, "+x", []int{3}}, {0, "-x", []int{2}}, {1, "-x", []int{2}}, {4, "+x", nil}}
	expectedLoading := []SequenceStep{{4, "+x", nil}, {1, "-x", nil}, {0, "-x", nil}, {2, "+x", []int{0, 1}}, {3, "+x", []int{2}}}
	for position := range expectedRemoval {
		equal(t, expectedRemoval[position].ToMap(), removal[position].ToMap())
		equal(t, expectedLoading[position].ToMap(), loading[position].ToMap())
	}
}

func TestAStuckStopNamesEveryPlacementLeft(t *testing.T) {
	_, err := SafeRouteRemovalOrder(scene, stops(2, 1, 1, -1, 0), container, []string{"+z"})
	route, ok := err.(*RouteSequenceError)
	if !ok {
		t.Fatalf("expected a route error, got %v", err)
	}
	equal(t, "stop 1: placements [1, 2] cannot be unloaded there", route.Error())
	equal(t, []int{1, 2}, route.Stuck())
	_, err = SafeRemovalOrder(scene, container, []string{"up"})
	if _, ok := err.(*InvalidDirectionError); !ok {
		t.Fatalf("expected a direction error, got %v", err)
	}
	equal(t, "unknown movement direction 'up'; expected one of ('+x', '-x', '+y', '-y', '+z', '-z')", err.Error())
}

func TestReplayRefusesTheFirstInfeasibleStep(t *testing.T) {
	err := ReplayRemovalOrder(scene, container, []int{0, 1, 2, 3, 4}, all)
	equal(t, "step 0: placement 0 is not safe there (something still resting on it has not been removed yet)", err.Error())
	err = ReplayLoadingOrder(scene, container, []int{2, 1, 0, 3, 4}, all)
	equal(t, "step 0: placement 2 is not safe there (a supporter has not been loaded yet)", err.Error())
	err = ReplayLoadingOrder(scene, container, []int{0, 0, 1, 2, 3}, all)
	equal(t, map[string]interface{}{"code": "sequence_replay", "index": int64(-1), "step": int64(-1),
		"reason": "order is not a permutation of every placement index exactly once"}, err.(*SequenceReplayError).ToMap())
}

func TestReachabilityCarriesSortedEvidence(t *testing.T) {
	reach, err := PlacementReachability(scene, container, stops(1, 2, 1, -1, 3), []string{"+x", "+z"})
	if err != nil {
		t.Fatal(err)
	}
	none := []interface{}{}
	equal(t, map[string]interface{}{"index": int64(0), "reachable": false, "blocked_by_support": []interface{}{int64(2)},
		"blocked_by_neighbors": []interface{}{int64(1), int64(2), int64(3), int64(4)}, "blocked_by_route": none}, reach[0].ToMap())
	equal(t, map[string]interface{}{"index": int64(1), "reachable": false, "blocked_by_support": []interface{}{int64(2)},
		"blocked_by_neighbors": []interface{}{int64(2), int64(4)}, "blocked_by_route": []interface{}{int64(0), int64(2)}}, reach[1].ToMap())
	equal(t, map[string]interface{}{"index": int64(2), "reachable": false, "blocked_by_support": []interface{}{int64(3)},
		"blocked_by_neighbors": none, "blocked_by_route": none}, reach[2].ToMap())
	equal(t, true, reach[3].Reachable())
	equal(t, []int{0, 1, 2}, reach[4].BlockedByRoute())
	if _, err := PlacementReachability(scene, container, stops(1), all); err == nil ||
		err.Error() != "stops must contain exactly one entry per placement" {
		t.Fatalf("expected a length refusal, got %v", err)
	}
}

func TestASyntheticCycleIsDetected(t *testing.T) {
	equal(t, false, NewDependencyGraph([][]int{{1}, {0}}).IsAcyclic())
	equal(t, true, BuildUnloadingGraph(scene).IsAcyclic())
	equal(t, true, BuildLoadingGraph(scene).IsAcyclic())
}

func TestWarningArgumentsRenderInKeyOrder(t *testing.T) {
	warning := NewSequenceWarning("w", 1, "k", map[string]string{"b": "1", "10": "x", "9": "y"})
	equal(t, []string{"10", "9", "b"}, warning.ArgumentKeys())
	equal(t, map[string]interface{}{"code": "w", "index": int64(1), "message_key": "k",
		"arguments": map[string]interface{}{"10": "x", "9": "y", "b": "1"}}, warning.ToMap())
}

func TestNoDirectionLeavesEveryPlacementStuck(t *testing.T) {
	_, err := SafeRemovalOrder(scene, container, nil)
	stuck, ok := err.(*SequenceError)
	if !ok {
		t.Fatalf("expected a sequence error, got %v", err)
	}
	equal(t, "no safe order exists: placements [0, 1, 2, 3, 4] are mutually blocking", stuck.Error())
	equal(t, map[string]interface{}{"code": "sequence_stuck",
		"stuck": []interface{}{int64(0), int64(1), int64(2), int64(3), int64(4)}}, stuck.ToMap())
}
