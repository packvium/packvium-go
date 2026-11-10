package constraint

import "math"

// The functions below are whole-scene load checks that each build their own support graph,
// O(n log n + E) per call for the loads and O(n^2) worst case for the ancestor sets. Code
// that already holds a LoadSupportGraph uses LoadAnalysis instead.

// TopLoads is LoadAnalysis.TopLoads over a fresh graph.
func TopLoads(units []LoadUnit) []int64 { return analysisOf(units).TopLoads() }

// Overloaded is LoadAnalysis.Overloaded over a fresh graph.
func Overloaded(units []LoadUnit) *Violation { return analysisOf(units).Overloaded() }

// Crushed is LoadAnalysis.Crushed over a fresh graph.
func Crushed(units []LoadUnit) *Violation { return analysisOf(units).Crushed() }

// RestingAbove is LoadAnalysis.RestingAbove over a fresh graph.
func RestingAbove(units []LoadUnit) [][]int { return analysisOf(units).RestingAbove() }

// StackedCounts is LoadAnalysis.StackedCounts over a fresh graph.
func StackedCounts(units []LoadUnit) []int64 { return analysisOf(units).StackedCounts() }

// StackLimitExceeded is LoadAnalysis.StackLimitExceeded over a fresh graph.
func StackLimitExceeded(units []LoadUnit) *Violation { return analysisOf(units).StackLimitExceeded() }

// StackDensityExceeded is LoadAnalysis.StackDensityExceeded over a fresh graph.
func StackDensityExceeded(units []LoadUnit, maxDensityTicks *int64) *Violation {
	return analysisOf(units).StackDensityExceeded(maxDensityTicks)
}

// StopValue is a stop index as RouteOrderViolated orders it. An item with no stop index rides
// the whole route: never removed, so it blocks every stop, which is what +Inf gives when the
// blocker test is "later than". Exact: stop indices are bounded to 2^53 - 1.
func StopValue(stopIndex *int64) float64 {
	if stopIndex == nil {
		return math.Inf(1)
	}
	return float64(*stopIndex)
}

// RouteOrderViolated is the first item due at an earlier stop with a later-stop item resting
// anywhere above it. stops[i] is unit i's StopValue.
//
// Vertical blocking is the half of route order a placement decision can see by itself.
// Whether an unblocked box can also slide out through some wall is a whole-container
// question the post-validator answers; this is the necessary condition, not the whole of it.
func RouteOrderViolated(units []LoadUnit, stops []float64) *Violation {
	onRoute := false
	for _, stop := range stops {
		if !math.IsInf(stop, 1) {
			onRoute = true
			break
		}
	}
	if !onRoute {
		return nil
	}
	for index, above := range RestingAbove(units) {
		for _, upper := range above {
			if stops[upper] > stops[index] {
				return &Violation{"unloading_order_violation", units[index].Label + " blocked by " + units[upper].Label}
			}
		}
	}
	return nil
}
