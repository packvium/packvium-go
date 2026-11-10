package sequence

import (
	"errors"
	"sort"

	"github.com/toxakara/packvium-go/domain"
)

// PlacementReachability is per-box reachability for the full scene as it stands, one entry
// per box.
//
// It reuses the unloading graph and the same sweep SafeRemovalOrder is built from, so there
// is no second notion of "blocked". stops is optional: nil leaves every route set empty;
// otherwise it needs exactly one entry per box, and a box due
// later than the earliest outstanding stop is blocked by every present box due earlier.
// O(n^2 * d) for n boxes and d directions.
func PlacementReachability(boxes []domain.Box, container domain.Dimensions, stops []*int64,
	directions []string) ([]Reachability, error) {
	if err := domain.ValidateDirections(directions); err != nil {
		return nil, err
	}
	graph := BuildUnloadingGraph(boxes)
	present := allPresent(len(boxes))
	if stops == nil {
		stops = make([]*int64, len(boxes))
	} else if len(stops) != len(boxes) {
		return nil, errors.New("stops must contain exactly one entry per placement")
	}
	earliest := earliestStop(stops)
	results := make([]Reachability, len(boxes))
	for index := range boxes {
		var bySupport []int
		for _, dependency := range graph.dependsOn[index] {
			if present[dependency] {
				bySupport = append(bySupport, dependency)
			}
		}
		var byRoute []int
		if stop := stops[index]; stop != nil && earliest != nil && *stop != *earliest {
			for other, here := range present {
				if here && other != index && stops[other] != nil && *stops[other] < *stop {
					byRoute = append(byRoute, other)
				}
			}
		}
		_, clear := clearDirection(index, boxes, present, container, directions)
		var byNeighbors []int
		if !clear && len(directions) > 0 {
			byNeighbors = unionOfBlockers(index, boxes, present, container, directions)
		}
		results[index] = Reachability{index, len(bySupport) == 0 && len(byRoute) == 0 && clear, bySupport, byNeighbors, byRoute}
	}
	return results, nil
}

func earliestStop(stops []*int64) *int64 {
	var earliest *int64
	for _, stop := range stops {
		if stop != nil && (earliest == nil || *stop < *earliest) {
			earliest = stop
		}
	}
	return earliest
}

func unionOfBlockers(index int, boxes []domain.Box, present presentSet, container domain.Dimensions, directions []string) []int {
	seen := make(map[int]struct{})
	var union []int
	for _, direction := range directions {
		for _, other := range blockingIndices(index, boxes, present, container, direction) {
			if _, found := seen[other]; !found {
				seen[other] = struct{}{}
				union = append(union, other)
			}
		}
	}
	sort.Ints(union)
	return union
}
