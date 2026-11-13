package constraint

import "github.com/packvium/packvium-go/domain"

// Active is the constraints that could reject some candidate of item in container.
//
// A candidate search evaluates the whole chain once per feasible position, and most rules are
// opt-in: they begin by reading a flag on the item, the container or the search state and
// allow when it is off. Those flags are fixed for one search, so the answer is read once here
// instead of once per position. A dropped rule would have allowed every position, so the
// first rejecting rule, and everything counted on the way to it, is unchanged. Only the exact
// built-in types are recognised: a caller's own constraint is always evaluated. O(k + t).
func Active(constraints []PlacementConstraint, container *domain.Container, item domain.ItemInstance,
	stackSensitive, routeSensitive bool) []PlacementConstraint {
	active := make([]PlacementConstraint, 0, len(constraints))
	for _, rule := range constraints {
		if !inert(rule, container, item.Item(), stackSensitive, routeSensitive) {
			active = append(active, rule)
		}
	}
	return active
}

func inert(rule PlacementConstraint, container *domain.Container, item *domain.Item,
	stackSensitive, routeSensitive bool) bool {
	switch typed := rule.(type) {
	case FloorConstraint:
		return !item.MustBeOnFloor()
	case ContainerEligibilityConstraint:
		homes := item.EligibleContainerTags()
		return homes.Len() == 0 || homes.Intersects(container.Tags())
	case CompatibilityConstraint:
		return item.Tags().Len() == 0 && item.IncompatibleTags().Len() == 0
	case TagCountConstraint:
		return !limitsAnyTag(container, item.Tags())
	case *TopLoadConstraint:
		return !stackSensitive
	case RouteOrderConstraint:
		return !routeSensitive
	case *StopAccessibilityConstraint:
		return typed.inert(container, routeSensitive)
	case AxleLoadConstraint:
		return container.Axles() == nil
	}
	return false
}

func limitsAnyTag(container *domain.Container, tags domain.TagSet) bool {
	for _, tag := range tags.Sorted() {
		if _, limited := container.TagLimit(tag); limited {
			return true
		}
	}
	return false
}
