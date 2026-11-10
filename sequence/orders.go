package sequence

import (
	"errors"
	"sort"

	"github.com/packvium/packvium-go/domain"
)

// lowestRemovable is the lowest index among candidates with nothing remaining resting on it
// and some allowed direction clear of what remains: deterministic, never iteration order.
func lowestRemovable(candidates []int, remaining presentSet, graph DependencyGraph, boxes []domain.Box,
	container domain.Dimensions, directions []string) (int, bool) {
	for _, index := range candidates {
		if anyPresent(graph.dependsOn[index], remaining) {
			continue
		}
		if _, clear := clearDirection(index, boxes, remaining, container, directions); clear {
			return index, true
		}
	}
	return 0, false
}

func presentIndexes(present presentSet) []int {
	var indexes []int
	for index, here := range present {
		if here {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// SafeRemovalOrder is a concrete order the boxes could be removed in, starting from the full
// scene, without ever moving a still-present box out of the way first. A *SequenceError when
// none exists, an *InvalidDirectionError for an unknown direction.
//
// A real simulation, not a topological sort: an item with nothing above it always has a
// clear +z path, so with every direction allowed the topmost items are always removable and
// induction completes the order; only a restricted direction set can deadlock. O(n^3 * d):
// n steps, each testing up to n candidates against n others in d directions.
func SafeRemovalOrder(boxes []domain.Box, container domain.Dimensions, directions []string) ([]int, error) {
	if err := domain.ValidateDirections(directions); err != nil {
		return nil, err
	}
	graph := BuildUnloadingGraph(boxes)
	present := allPresent(len(boxes))
	order := make([]int, 0, len(boxes))
	for len(order) < len(boxes) {
		remaining := presentIndexes(present)
		chosen, found := lowestRemovable(remaining, present, graph, boxes, container, directions)
		if !found {
			return nil, newSequenceError(remaining)
		}
		order = append(order, chosen)
		present[chosen] = false
	}
	if err := ReplayRemovalOrder(boxes, container, order, directions); err != nil {
		return nil, err
	}
	return order, nil
}

// SafeRouteRemovalOrder is an unloading order that fully empties each stop, in ascending
// stop order, before the next one starts. stops[i] is box i's stop; nil (or a missing
// trailing entry) means it is not on the route: never scheduled, present throughout, still
// a blocker. With every stop nil nothing is scheduled and the order is empty. A
// *RouteSequenceError names the first stop that cannot be emptied. O(n^3 * d).
func SafeRouteRemovalOrder(boxes []domain.Box, stops []*int64, container domain.Dimensions,
	directions []string) ([]int, error) {
	if err := domain.ValidateDirections(directions); err != nil {
		return nil, err
	}
	graph := BuildUnloadingGraph(boxes)
	remaining := allPresent(len(boxes))
	var order []int
	for _, stop := range distinctStops(stops) {
		var due []int
		for index := range boxes {
			if index < len(stops) && stops[index] != nil && *stops[index] == stop {
				due = append(due, index)
			}
		}
		for len(due) > 0 {
			chosen, found := lowestRemovable(due, remaining, graph, boxes, container, directions)
			if !found {
				return nil, &RouteSequenceError{stop, sortedCopy(due)}
			}
			order = append(order, chosen)
			remaining[chosen] = false
			due = without(due, chosen)
		}
	}
	return order, nil
}

func distinctStops(stops []*int64) []int64 {
	seen := make(map[int64]struct{})
	var distinct []int64
	for _, stop := range stops {
		if stop == nil {
			continue
		}
		if _, found := seen[*stop]; !found {
			seen[*stop] = struct{}{}
			distinct = append(distinct, *stop)
		}
	}
	sort.Slice(distinct, func(a, b int) bool { return distinct[a] < distinct[b] })
	return distinct
}

func without(indexes []int, removed int) []int {
	kept := indexes[:0]
	for _, index := range indexes {
		if index != removed {
			kept = append(kept, index)
		}
	}
	return kept
}

// SafeLoadingOrder is a concrete order the boxes could be loaded in, starting from an empty
// container.
//
// Computed as the exact reverse of a valid unloading order, not a second forward search: a
// proof, not a shortcut. Unloading only shrinks the present set, so its greedy choice is
// trap-free; a naive forward loader is not, since an early greedy choice can block a later
// item's only clear direction. A supporter s of r forces r out before s when unloading, so s
// precedes r in the reversal, which is the loading graph's supporters-first requirement, and
// the sweep is symmetric by construction. ReplayLoadingOrder re-checks this independently.
func SafeLoadingOrder(boxes []domain.Box, container domain.Dimensions, directions []string) ([]int, error) {
	removal, err := SafeRemovalOrder(boxes, container, directions)
	if err != nil {
		return nil, err
	}
	order := make([]int, len(removal))
	for position, index := range removal {
		order[len(removal)-1-position] = index
	}
	if err := ReplayLoadingOrder(boxes, container, order, directions); err != nil {
		return nil, err
	}
	return order, nil
}

// SafeLoadingOrderForPlacements is the domain-level entry point: geometry, accessibility and
// every stacking business rule are replayed before an order is returned.
func SafeLoadingOrderForPlacements(placements []domain.Placement, container *domain.Container,
	directions []string) ([]int, error) {
	boxes := make([]domain.Box, len(placements))
	for index, placement := range placements {
		boxes[index] = placement.EnvelopeBox()
	}
	order, err := SafeLoadingOrder(boxes, container.InnerDimensions(), directions)
	if err != nil {
		return nil, err
	}
	if err := VerifyLoadingPrefixBusinessRules(placements, order, container); err != nil {
		return nil, err
	}
	return order, nil
}

// ReplayRemovalOrder independently replays an unloading order, full scene to empty, and
// returns a *SequenceReplayError at the first step not feasible given only what remains. It
// reuses the geometric primitives of SafeRemovalOrder but none of its search or tie-break
// logic, so it catches a generator bug or a wrong externally supplied order. O(n^2 * d).
func ReplayRemovalOrder(boxes []domain.Box, container domain.Dimensions, order []int, directions []string) error {
	if err := domain.ValidateDirections(directions); err != nil {
		return err
	}
	if err := requirePermutation(order, len(boxes)); err != nil {
		return err
	}
	graph := BuildUnloadingGraph(boxes)
	present := allPresent(len(boxes))
	for step, index := range order {
		if err := validateBoxAtStep(index, step, boxes, present, container); err != nil {
			return err
		}
		if anyPresent(graph.dependsOn[index], present) {
			return replayError(index, step, "something still resting on it has not been removed yet")
		}
		if _, clear := clearDirection(index, boxes, present, container, directions); !clear {
			return replayError(index, step, "no allowed direction is clear of the remaining placements")
		}
		present[index] = false
	}
	return nil
}

// ReplayLoadingOrder independently replays a loading order, empty container to full scene,
// and returns a *SequenceReplayError at the first step not feasible given only what has been
// loaded. O(n^2 * d).
func ReplayLoadingOrder(boxes []domain.Box, container domain.Dimensions, order []int, directions []string) error {
	if err := domain.ValidateDirections(directions); err != nil {
		return err
	}
	if err := requirePermutation(order, len(boxes)); err != nil {
		return err
	}
	graph := BuildLoadingGraph(boxes)
	present := make(presentSet, len(boxes))
	for step, index := range order {
		if err := validateBoxAtStep(index, step, boxes, present, container); err != nil {
			return err
		}
		for _, dependency := range graph.dependsOn[index] {
			if !present[dependency] {
				return replayError(index, step, "a supporter has not been loaded yet")
			}
		}
		if _, clear := clearDirection(index, boxes, present, container, directions); !clear {
			return replayError(index, step, "no allowed direction is clear of what has already been loaded")
		}
		present[index] = true
	}
	return nil
}

// SafeRemovalOrderWithEvidence is SafeRemovalOrder with each step's dependency and the
// direction it used, re-derived by replaying the computed order.
func SafeRemovalOrderWithEvidence(boxes []domain.Box, container domain.Dimensions, directions []string) ([]SequenceStep, error) {
	order, err := SafeRemovalOrder(boxes, container, directions)
	if err != nil {
		return nil, err
	}
	return evidence(boxes, container, directions, order, BuildUnloadingGraph(boxes), allPresent(len(boxes)), false), nil
}

// SafeLoadingOrderWithEvidence is SafeLoadingOrder with each step's dependency and the
// direction it used, re-derived by replaying the computed order.
func SafeLoadingOrderWithEvidence(boxes []domain.Box, container domain.Dimensions, directions []string) ([]SequenceStep, error) {
	order, err := SafeLoadingOrder(boxes, container, directions)
	if err != nil {
		return nil, err
	}
	return evidence(boxes, container, directions, order, BuildLoadingGraph(boxes), make(presentSet, len(boxes)), true), nil
}

func evidence(boxes []domain.Box, container domain.Dimensions, directions []string, order []int,
	graph DependencyGraph, present presentSet, loading bool) []SequenceStep {
	steps := make([]SequenceStep, len(order))
	for position, index := range order {
		direction, clear := clearDirection(index, boxes, present, container, directions)
		if !clear {
			panic(errors.New("a replayed order lost a step it had already proved feasible"))
		}
		steps[position] = SequenceStep{index, direction, graph.dependsOn[index]}
		present[index] = loading
	}
	return steps
}
