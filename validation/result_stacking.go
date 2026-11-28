package validation

import (
	"math/big"
	"sort"
)

// What rests on what, and everything that follows from it: support, top load, stacking
// limits, load propagation, crush, stack density and the route's unloading order. Entries are
// a container's placements by item id, in the reference's dict order; edges refer to them by
// position.

// squareMetreTicks is one square metre in length ticks squared.
var squareMetreTicks = bi(256_000_000_000_000)

type supportEdge struct {
	lower int
	area  *big.Int
}

// supportGraph is the exact direct-support edges and the items directly above each. Ordinary
// face contacts keep placement order; nested instances of one column are linked only to
// their adjacent predecessor, which replaces any shadowed face contact from the same column.
// O(n * k) for k entries sharing a top height.
func supportGraph(entries []*placementEntry) ([][]supportEdge, []map[int]bool) {
	byTop := map[string][]int{}
	for index, entry := range entries {
		key := entry.envelope.z2.String()
		byTop[key] = append(byTop[key], index)
	}
	supporters := make([][]supportEdge, len(entries))
	for upper, entry := range entries {
		for _, lower := range byTop[entry.envelope.z.String()] {
			if lower == upper {
				continue
			}
			if area := entries[lower].envelope.overlapAreaXY(entry.envelope); area.Sign() > 0 {
				supporters[upper] = append(supporters[upper], supportEdge{lower, area})
			}
		}
	}
	groupKeys := map[int]string{}
	groups := map[string][]int{}
	var groupOrder []string
	for index, entry := range entries {
		if entry.spec == nil || entry.spec.nestingHeight == nil {
			continue
		}
		box := entry.envelope
		key := hashKey(member(entry.placement, "item_type")) + "\x00" + box.x.String() + "," + box.y.String() + "," +
			box.x2.String() + "," + box.y2.String() + "," + entry.spec.nestingHeight.String()
		groupKeys[index] = key
		if _, known := groups[key]; !known {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], index)
	}
	predecessors := map[int]int{}
	var uppers []int
	for _, key := range groupOrder {
		group := append([]int(nil), groups[key]...)
		sort.SliceStable(group, func(left, right int) bool {
			if order := entries[group[left]].envelope.z.Cmp(entries[group[right]].envelope.z); order != 0 {
				return order < 0
			}
			return group[left] < group[right]
		})
		for position := 0; position+1 < len(group); position++ {
			lower, upper := group[position], group[position+1]
			if sub(entries[lower].envelope.z2, entries[upper].envelope.z).Cmp(entries[lower].spec.nestingHeight) == 0 {
				if _, seen := predecessors[upper]; !seen {
					uppers = append(uppers, upper)
				}
				predecessors[upper] = lower
			}
		}
	}
	for _, upper := range uppers {
		lower, key := predecessors[upper], groupKeys[upper]
		var edges []supportEdge
		for _, edge := range supporters[upper] {
			if other, grouped := groupKeys[edge.lower]; !grouped || other != key {
				edges = append(edges, edge)
			}
		}
		at := len(edges)
		for position, edge := range edges {
			if edge.lower > lower {
				at = position
				break
			}
		}
		edges = append(edges, supportEdge{})
		copy(edges[at+1:], edges[at:])
		edges[at] = supportEdge{lower, entries[upper].envelope.baseArea}
		supporters[upper] = edges
	}
	directAbove := make([]map[int]bool, len(entries))
	for index := range directAbove {
		directAbove[index] = map[int]bool{}
	}
	for upper, edges := range supporters {
		for _, edge := range edges {
			directAbove[edge.lower][upper] = true
		}
	}
	return supporters, directAbove
}

