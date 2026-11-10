package constraint

import (
	"fmt"
	"math"

	"github.com/packvium/packvium-go/domain"
)

// noStop is the stop of an item with no stop index: it rides the whole route and is never
// removed, so it blocks every stop. Real stop indices are bounded to 2^53 - 1.
const noStop = math.MaxInt64

// StopAccessibilityConstraint refuses a placement that walls an earlier-stop item away from
// every door.
//
// RouteOrderConstraint enforces the vertical half of route order; this is the horizontal
// half: nothing due later may stand between an earlier item and the way out. Both are
// necessary and neither implies the other; the post-validator's whole-scene replay remains
// the sufficient check.
//
// Opt-in twice over: inert unless the caller supplies exit directions (assuming all six walls
// open would enforce a rule true of no real vehicle), and inert unless two distinct stops are
// in play. The blocker set is strictly later stops: the order within a stop is free, so
// "later or equal" would refuse two same-stop pallets standing one behind the other.
//
// Per placement list the door table is O(n^2 * d); each candidate then costs O(n * d).
// Mutable per-solve state, so one instance must not be shared between concurrent solves.
type StopAccessibilityConstraint struct {
	directions []string
	placements []domain.Placement
	container  domain.Dimensions
	primed     bool
	clear      [][]string
	stops      []int64
}

// NewStopAccessibilityConstraint opens the given doors, refusing a direction outside the
// vocabulary with a *domain.InvalidDirectionError. The doors are kept in the canonical order
// rather than as given, so two callers naming the same doors differently search identically.
func NewStopAccessibilityConstraint(directions []string) (*StopAccessibilityConstraint, error) {
	if err := domain.ValidateDirections(directions); err != nil {
		return nil, err
	}
	var canonical []string
	for _, direction := range domain.AllDirections {
		for _, requested := range directions {
			if requested == direction {
				canonical = append(canonical, direction)
				break
			}
		}
	}
	return &StopAccessibilityConstraint{directions: canonical}, nil
}

func stopOf(placement domain.Placement) int64 {
	return stopIndexOrNone(placement.Instance().Item().StopIndex())
}

func stopIndexOrNone(stopIndex *int64) int64 {
	if stopIndex == nil {
		return noStop
	}
	return *stopIndex
}

func sweep(box domain.Box, inner domain.Dimensions, direction string) domain.Sweep {
	region, err := domain.SweptVolume(box, inner, direction)
	if err != nil {
		// Every direction reaching here was validated when the constraint was built.
		panic(err)
	}
	return region
}

// baseFor computes, per placed box, the doors still open to it against the other placed
// boxes. Keyed on the container too, because a corridor runs to a wall: the same boxes have
// different exits in a longer container.
func (s *StopAccessibilityConstraint) baseFor(current []domain.Placement, inner domain.Dimensions) {
	if s.primed && s.container == inner && samePlacements(s.placements, current) {
		return
	}
	stops := make([]int64, len(current))
	boxes := make([]domain.Box, len(current))
	for index, placement := range current {
		stops[index], boxes[index] = stopOf(placement), placement.EnvelopeBox()
	}
	clear := make([][]string, len(boxes))
	for index := range boxes {
		if stops[index] == noStop {
			// Never unloaded, so it needs no door of its own: it only ever blocks.
			clear[index] = s.directions
			continue
		}
		var open []string
		for _, direction := range s.directions {
			if !blockedByLater(sweep(boxes[index], inner, direction), boxes, stops, stops[index], index) {
				open = append(open, direction)
			}
		}
		clear[index] = open
	}
	s.placements, s.container, s.primed = append([]domain.Placement(nil), current...), inner, true
	s.clear, s.stops = clear, stops
}

// blockedByLater reports whether any box other than skip, due strictly after stop, stands in
// region. O(n).
func blockedByLater(region domain.Sweep, boxes []domain.Box, stops []int64, stop int64, skip int) bool {
	for other, box := range boxes {
		if other != skip && stops[other] > stop && region.Intersects(box) {
			return true
		}
	}
	return false
}

// Evaluate implements PlacementConstraint.
func (s *StopAccessibilityConstraint) Evaluate(context Context) Result {
	if len(s.directions) == 0 || !context.RouteSensitive() {
		return Allow()
	}
	candidateStop := stopIndexOrNone(context.Item().Item().StopIndex())
	inner := context.Container().InnerDimensions()
	s.baseFor(context.Placements(), inner)
	// One distinct stop means nothing is due before anything else, so no corridor can be
	// blocked by a later item. The candidate counts too, or the first placement into an
	// empty container would skip a check it should make.
	if !distinctStops(s.stops, candidateStop) {
		return Allow()
	}
	candidate := context.EnvelopeBox()
	placed := context.Placements()
	for index, placement := range placed {
		if candidateStop <= s.stops[index] || len(s.clear[index]) == 0 {
			continue
		}
		if !anyDoorOpen(placement.EnvelopeBox(), inner, s.clear[index], candidate) {
			return Reject("stop_accessibility_violation", fmt.Sprintf("%s due at stop %d loses its last exit to %s",
				placement.Instance().ID(), s.stops[index], context.Item().ID()))
		}
	}
	if candidateStop == noStop {
		return Allow()
	}
	boxes := make([]domain.Box, len(placed))
	for index, placement := range placed {
		boxes[index] = placement.EnvelopeBox()
	}
	for _, direction := range s.directions {
		if !blockedByLater(sweep(candidate, inner, direction), boxes, s.stops, candidateStop, -1) {
			return Allow()
		}
	}
	return Reject("stop_accessibility_violation",
		fmt.Sprintf("%s due at stop %d would have no exit", context.Item().ID(), candidateStop))
}

func distinctStops(stops []int64, candidate int64) bool {
	for _, stop := range stops {
		if stop != candidate {
			return true
		}
	}
	return false
}

// anyDoorOpen reports whether box keeps at least one of doors clear of candidate. O(d).
func anyDoorOpen(box domain.Box, inner domain.Dimensions, doors []string, candidate domain.Box) bool {
	for _, direction := range doors {
		if !sweep(box, inner, direction).Intersects(candidate) {
			return true
		}
	}
	return false
}
