package constraint

import "fmt"

// FloorConstraint refuses an item that must stand on the floor anywhere above it. O(1).
type FloorConstraint struct{}

// Evaluate implements PlacementConstraint.
func (FloorConstraint) Evaluate(context Context) Result {
	if context.Item().Item().MustBeOnFloor() && context.Point().Z() != 0 {
		return Reject("must_be_on_floor", "")
	}
	return Allow()
}

// ContainerEligibilityConstraint refuses a container the item did not opt into. An item
// with no eligible container tags may go anywhere, the same "empty means unconstrained"
// convention as tags and incompatible tags. O(t).
type ContainerEligibilityConstraint struct{}

// Evaluate implements PlacementConstraint.
func (ContainerEligibilityConstraint) Evaluate(context Context) Result {
	homes := context.Item().Item().EligibleContainerTags()
	if homes.Len() == 0 || homes.Intersects(context.Container().Tags()) {
		return Allow()
	}
	return Reject("container_ineligible", context.Container().ID())
}

// CompatibilityConstraint refuses an item sharing a container with one whose tags either
// of them declares incompatible. O(n * t) for n placements carrying t tags each.
type CompatibilityConstraint struct{}

// Evaluate implements PlacementConstraint.
func (CompatibilityConstraint) Evaluate(context Context) Result {
	item := context.Item().Item()
	if item.Tags().Len() == 0 && item.IncompatibleTags().Len() == 0 {
		return Allow()
	}
	for _, placement := range context.Placements() {
		other := placement.Instance().Item()
		if item.IncompatibleTags().Intersects(other.Tags()) || other.IncompatibleTags().Intersects(item.Tags()) {
			return Reject("incompatible_items", item.ID()+" is incompatible with "+other.ID())
		}
	}
	return Allow()
}

// TagCountConstraint caps how many items carrying a tag may share one container.
//
// A group is atomic but cannot express a count: "at most two of this class per container"
// is a policy about the container, not about any one item's neighbours, so it lives on the
// container's tag limits. Limited tags are checked in sorted order. O(t * n) for t limited
// tags.
type TagCountConstraint struct{}

// Evaluate implements PlacementConstraint.
func (TagCountConstraint) Evaluate(context Context) Result {
	container := context.Container()
	for _, tag := range context.Item().Item().Tags().Sorted() {
		limit, limited := container.TagLimit(tag)
		if !limited {
			continue
		}
		count := int64(0)
		for _, placement := range context.Placements() {
			if placement.Instance().Item().Tags().Has(tag) {
				count++
			}
		}
		if count+1 > limit {
			return Reject("tag_count_exceeded", fmt.Sprintf("%s: limit %d, would be %d", tag, limit, count+1))
		}
	}
	return Allow()
}

// RouteOrderConstraint refuses a placement that would bury an item due off at an earlier
// stop.
//
// A no-op unless some item declares a stop index: route enforcement is opt-in, so a request
// that never mentions a route sees no new rejection code. O(n log n + E) per candidate for
// the graph, O(n^2) worst case for the ancestor sets.
type RouteOrderConstraint struct{}

// Evaluate implements PlacementConstraint.
func (RouteOrderConstraint) Evaluate(context Context) Result {
	if !context.RouteSensitive() {
		return Allow()
	}
	placements := context.Placements()
	units := UnitsWith(placements, bareUnit(context.EnvelopeBox(), context.Item()))
	stops := make([]float64, len(placements)+1)
	for index, placement := range placements {
		stops[index] = StopValue(placement.Instance().Item().StopIndex())
	}
	stops[len(placements)] = StopValue(context.Item().Item().StopIndex())
	return resultOf(RouteOrderViolated(units, stops))
}

// AxleLoadConstraint refuses a placement that would push either axle of a two-axle
// container over its own limit. A no-op unless the container declares axles. O(n) per
// candidate.
type AxleLoadConstraint struct{}

// Evaluate implements PlacementConstraint.
func (AxleLoadConstraint) Evaluate(context Context) Result {
	container := context.Container()
	axles := container.Axles()
	if axles == nil {
		return Allow()
	}
	units := UnitsWith(context.Placements(), bareUnit(context.EnvelopeBox(), context.Item()))
	return resultOf(AxleLoadExceeded(*axles, units, container.TareWeight().Ticks(),
		container.InnerDimensions().Length().Ticks()))
}
