package sequence

import "github.com/toxakara/packvium-go/domain"

// presentSet is which placements are in the container at one step of a replay. Membership
// alone matters: every reading of it is a boolean or a set that is sorted before it is
// reported, so the walk order never reaches output.
type presentSet []bool

func allPresent(count int) presentSet {
	present := make(presentSet, count)
	for index := range present {
		present[index] = true
	}
	return present
}

// AllDirections is the six walls in the canonical order a clear direction is searched in.
func AllDirections() []string { return append([]string(nil), domain.AllDirections...) }

// SweptVolume is the region between box's own face and the matching container wall along
// direction, refusing an unknown direction with an *InvalidDirectionError.
func SweptVolume(box domain.Box, container domain.Dimensions, direction string) (domain.Sweep, error) {
	if err := domain.ValidateDirections([]string{direction}); err != nil {
		return domain.Sweep{}, err
	}
	return domain.SweptVolume(box, container, direction)
}

// sweep is SweptVolume for an already validated direction.
func sweep(box domain.Box, container domain.Dimensions, direction string) domain.Sweep {
	region, err := domain.SweptVolume(box, container, direction)
	if err != nil {
		panic(err)
	}
	return region
}

// blockingIndices is every other present box intersecting box's sweep toward direction,
// ascending. O(n).
func blockingIndices(index int, boxes []domain.Box, present presentSet, container domain.Dimensions,
	direction string) []int {
	region := sweep(boxes[index], container, direction)
	var blocking []int
	for other, here := range present {
		if here && other != index && region.Intersects(boxes[other]) {
			blocking = append(blocking, other)
		}
	}
	return blocking
}

// blocked is the predicate of blockingIndices, stopping at the first blocker: the safe-order
// search asks it O(n * d) times per step and never reads the set. O(n).
func blocked(index int, boxes []domain.Box, present presentSet, container domain.Dimensions, direction string) bool {
	region := sweep(boxes[index], container, direction)
	for other, here := range present {
		if here && other != index && region.Intersects(boxes[other]) {
			return true
		}
	}
	return false
}

// clearDirection is the first allowed direction, in the order given, whose sweep is clear.
// O(n * d).
func clearDirection(index int, boxes []domain.Box, present presentSet, container domain.Dimensions,
	directions []string) (string, bool) {
	for _, direction := range directions {
		if !blocked(index, boxes, present, container, direction) {
			return direction, true
		}
	}
	return "", false
}

// validateBoxAtStep enforces the two final-scene invariants a sweep cannot prove:
// containment and no overlap with what is present. O(n).
func validateBoxAtStep(index, step int, boxes []domain.Box, present presentSet, container domain.Dimensions) error {
	box := boxes[index]
	origin := box.Origin()
	if origin.X() < 0 || origin.Y() < 0 || origin.Z() < 0 || box.X2() > container.Length().Ticks() ||
		box.Y2() > container.Width().Ticks() || box.Z2() > container.Height().Ticks() {
		return replayError(index, step, "placement is outside the container")
	}
	for other, here := range present {
		if here && other != index && box.Intersects(boxes[other]) {
			return replayError(index, step, "placement collides with an already present placement")
		}
	}
	return nil
}

// requirePermutation refuses an order that is not every index in 0..count-1 exactly once.
func requirePermutation(order []int, count int) error {
	sorted := sortedCopy(order)
	valid := len(sorted) == count
	for position := 0; valid && position < count; position++ {
		valid = sorted[position] == position
	}
	if !valid {
		return replayError(-1, -1, "order is not a permutation of every placement index exactly once")
	}
	return nil
}

func anyPresent(indexes []int, present presentSet) bool {
	for _, index := range indexes {
		if present[index] {
			return true
		}
	}
	return false
}
