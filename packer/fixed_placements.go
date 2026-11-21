package packer

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/canonicaljson"
	"github.com/packvium/packvium-go/units"
	"github.com/packvium/packvium-go/validation"
)

// A fixed placement is an item already in a known place: loaded, or locked there by an
// operator. It enters the solve as a real placement, weight, support and top load included,
// seeded into the one container instance it names, so every rule the engine already enforces
// holds for it with no rule of its own.
//
// What this file adds is the refusal. A fixed set that is not a valid packing on its own is
// refused before any search rather than exempted: an exempt item would need a second
// validator, and the engines would have to agree on which rules it skips. The check is the
// ordinary independent validator run over the fixed set alone, with item accounting left out
// because the free items have not been placed yet (docs/PLAN-REVISIONS.md).

// fixedLoad is what the solve starts from: the fixed containers in opening order (request
// container order, then instance), each holding only its fixed placements, and every
// instance a fixed placement did not take, in request order.
type fixedLoad struct {
	containers []*domain.PackedContainer
	free       []domain.ItemInstance
}

func cannotHold(detail string) error {
	return domain.NewFixedPlacementError("cannot_hold", "/fixed_placements", detail)
}

// admitFixedPlacements resolves the request's fixed placements, or refuses the request.
// O(f log f) to resolve f entries, plus one validator pass over them, which is O(f^2) in the
// worst case of its pairwise checks. It runs once, before search.
func admitFixedPlacements(request *domain.PackingRequest, minimumSupportRatio float64, clearance units.Length,
	maxContainers *int64) (fixedLoad, error) {
	entries := request.FixedPlacements()
	if len(entries) == 0 {
		return fixedLoad{free: request.Instances()}, nil
	}
	items := map[string]*domain.Item{}
	for _, item := range request.Items() {
		items[item.ID()] = item
	}
	containers := map[string]*domain.Container{}
	order := map[string]int{}
	for index, container := range request.Containers() {
		containers[container.ID()] = container
		order[container.ID()] = index
	}
	for _, entry := range entries {
		if err := requireKnown(entry, items, containers); err != nil {
			return fixedLoad{}, err
		}
	}
	instances, err := assignInstances(entries, items)
	if err != nil {
		return fixedLoad{}, err
	}
	grouped, err := groupByContainer(entries, instances, clearance, order)
	if err != nil {
		return fixedLoad{}, err
	}
	if err := requireContiguousInstances(grouped, containers, maxContainers); err != nil {
		return fixedLoad{}, err
	}
	packed := make([]*domain.PackedContainer, len(grouped))
	taken := map[string]bool{}
	for index, group := range grouped {
		packed[index] = domain.NewPackedContainer(containers[group.containerID], group.sequence,
			withSupportAndLoads(group.placements))
		for _, placement := range group.placements {
			taken[placement.Instance().ID()] = true
		}
	}
	if err := requireValidAlone(request, packed, minimumSupportRatio, clearance); err != nil {
		return fixedLoad{}, err
	}
	var free []domain.ItemInstance
	for _, instance := range request.Instances() {
		if !taken[instance.ID()] {
			free = append(free, instance)
		}
	}
	return fixedLoad{packed, free}, nil
}

func requireKnown(entry domain.FixedPlacement, items map[string]*domain.Item,
	containers map[string]*domain.Container) error {
	item, ok := items[entry.ItemID()]
	if !ok {
		return cannotHold("unknown item type " + canonicaljson.Spelling(entry.ItemID()))
	}
	if _, ok := containers[entry.ContainerID()]; !ok {
		return cannotHold("unknown container type " + canonicaljson.Spelling(entry.ContainerID()))
	}
	for _, allowed := range item.AllowedRotations() {
		if allowed == entry.Rotation() {
			return nil
		}
	}
	return cannotHold(fmt.Sprintf("%s may not be placed in orientation %s", entry.ItemID(), entry.Rotation()))
}

// assignInstances gives fixed items the first instances of their type, in the order listed.
func assignInstances(entries []domain.FixedPlacement, items map[string]*domain.Item) ([]domain.ItemInstance, error) {
	taken := map[string]int{}
	byType := map[string][]domain.ItemInstance{}
	instances := make([]domain.ItemInstance, len(entries))
	for index, entry := range entries {
		item := items[entry.ItemID()]
		taken[item.ID()]++
		if int64(taken[item.ID()]) > item.Quantity() {
			return nil, cannotHold(fmt.Sprintf("%d %s fixed, %d requested", taken[item.ID()], item.ID(), item.Quantity()))
		}
		if byType[item.ID()] == nil {
			byType[item.ID()] = item.Instances()
		}
		instances[index] = byType[item.ID()][taken[item.ID()]-1]
	}
	return instances, nil
}

type fixedGroup struct {
	containerID string
	sequence    int64
	placements  []domain.Placement
}

