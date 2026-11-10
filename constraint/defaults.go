package constraint

// Defaults is the constraint pipeline every solver runs, in the reference order, followed by
// the caller's own constraints. minimumSupportRatio is the request-wide support floor and
// accessDirections the doors the stop accessibility rule may use; an unknown direction is
// refused with a *domain.InvalidDirectionError. The stateful constraints in the list are
// fresh, so the list belongs to one solve.
func Defaults(minimumSupportRatio float64, accessDirections []string, custom ...PlacementConstraint) ([]PlacementConstraint, error) {
	access, err := NewStopAccessibilityConstraint(accessDirections)
	if err != nil {
		return nil, err
	}
	constraints := []PlacementConstraint{
		FloorConstraint{}, ContainerEligibilityConstraint{}, CompatibilityConstraint{}, TagCountConstraint{},
		NewSupportConstraint(minimumSupportRatio), NewTopLoadConstraint(), RouteOrderConstraint{}, access,
		AxleLoadConstraint{},
	}
	return append(constraints, custom...), nil
}
