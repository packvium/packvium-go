// Package validation re-derives every guarantee of a packing result from the placements
// alone. It shares no state with the solvers, so a solver that reports a placement it never
// verified is caught here rather than reaching the caller.
//
// For n placements in one container: O(n log n + n * a) for the collision sweep (a the
// per-level active set), O(n * k) for the per-placement checks with k obstacle boxes, and
// O(n^2) only when pair-dependent rules (tags, support) are present, the same bound as the
// constraint checks the solvers run.
package validation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
	"github.com/packvium/packvium-go/sequence"
	"github.com/packvium/packvium-go/units"
)

// ValidationIssue is one broken guarantee: a stable code and the detail naming what broke.
type ValidationIssue struct {
	Code   string
	Detail string
}

// ValidationReport is every issue found, in the order it was found.
type ValidationReport struct{ issues []ValidationIssue }

// Valid reports whether no issue was found.
func (r ValidationReport) Valid() bool { return len(r.issues) == 0 }

// Issues is every issue in the order found.
func (r ValidationReport) Issues() []ValidationIssue {
	return append([]ValidationIssue(nil), r.issues...)
}

// Validate checks the packed containers against the request. clearance is the envelope
// padding the solver used, nil for none. Completeness is not checked: use
// ValidateWithUnpacked when the unpacked items are known.
func Validate(request *domain.PackingRequest, containers []*domain.PackedContainer, minimumSupportRatio float64,
	clearance *units.Length) ValidationReport {
	return validate(request, containers, minimumSupportRatio, clearance, nil, false)
}

// ValidateWithUnpacked is the packer's call: every placement and every unpacked item, so
// that each requested instance is accounted for exactly once, every unpacked item has a
// reason, and no group is only partly packed.
func ValidateWithUnpacked(request *domain.PackingRequest, containers []*domain.PackedContainer,
	minimumSupportRatio float64, clearance *units.Length, unpacked []domain.UnpackedItem) ValidationReport {
	return validate(request, containers, minimumSupportRatio, clearance, unpacked, true)
}

type stringSet map[string]struct{}

func (s stringSet) has(value string) bool {
	_, found := s[value]
	return found
}

func validate(request *domain.PackingRequest, containers []*domain.PackedContainer, minimumSupportRatio float64,
	clearance *units.Length, unpacked []domain.UnpackedItem, complete bool) ValidationReport {
	var issues []ValidationIssue
	seen := stringSet{}
	expected := stringSet{}
	for _, instance := range request.Instances() {
		expected[instance.ID()] = struct{}{}
	}
	clearanceTicks := int64(0)
	if clearance != nil {
		clearanceTicks = clearance.Ticks()
	}
	inventory := make(map[string]int64)
	for _, packed := range containers {
		inventory[packed.Container().ID()]++
		issues = append(issues, containerIssues(packed, minimumSupportRatio, clearanceTicks, seen)...)
	}
	for _, container := range request.Containers() {
		if quantity := container.Quantity(); quantity != nil && inventory[container.ID()] > *quantity {
			issues = append(issues, ValidationIssue{"container_inventory_exceeded", container.ID()})
		}
	}
	if complete {
		for _, item := range unpacked {
			id := item.Instance.ID()
			if seen.has(id) {
				issues = append(issues, ValidationIssue{"duplicate_item", id + " is both packed and unpacked"})
			}
			seen[id] = struct{}{}
			if item.Reason == "" {
				issues = append(issues, ValidationIssue{"missing_reason", id})
			}
		}
		for _, id := range sortedDifference(expected, seen) {
			issues = append(issues, ValidationIssue{"missing_item", id})
		}
	}
	for _, id := range sortedDifference(seen, expected) {
		issues = append(issues, ValidationIssue{"unknown_item", id})
	}
	issues = append(issues, splitGroups(containers)...)
	if complete {
		issues = append(issues, partialGroups(request, containers)...)
	}
	return ValidationReport{issues}
}