// groupByContainer is the placements per named container instance, in request container
// order, then instance.
func groupByContainer(entries []domain.FixedPlacement, instances []domain.ItemInstance, clearance units.Length,
	order map[string]int) ([]fixedGroup, error) {
	type instanceKey struct {
		containerID string
		sequence    int64
	}
	position := map[instanceKey]int{}
	var grouped []fixedGroup
	for index, entry := range entries {
		placement, err := fixedPlacement(entry, instances[index], clearance)
		if err != nil {
			return nil, err
		}
		key := instanceKey{entry.ContainerID(), entry.ContainerInstance()}
		at, seen := position[key]
		if !seen {
			at = len(grouped)
			position[key] = at
			grouped = append(grouped, fixedGroup{entry.ContainerID(), entry.ContainerInstance(), nil})
		}
		grouped[at].placements = append(grouped[at].placements, placement)
	}
	sort.SliceStable(grouped, func(i, j int) bool {
		left, right := grouped[i], grouped[j]
		if order[left.containerID] != order[right.containerID] {
			return order[left.containerID] < order[right.containerID]
		}
		return left.sequence < right.sequence
	})
	return grouped, nil
}

// fixedPlacement places one entry. The clearance envelope must be inside the container, as it
// must for any placement; an item flush against a wall has an envelope that starts before it.
func fixedPlacement(entry domain.FixedPlacement, instance domain.ItemInstance, clearance units.Length) (domain.Placement, error) {
	margin := clearance.Ticks()
	position := entry.Position()
	if position.X() < margin || position.Y() < margin || position.Z() < margin {
		return domain.Placement{}, cannotHold("outside_container: " + instance.ID())
	}
	dimensions := instance.Item().Dimensions().Rotated(entry.Rotation())
	// A far corner past the tick range is outside every container. Refused here, as the
	// validator would refuse it, because adding the extent would wrap.
	if beyondTickRange(position, dimensions, margin) {
		return domain.Placement{}, cannotHold("outside_container: " + instance.ID())
	}
	envelopeOrigin := domain.MustPoint(position.X()-margin, position.Y()-margin, position.Z()-margin)
	envelope := dimensions
	if margin != 0 {
		envelope = dimensions.Expand(clearance)
	}
	return domain.NewPlacement(instance, position, entry.Rotation(), dimensions, envelopeOrigin, envelope, 1).AsFixed(), nil
}

func beyondTickRange(position domain.Point, dimensions domain.Dimensions, margin int64) bool {
	extents := [3]int64{dimensions.Length().Ticks(), dimensions.Width().Ticks(), dimensions.Height().Ticks()}
	for axis, origin := range [3]int64{position.X(), position.Y(), position.Z()} {
		if extents[axis] > math.MaxInt64-origin-margin {
			return true
		}
	}
	return false
}

func requireContiguousInstances(grouped []fixedGroup, containers map[string]*domain.Container,
	maxContainers *int64) error {
	var names []string
	named := map[string][]int64{}
	for _, group := range grouped {
		if _, seen := named[group.containerID]; !seen {
			names = append(names, group.containerID)
		}
		named[group.containerID] = append(named[group.containerID], group.sequence)
	}
	for _, id := range names {
		sequences := named[id]
		if !numberedFromOne(sequences) {
			return cannotHold(fmt.Sprintf("%s instances %s are not numbered 1..%d", id, spellIntegers(sequences),
				len(sequences)))
		}
		if quantity := containers[id].Quantity(); quantity != nil && int64(len(sequences)) > *quantity {
			return cannotHold(fmt.Sprintf("%d %s named, %d available", len(sequences), id, *quantity))
		}
	}
	if maxContainers != nil && int64(len(grouped)) > *maxContainers {
		return cannotHold(fmt.Sprintf("%d containers hold fixed items, max_containers is %d", len(grouped),
			*maxContainers))
	}
	return nil
}

func numberedFromOne(sequences []int64) bool {
	for index, sequence := range sequences {
		if sequence != int64(index)+1 {
			return false
		}
	}
	return true
}

// spellIntegers is a list of instance numbers as canonical JSON; each was admitted within
// 2^53 - 1, so every one has a spelling.
func spellIntegers(values []int64) string {
	text := "["
	for index, value := range values {
		if index > 0 {
			text += ","
		}
		text += strconv.FormatInt(value, 10)
	}
	return text + "]"
}

// withSupportAndLoads is each fixed item's support ratio from the fixed items under it, and
// its top load. Support is measured against every other fixed item in the container, not
// only the ones listed before it: the set is one arrangement, and listing order is not
// physics. O(p^2) for p placements.
func withSupportAndLoads(placements []domain.Placement) []domain.Placement {
	supported := make([]domain.Placement, len(placements))
	for index, placement := range placements {
		others := make([]domain.Placement, 0, len(placements)-1)
		others = append(append(others, placements[:index]...), placements[index+1:]...)
		supported[index] = placement.WithSupportRatio(supportRatio(others, placement))
	}
	loads := constraint.TopLoads(constraint.Units(supported))
	for index := range supported {
		supported[index] = supported[index].WithTopLoad(units.MustWeight(loads[index]))
	}
	return supported
}

func supportRatio(others []domain.Placement, placement domain.Placement) float64 {
	if placement.EnvelopeOrigin().Z() == 0 {
		return 1.0
	}
	support := constraint.CandidateView(others, placement.Instance(), placement.EnvelopeBox())
	return float64(support.SupportingArea()) / float64(placement.EnvelopeDimensions().BaseArea())
}

func requireValidAlone(request *domain.PackingRequest, packed []*domain.PackedContainer, minimumSupportRatio float64,
	clearance units.Length) error {
	report := validation.Validate(request, packed, minimumSupportRatio, &clearance)
	if report.Valid() {
		return nil
	}
	issue := report.Issues()[0]
	return cannotHold(issue.Code + ": " + issue.Detail)
}
