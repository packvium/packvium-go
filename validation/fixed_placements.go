package validation

import (
	"fmt"
	"sort"

	"github.com/packvium/packvium-go/domain"
)

// fixedKey is what a fixed placement promises: which container instance, which item type,
// where and how it is turned.
type fixedKey struct {
	containerID string
	itemID      string
	position    domain.Point
	rotation    domain.Rotation
}

func (k fixedKey) less(other fixedKey) bool {
	if k.containerID != other.containerID {
		return k.containerID < other.containerID
	}
	if k.itemID != other.itemID {
		return k.itemID < other.itemID
	}
	for axis, value := range [3]int64{k.position.X(), k.position.Y(), k.position.Z()} {
		if theirs := [3]int64{other.position.X(), other.position.Y(), other.position.Z()}[axis]; value != theirs {
			return value < theirs
		}
	}
	return k.rotation < other.rotation
}

func (k fixedKey) detail() string {
	return fmt.Sprintf("%s in %s at (%d, %d, %d) %s", k.itemID, k.containerID, k.position.X(), k.position.Y(),
		k.position.Z(), k.rotation)
}

// fixedPlacementIssues is every fixed placement that is not where the request put it, and
// every placement claiming to be fixed that the request never fixed. Physics needs no rule
// here: a fixed item is an ordinary placement, so every other check already applied to it.
// O(f log f + P).
func fixedPlacementIssues(request *domain.PackingRequest, containers []*domain.PackedContainer) []ValidationIssue {
	requested := map[fixedKey]bool{}
	for _, entry := range request.FixedPlacements() {
		requested[fixedKey{entry.PackedContainerID(), entry.ItemID(), entry.Position(), entry.Rotation()}] = true
	}
	reported := map[fixedKey]bool{}
	present := map[string]bool{}
	for _, packed := range containers {
		present[packed.ID()] = true
		for _, placement := range packed.Placements() {
			if placement.Fixed() {
				reported[fixedKey{packed.ID(), placement.Instance().Item().ID(), placement.Position(),
					placement.Rotation()}] = true
			}
		}
	}
	var issues []ValidationIssue
	for _, key := range sortedFixedKeys(missingFrom(requested, reported)) {
		code := "fixed_container_missing"
		if present[key.containerID] {
			code = "fixed_placement_moved"
		}
		issues = append(issues, ValidationIssue{code, key.detail()})
	}
	for _, key := range sortedFixedKeys(missingFrom(reported, requested)) {
		issues = append(issues, ValidationIssue{"unexpected_fixed_placement", key.detail()})
	}
	return issues
}

func missingFrom(left, right map[fixedKey]bool) []fixedKey {
	var out []fixedKey
	for key := range left {
		if !right[key] {
			out = append(out, key)
		}
	}
	return out
}

func sortedFixedKeys(keys []fixedKey) []fixedKey {
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	return keys
}