func sortedIndices(set map[int]bool) []int {
	keys := make([]int, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func checkSupportAndLoad(entries []*placementEntry, supporters [][]supportEdge, directAbove []map[int]bool,
	globalSupport *big.Rat, issues *[]ResultIssue) {
	for index, entry := range entries {
		spec := entry.spec
		if spec == nil || entry.envelope.z.Sign() == 0 {
			continue
		}
		itemID := pyStr(entry.id)
		supporting := supporters[index]
		required := globalSupport
		if globalSupport.Cmp(spec.minimumSupportRatio) < 0 {
			required = spec.minimumSupportRatio
		}
		checkGroundContact(itemID, entry.envelope, supporting, entries, spec.groundContactRule, issues)
		if required.Sign() > 0 {
			area := bi(0)
			for _, edge := range supporting {
				area = add(area, edge.area)
			}
			needed := floorQuo(mul(entry.envelope.baseArea, bi(ratioPPM(required))), bi(scoreScale))
			if area.Cmp(needed) < 0 {
				*issues = append(*issues, ResultIssue{"insufficient_support", itemID + ": " +
					fixed6(ratFloat(fraction(area, entry.envelope.baseArea))) + " < " + fixed6(ratFloat(required))})
			}
		}
		for _, edge := range supporting {
			lowerSpec := entries[edge.lower].spec
			if lowerSpec == nil {
				continue
			}
			lowerID := pyStr(entries[edge.lower].id)
			switch {
			case !lowerSpec.stackable:
				*issues = append(*issues, ResultIssue{"non_stackable", itemID + " rests on " + lowerID})
			case len(lowerSpec.acceptsStackingGroups) > 0 && !lowerSpec.acceptsStackingGroups.holds(spec.stackingGroup):
				*issues = append(*issues, ResultIssue{"stacking_group_incompatible", itemID + " rests on " + lowerID})
			}
			if lowerSpec.maxTopLoad != nil && spec.weight.Cmp(lowerSpec.maxTopLoad) > 0 {
				*issues = append(*issues, ResultIssue{"top_load_exceeded", itemID + " (" + spec.weight.String() +
					" ticks) on " + lowerID + " (limit " + lowerSpec.maxTopLoad.String() + ")"})
			}
		}
	}
	for index, entry := range entries {
		if entry.spec == nil || entry.spec.maxStackedItems == nil {
			continue
		}
		// Iterative, so the walk's depth does not grow with the stack.
		descendants := map[int]bool{}
		pending := sortedIndices(directAbove[index])
		for len(pending) > 0 {
			above := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if descendants[above] {
				continue
			}
			descendants[above] = true
			pending = append(pending, sortedIndices(directAbove[above])...)
		}
		if count := bi(int64(len(descendants))); count.Cmp(entry.spec.maxStackedItems) > 0 {
			*issues = append(*issues, ResultIssue{"max_stacked_items_exceeded", pyStr(entry.id) + ": " +
				count.String() + " above > limit " + entry.spec.maxStackedItems.String()})
		}
	}
}

func checkGroundContact(itemID string, envelope cuboid, supporting []supportEdge, entries []*placementEntry,
	rule interface{}, issues *[]ResultIssue) {
	switch {
	case rule == nil || isText(rule, "free"):
	case isText(rule, "covered"):
		corners := [][2]*big.Int{{envelope.x, envelope.y}, {envelope.x2, envelope.y}, {envelope.x, envelope.y2},
			{envelope.x2, envelope.y2}}
		for _, corner := range corners {
			covered := false
			for _, edge := range supporting {
				surface := entries[edge.lower].envelope
				if surface.x.Cmp(corner[0]) <= 0 && corner[0].Cmp(surface.x2) <= 0 &&
					surface.y.Cmp(corner[1]) <= 0 && corner[1].Cmp(surface.y2) <= 0 {
					covered = true
					break
				}
			}
			if !covered {
				*issues = append(*issues, ResultIssue{"ground_contact_violation", itemID + ": covered requires all four base corners"})
				return
			}
		}
	case isText(rule, "single") && len(supporting) != 1:
		*issues = append(*issues, ResultIssue{"ground_contact_violation",
			itemID + ": single has " + bi(int64(len(supporting))).String() + " supporters"})
	case isText(rule, "multiple") && len(supporting) < 2:
		*issues = append(*issues, ResultIssue{"ground_contact_violation",
			itemID + ": multiple has " + bi(int64(len(supporting))).String() + " supporters"})
	}
}

// propagateLoad is the cumulative weight each item bears from everything resting on it,
// split across supporters by contact area and conserved exactly (the remainder goes to the
// last supporter), top down. O(n log n + edges).
func propagateLoad(entries []*placementEntry, supporters [][]supportEdge) []*big.Int {
	order := make([]int, len(entries))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(left, right int) bool {
		a, b := entries[order[left]].envelope, entries[order[right]].envelope
		if c := b.z2.Cmp(a.z2); c != 0 {
			return c < 0
		}
		if c := b.z.Cmp(a.z); c != 0 {
			return c < 0
		}
		return compare(entries[order[left]].id, entries[order[right]].id) < 0
	})
	loads := make([]*big.Int, len(entries))
	for index := range loads {
		loads[index] = bi(0)
	}
	for _, upper := range order {
		spec := entries[upper].spec
		if spec == nil {
			continue
		}
		supports := supporters[upper]
		total := bi(0)
		for _, edge := range supports {
			total = add(total, edge.area)
		}
		if total.Sign() == 0 {
			continue
		}
		downward := add(spec.weight, loads[upper])
		assigned := bi(0)
		for position, edge := range supports {
			share := floorQuo(mul(downward, edge.area), total)
			if position == len(supports)-1 {
				share = sub(downward, assigned)
			}
			assigned = add(assigned, share)
			loads[edge.lower] = add(loads[edge.lower], share)
		}
	}
	return loads
}

// pressure is the pressure a footprint carries in kPa, exactly:
// load * g * (ticks per metre)^2 / (ticks per kg * g denominator * Pa per kPa * area).
func pressure(load, baseArea *big.Int) *big.Rat {
	return fraction(mul(load, mul(bi(980_665), squareMetreTicks)), mul(mul(bi(ticksPerKG), bi(100_000_000)), baseArea))
}

// checkCrush: no compressible item may carry more pressure than it declared.
func checkCrush(label *packedLabel, entries []*placementEntry, loads []*big.Int, issues *[]ResultIssue) {
	for index, entry := range entries {
		if entry.spec == nil || entry.spec.maxCompressionKPA == nil {
			continue
		}
		limit := entry.spec.maxCompressionKPA
		if carried := pressure(loads[index], entry.envelope.baseArea); carried.Cmp(ratOf(limit)) > 0 {
			*issues = append(*issues, ResultIssue{"crush_violation", pyStr(entry.id) + " in " + label.String() + ": " +
				fixed6(ratFloat(carried)) + " kPa exceeds " + limit.String() + " kPa"})
		}
	}
}

// compressedHeight is the occupied height under load, rounded up, never below one tick. A
// crushed item keeps its height: checkCrush already reports it.
func compressedHeight(envelope cuboid, spec *itemSpec, load *big.Int) *big.Int {
	limit := spec.maxCompressionKPA
	if limit == nil {
		return envelope.height
	}
	if spec.compressionRatioPPM == nil {
		panic(shapeRefusal(spec.id + ": max_compression_pressure_kpa without compression_ratio"))
	}
	carried := pressure(load, envelope.baseArea)
	if carried.Cmp(ratOf(limit)) > 0 || limit.Sign() == 0 {
		return envelope.height
	}
	divisor := mul(mul(limit, bi(1_000_000)), carried.Denom())
	retained := sub(divisor, mul(spec.compressionRatioPPM, carried.Num()))
	return maxInt(bi(1), ceilDiv(mul(envelope.height, retained), divisor))
}

// checkStackDensity: weight per square metre of an item's own base, at every level.
func checkStackDensity(label *packedLabel, entries []*placementEntry, loads []*big.Int, maxDensity *big.Int,
	issues *[]ResultIssue) {
	for index, entry := range entries {
		if entry.spec == nil {
			continue
		}
		total := add(entry.spec.weight, loads[index])
		if mul(total, squareMetreTicks).Cmp(mul(maxDensity, entry.envelope.baseArea)) > 0 {
			*issues = append(*issues, ResultIssue{"stack_density_exceeded", pyStr(entry.id) + " in " + label.String() + ": " +
				total.String() + " ticks over " + entry.envelope.baseArea.String() + " ticks^2 exceeds " +
				maxDensity.String() + " ticks/m^2"})
		}
	}
}

// checkRouteRemovalOrder: items due at an earlier stop must all be removable, unblocked from
// above and slidable out a door (all six walls when the container names none), before the
// route moves on.
func checkRouteRemovalOrder(label *packedLabel, entries []*placementEntry, directAbove []map[int]bool,
	container [3]*big.Int, doors []string, issues *[]ResultIssue) {
	stops := map[int]*big.Int{}
	var distinct []*big.Int
	for index, entry := range entries {
		if entry.spec == nil || entry.spec.stopIndex == nil {
			continue
		}
		stops[index] = entry.spec.stopIndex
		known := false
		for _, stop := range distinct {
			known = known || stop.Cmp(entry.spec.stopIndex) == 0
		}
		if !known {
			distinct = append(distinct, entry.spec.stopIndex)
		}
	}
	if len(stops) == 0 {
		return
	}
	directions := doors
	if len(directions) == 0 {
		directions = exitDirections
	}
	sort.Slice(distinct, func(left, right int) bool { return distinct[left].Cmp(distinct[right]) < 0 })
	present := map[int]bool{}
	for index := range entries {
		present[index] = true
	}
	for _, stop := range distinct {
		due := map[int]bool{}
		for index, value := range stops {
			if value.Cmp(stop) == 0 && present[index] {
				due[index] = true
			}
		}
		for len(due) > 0 {
			chosen := -1
			for _, item := range sortedIndices(due) {
				blocked := false
				for above := range directAbove[item] {
					blocked = blocked || present[above]
				}
				if blocked || !hasClearExit(item, entries, present, container, directions) {
					continue
				}
				if chosen < 0 || compare(entries[item].id, entries[chosen].id) < 0 {
					chosen = item
				}
			}
			if chosen < 0 {
				var names []interface{}
				for _, index := range sortedIndices(due) {
					names = append(names, entries[index].id)
				}
				sortValues(names)
				*issues = append(*issues, ResultIssue{"unloading_order_violation", label.String() + ": stop " +
					stop.String() + " cannot fully unload " + reprList(names) + " before the route moves on"})
				return
			}
			delete(present, chosen)
			delete(due, chosen)
		}
	}
}

func hasClearExit(item int, entries []*placementEntry, present map[int]bool, container [3]*big.Int,
	directions []string) bool {
	for _, direction := range directions {
		region := sweptRegion(entries[item].envelope, container, direction)
		blocked := false
		for _, other := range sortedIndices(present) {
			if other != item && region.intersects(entries[other].envelope) {
				blocked = true
				break
			}
		}
		if !blocked {
			return true
		}
	}
	return false
}

func sweptRegion(envelope cuboid, container [3]*big.Int, direction string) cuboid {
	x1, y1, z1, x2, y2, z2 := envelope.x, envelope.y, envelope.z, envelope.x2, envelope.y2, envelope.z2
	switch direction {
	case "+x":
		x1, x2 = x2, container[0]
	case "-x":
		x1, x2 = bi(0), x1
	case "+y":
		y1, y2 = y2, container[1]
	case "-y":
		y1, y2 = bi(0), y1
	case "+z":
		z1, z2 = z2, container[2]
	default:
		z1, z2 = bi(0), z1
	}
	return newCuboid(x1, y1, z1, sub(x2, x1), sub(y2, y1), sub(z2, z1))
}

// validNesting is whether two stacked instances form a valid nested column.
func validNesting(left, right *placementEntry) bool {
	if left.spec == nil || right.spec == nil || left.spec.nestingHeight == nil {
		return false
	}
	nesting := left.spec.nestingHeight
	if left.spec.id != right.spec.id || !equals(member(left.placement, "item_type"), member(right.placement, "item_type")) ||
		right.spec.nestingHeight == nil || right.spec.nestingHeight.Cmp(nesting) != 0 {
		return false
	}
	a, b := left.envelope, right.envelope
	if a.x.Cmp(b.x) != 0 || a.y.Cmp(b.y) != 0 || a.x2.Cmp(b.x2) != 0 || a.y2.Cmp(b.y2) != 0 {
		return false
	}
	lower, upper := a, b
	if a.z.Cmp(b.z) > 0 {
		lower, upper = b, a
	}
	return lower.z.Cmp(upper.z) != 0 && sub(lower.z2, upper.z).Cmp(nesting) == 0
}

// occupiedVolume is the space a placement actually takes: its compressed box, its hull, or
// its box.
func occupiedVolume(envelope cuboid, placement interface{}, spec *itemSpec, load *big.Int) *big.Int {
	if spec != nil && spec.maxCompressionKPA != nil {
		return mul(envelope.baseArea, compressedHeight(envelope, spec, load))
	}
	if spec != nil && spec.isConvexHull() && spec.hullVertices != nil {
		return hullVolume(rotateHull(spec.hullVertices, axesOf(text(member(placement, "orientation"), "orientation"))))
	}
	solid, exact := placedSolid(envelope, placement, spec)
	if !exact {
		dimensions := member(placement, "dimensions")
		side := func(name string) *big.Int {
			return integer(member(member(dimensions, name), "ticks"), "a placement dimension")
		}
		return mul(mul(side("length"), side("width")), side("height"))
	}
	return hullVolume(solid)
}

// placedHull is the solid a placement occupies, as vertices in container coordinates.
func placedHull(envelope cuboid, placement interface{}, spec *itemSpec) []vertex {
	solid, _ := placedSolid(envelope, placement, spec)
	return solid
}

// placedSolid is the exact hull when the request allows it and the reported box matches,
// otherwise the envelope's corners; exact says which.
func placedSolid(envelope cuboid, placement interface{}, spec *itemSpec) ([]vertex, bool) {
	if spec == nil || spec.hullVertices == nil || !spec.isConvexHull() || spec.stopIndex != nil {
		return envelope.vertices(), false
	}
	axes := axesOf(text(member(placement, "orientation"), "orientation"))
	if envelope.length.Cmp(spec.dimensions[axes[0]]) != 0 || envelope.width.Cmp(spec.dimensions[axes[1]]) != 0 ||
		envelope.height.Cmp(spec.dimensions[axes[2]]) != 0 {
		return envelope.vertices(), false
	}
	local := rotateHull(spec.hullVertices, axes)
	solid := make([]vertex, len(local))
	for index, point := range local {
		solid[index] = vertex{add(envelope.x, point[0]), add(envelope.y, point[1]), add(envelope.z, point[2])}
	}
	return solid, true
}