// sortedDifference is the members of left missing from right, in code point order.
func sortedDifference(left, right stringSet) []string {
	var out []string
	for value := range left {
		if !right.has(value) {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func containerIssues(packed *domain.PackedContainer, minimumSupportRatio float64, clearanceTicks int64,
	seen stringSet) []ValidationIssue {
	container := packed.Container()
	placements := packed.Placements()
	boundary := domain.NewBox(domain.MustPoint(0, 0, 0), container.InnerDimensions())
	constraints, stackSensitive := pairRules(placements, minimumSupportRatio)
	var stackGraph *constraint.LoadSupportGraph
	if stackSensitive {
		stackGraph = constraint.NewLoadSupportGraph(constraint.Units(placements), 1)
	}
	var issues []ValidationIssue
	for _, pair := range collisionPairs(placements) {
		issues = append(issues, ValidationIssue{"collision",
			placements[pair[0]].Instance().ID() + " with " + placements[pair[1]].Instance().ID()})
	}
	payload := int64(0)
	for index, placement := range placements {
		id := placement.Instance().ID()
		if seen.has(id) {
			issues = append(issues, ValidationIssue{"duplicate_item", id})
		}
		seen[id] = struct{}{}
		payload = ints.AddExact(payload, placement.Instance().Item().Weight().Ticks())
		issues = append(issues, geometryIssues(placement, container, boundary, clearanceTicks)...)
		issues = append(issues, constraintIssues(constraints, container, placements, index)...)
		if stackGraph != nil {
			if failure := stackGraph.NonStackableFailure(placements, placement.Instance(), index); failure != nil {
				issues = append(issues, ValidationIssue{failure.Code, id + ": " + failure.Detail})
			}
		}
	}
	issues = append(issues, loadIssues(packed, placements)...)
	if onRoute(placements) {
		if issue := unloadingOrderViolation(packed, placements); issue != nil {
			issues = append(issues, *issue)
		}
	}
	if limit := container.MaxPayload(); limit != nil && payload > limit.Ticks() {
		issues = append(issues, ValidationIssue{"payload_exceeded", packed.ID()})
	}
	if limit := container.MaxItems(); limit != nil && int64(len(placements)) > *limit {
		issues = append(issues, ValidationIssue{"max_items_exceeded", packed.ID()})
	}
	return issues
}

// pairRules is the placement constraints worth running against every other placement, and
// whether any item makes the stacking check meaningful. The common lattice case has no
// pair-dependent rule, so no "all other placements" list is built for it.
func pairRules(placements []domain.Placement, minimumSupportRatio float64) ([]constraint.PlacementConstraint, bool) {
	compatibility, support, stack := false, minimumSupportRatio > 0, false
	for _, placement := range placements {
		item := placement.Instance().Item()
		compatibility = compatibility || item.Tags().Len() > 0 || item.IncompatibleTags().Len() > 0
		rule := item.GroundContactRule()
		support = support || item.MinimumSupportRatio() > 0 || (rule != nil && *rule != "free")
		stack = stack || item.IsStackSensitive()
	}
	var constraints []constraint.PlacementConstraint
	if compatibility {
		constraints = append(constraints, constraint.CompatibilityConstraint{}, constraint.TagCountConstraint{})
	}
	if support {
		constraints = append(constraints, constraint.NewSupportConstraint(minimumSupportRatio))
	}
	return constraints, stack
}

func constraintIssues(constraints []constraint.PlacementConstraint, container *domain.Container,
	placements []domain.Placement, index int) []ValidationIssue {
	if len(constraints) == 0 {
		return nil
	}
	placement := placements[index]
	others := make([]domain.Placement, 0, len(placements)-1)
	others = append(append(others, placements[:index]...), placements[index+1:]...)
	context := constraint.NewContext(container, others, placement.Instance(), placement.EnvelopeOrigin(),
		placement.Rotation(), placement.Dimensions(), placement.EnvelopeDimensions())
	var issues []ValidationIssue
	for _, rule := range constraints {
		if result := rule.Evaluate(context); !result.Allowed() {
			issues = append(issues, ValidationIssue{result.Code(), placement.Instance().ID() + ": " + result.Detail()})
		}
	}
	return issues
}

// geometryIssues is the checks that need one placement and its container only. O(k) for k
// obstacle boxes.
func geometryIssues(placement domain.Placement, container *domain.Container, boundary domain.Box,
	clearanceTicks int64) []ValidationIssue {
	id := placement.Instance().ID()
	item := placement.Instance().Item()
	var issues []ValidationIssue
	if !boundary.Contains(placement.EnvelopeBox()) {
		issues = append(issues, ValidationIssue{"outside_container", id})
	}
	if !allowsRotation(item, placement.Rotation()) {
		issues = append(issues, ValidationIssue{"forbidden_rotation", id})
	}
	if placement.Dimensions() != item.Dimensions().Rotated(placement.Rotation()) {
		issues = append(issues, ValidationIssue{"dimension_mismatch", id})
	}
	if !envelopeMatches(placement, clearanceTicks) {
		issues = append(issues, ValidationIssue{"clearance_mismatch", id})
	}
	if hitsObstacle(placement, container) {
		issues = append(issues, ValidationIssue{"obstacle_collision", id})
	}
	if item.MustBeOnFloor() && placement.EnvelopeOrigin().Z() != 0 {
		issues = append(issues, ValidationIssue{"must_be_on_floor", id + ": "})
	}
	if homes := item.EligibleContainerTags(); homes.Len() > 0 && !homes.Intersects(container.Tags()) {
		issues = append(issues, ValidationIssue{"container_ineligible", id})
	}
	return issues
}

func allowsRotation(item *domain.Item, rotation domain.Rotation) bool {
	for _, allowed := range item.AllowedRotations() {
		if allowed == rotation {
			return true
		}
	}
	return false
}

// hitsObstacle is one verdict per placement, not one per obstacle box.
func hitsObstacle(placement domain.Placement, container *domain.Container) bool {
	for _, obstacle := range container.Obstacles() {
		for _, box := range obstacle.Boxes() {
			if domain.PlacementHitsBox(placement, box) {
				return true
			}
		}
	}
	return false
}

func envelopeMatches(placement domain.Placement, clearanceTicks int64) bool {
	expected := placement.Dimensions()
	if clearanceTicks != 0 {
		expected = expected.Expand(units.MustLength(clearanceTicks))
	}
	position, origin := placement.Position(), placement.EnvelopeOrigin()
	return placement.EnvelopeDimensions() == expected && position.X() == origin.X()+clearanceTicks &&
		position.Y() == origin.Y()+clearanceTicks && position.Z() == origin.Z()+clearanceTicks
}

// loadIssues is a second, whole-container bearing pass: the per-placement checks report the
// first offender they meet, this one is anchored on the container.
func loadIssues(packed *domain.PackedContainer, placements []domain.Placement) []ValidationIssue {
	container := packed.Container()
	loadUnits := constraint.Units(placements)
	analysis := constraint.NewLoadAnalysis(loadUnits, constraint.NewLoadSupportGraph(loadUnits, 1))
	var issues []ValidationIssue
	if failure := analysis.BearingFailure(constraint.DensityLimitTicks(container)); failure != nil {
		issues = append(issues, ValidationIssue{failure.Code, packed.ID() + ": " + failure.Detail})
	}
	if axles := container.Axles(); axles != nil {
		failure := constraint.AxleLoadExceeded(*axles, loadUnits, container.TareWeight().Ticks(),
			container.InnerDimensions().Length().Ticks())
		if failure != nil {
			issues = append(issues, ValidationIssue{failure.Code, packed.ID() + ": " + failure.Detail})
		}
	}
	return issues
}

func onRoute(placements []domain.Placement) bool {
	for _, placement := range placements {
		if placement.Instance().Item().StopIndex() != nil {
			return true
		}
	}
	return false
}

// unloadingOrderViolation is whether the container's route can be unloaded stop by stop,
// with the same geometry the sequence package proves removal orders against.
//
// Physical, not envelope, boxes: reachability is about what a forklift or a hand would
// actually collide with, and clearance is a placement margin, not an obstruction.
func unloadingOrderViolation(packed *domain.PackedContainer, placements []domain.Placement) *ValidationIssue {
	boxes := make([]domain.Box, len(placements))
	stops := make([]*int64, len(placements))
	for index, placement := range placements {
		boxes[index], stops[index] = placement.Box(), placement.Instance().Item().StopIndex()
	}
	_, err := sequence.SafeRouteRemovalOrder(boxes, stops, packed.Container().InnerDimensions(), sequence.AllDirections())
	if err == nil {
		return nil
	}
	route, ok := err.(*sequence.RouteSequenceError)
	if !ok {
		// Every direction passed is in the vocabulary, so nothing else can be refused.
		panic(err)
	}
	ids := make([]string, 0, len(route.Stuck()))
	for _, index := range route.Stuck() {
		ids = append(ids, placements[index].Instance().ID())
	}
	return &ValidationIssue{"unloading_order_violation", fmt.Sprintf("%s: stop %d cannot be fully unloaded (%s still blocked)",
		packed.ID(), route.Stop(), strings.Join(ids, ", "))}
}
