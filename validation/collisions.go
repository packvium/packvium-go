package validation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
)

// collisionPairs is the colliding placement index pairs, ascending, from a sweep along x
// bucketed by z-level first.
//
// A valid nested pair is excluded: a deliberate, exactly bounded overlap between two
// identical items, one sunk into the other. Pruning the active set by x-overlap alone leaves
// a multi-layer lattice with an active set close to O(n); bucketing by z-level first bounds
// each sweep by that level's own density instead of the whole container's.
func collisionPairs(placements []domain.Placement) [][2]int {
	if len(placements) == 0 {
		return nil
	}
	cell := int64(1)
	for _, placement := range placements {
		box := placement.EnvelopeBox()
		cell = ints.Max(cell, box.Z2()-box.Origin().Z())
	}
	buckets := make(map[int64][]int)
	var levels []int64
	for index, placement := range placements {
		box := placement.EnvelopeBox()
		low, high := floorDiv(box.Origin().Z(), cell), floorDiv(box.Z2()-1, cell)
		for _, level := range uniqueLevels(low, high) {
			if _, found := buckets[level]; !found {
				levels = append(levels, level)
			}
			buckets[level] = append(buckets[level], index)
		}
	}
	pairs := make(map[[2]int]struct{})
	for _, level := range levels {
		sweep(placements, buckets[level], pairs)
	}
	out := make([][2]int, 0, len(pairs))
	for pair := range pairs {
		out = append(out, pair)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a][0] != out[b][0] {
			return out[a][0] < out[b][0]
		}
		return out[a][1] < out[b][1]
	})
	return out
}

func uniqueLevels(low, high int64) []int64 {
	if low == high {
		return []int64{low}
	}
	return []int64{low, high}
}

type sweepEntry struct {
	x1, x2 int64
	index  int
}

type activeEntry struct {
	x2    int64
	index int
}

func sweep(placements []domain.Placement, indexes []int, pairs map[[2]int]struct{}) {
	ordered := make([]sweepEntry, len(indexes))
	for position, index := range indexes {
		box := placements[index].EnvelopeBox()
		ordered[position] = sweepEntry{box.Origin().X(), box.X2(), index}
	}
	sort.Slice(ordered, func(a, b int) bool {
		left, right := ordered[a], ordered[b]
		if left.x1 != right.x1 {
			return left.x1 < right.x1
		}
		if left.x2 != right.x2 {
			return left.x2 < right.x2
		}
		return left.index < right.index
	})
	var active []activeEntry
	for _, entry := range ordered {
		kept := active[:0]
		for _, other := range active {
			if other.x2 > entry.x1 {
				kept = append(kept, other)
			}
		}
		active = kept
		for _, other := range active {
			left, right := placements[entry.index], placements[other.index]
			if domain.PlacementsCollide(left, right) && !domain.IsValidNesting(left, right) {
				pairs[[2]int{intMin(entry.index, other.index), intMax(entry.index, other.index)}] = struct{}{}
			}
		}
		active = append(active, activeEntry{entry.x2, entry.index})
	}
}

func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func intMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func floorDiv(a, b int64) int64 {
	quotient := a / b
	if a%b != 0 && a < 0 {
		quotient--
	}
	return quotient
}

// splitGroups is the groups packed into more than one container, by group name in code point
// order. O(n log n).
func splitGroups(containers []*domain.PackedContainer) []ValidationIssue {
	located := make(map[string]stringSet)
	for _, packed := range containers {
		for _, placement := range packed.Placements() {
			if group := placement.Instance().Item().Group(); group != nil {
				addMember(located, *group, packed.ID())
			}
		}
	}
	var issues []ValidationIssue
	for _, group := range sortedKeys(located) {
		if where := located[group]; len(where) > 1 {
			issues = append(issues, ValidationIssue{"group_split", group + ": " + strings.Join(sortedMembers(where), ", ")})
		}
	}
	return issues
}

// partialGroups is the groups some but not all of whose members were packed. O(n log n).
func partialGroups(request *domain.PackingRequest, containers []*domain.PackedContainer) []ValidationIssue {
	expected := make(map[string]stringSet)
	placed := make(map[string]stringSet)
	for _, instance := range request.Instances() {
		if group := instance.Item().Group(); group != nil {
			addMember(expected, *group, instance.ID())
		}
	}
	for _, packed := range containers {
		for _, placement := range packed.Placements() {
			if group := placement.Instance().Item().Group(); group != nil {
				addMember(placed, *group, placement.Instance().ID())
			}
		}
	}
	var issues []ValidationIssue
	for _, group := range sortedKeys(expected) {
		members, count := len(expected[group]), len(placed[group])
		if count > 0 && count < members {
			issues = append(issues, ValidationIssue{"group_partial", fmt.Sprintf("%s: %d/%d packed", group, count, members)})
		}
	}
	return issues
}

func addMember(groups map[string]stringSet, group, member string) {
	if groups[group] == nil {
		groups[group] = stringSet{}
	}
	groups[group][member] = struct{}{}
}

func sortedKeys(groups map[string]stringSet) []string {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMembers(members stringSet) []string {
	out := make([]string, 0, len(members))
	for member := range members {
		out = append(out, member)
	}
	sort.Strings(out)
	return out
}
