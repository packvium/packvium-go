package validation

import (
	"math/big"
)

// One packed container: geometry, loads, accounting figures, and a made-to-measure box.

// scoreScale is parts per million, the scale of the objective's ratios and of the reserve.
const scoreScale = 1_000_000

// ratioPPM is a support, reserve or compression ratio in parts per million exactly as every
// solver converts it: the double floor(ratio * 10^6 + 0.5). Rounding the exact rational
// instead disagrees on about one seven-decimal half in a hundred.
func ratioPPM(ratio *big.Rat) int64 {
	// The explicit conversion rounds the product before the addition, so the two are never
	// fused into one multiply-add that rounds once.
	return int64(float64(ratioFloat(ratio)*scoreScale) + 0.5)
}

func ratioFloat(ratio *big.Rat) float64 { return ratFloat(ratio) }

// placementEntry is one placement as the container checks see it.
type placementEntry struct {
	id        interface{}
	envelope  cuboid
	placement interface{}
	spec      *itemSpec
}

// packedLabel is a packed container's id as an issue prints it. The reference reads the id
// only while formatting an issue, so a missing id fails only once an issue needs it; verify
// then aborts the run, as the reference's KeyError does.
type packedLabel struct {
	text        string
	missing     bool
	missingUsed bool
}

func newPackedLabel(packed interface{}) *packedLabel {
	if !has(packed, "id") {
		get(packed, "id", nil)
		return &packedLabel{missing: true}
	}
	return &packedLabel{text: pyStr(member(packed, "id"))}
}

func (l *packedLabel) String() string {
	if l.missing {
		l.missingUsed = true
		return "?"
	}
	return l.text
}

func (l *packedLabel) verify() {
	if l.missingUsed {
		panic(shapeRefusal("a packed container with an issue has no id"))
	}
}

// envelopeOf is the physical box grown by the clearance margin. A placement without integer
// tick coordinates is the reference's contract refusal.
func envelopeOf(placement interface{}, clearance *big.Int) (box cuboid) {
	defer func() {
		if recovered := recover(); recovered != nil {
			missing, ok := recovered.(*MalformedResult)
			if !ok {
				panic(recovered)
			}
			panic(contractRefusal("placement is missing tick coordinates: " + missing.Message))
		}
	}()
	position, dimensions := member(placement, "position"), member(placement, "dimensions")
	tick := func(block interface{}, key string) *big.Int {
		return integer(member(member(block, key), "ticks"), "placement ticks")
	}
	margin := mul(bi(2), clearance)
	return newCuboid(sub(tick(position, "x"), clearance), sub(tick(position, "y"), clearance),
		sub(tick(position, "z"), clearance), add(tick(dimensions, "length"), margin),
		add(tick(dimensions, "width"), margin), add(tick(dimensions, "height"), margin))
}

// achievedExtent is the largest envelope x2, y2 and z2 over a container's placements.
func achievedExtent(packed interface{}, clearance *big.Int) [3]*big.Int {
	extent := [3]*big.Int{bi(0), bi(0), bi(0)}
	for index, placement := range items(member(packed, "placements"), "placements") {
		envelope := envelopeOf(placement, clearance)
		if index == 0 {
			extent = [3]*big.Int{envelope.x2, envelope.y2, envelope.z2}
			continue
		}
		extent = [3]*big.Int{maxInt(extent[0], envelope.x2), maxInt(extent[1], envelope.y2), maxInt(extent[2], envelope.z2)}
	}
	return extent
}

// checkAchievedExtent: achieved_dimensions is present exactly when the container declares a
// soft size and equals the tight extent; a flat bag's extent must pass the girth rule.
func checkAchievedExtent(packed interface{}, spec *containerSpec, clearance *big.Int, issues *[]ResultIssue) {
	reported := get(packed, "achieved_dimensions", nil)
	label := newPackedLabel(packed)
	defer label.verify()
	if !spec.reportsAchievedExtent {
		if reported != nil {
			*issues = append(*issues, ResultIssue{"achieved_dimensions_mismatch", label.String() + ": reported for a container of fixed size"})
		}
		return
	}
	extent := achievedExtent(packed, clearance)
	var stated []interface{}
	if isDict(reported) {
		for _, side := range sideNames {
			stated = append(stated, get(get(reported, side, newJSONObject()), "ticks", nil))
		}
	}
	if !matchesTicks(stated, extent[:]) {
		*issues = append(*issues, ResultIssue{"achieved_dimensions_mismatch",
			label.String() + ": reported " + optionalTuple(stated) + ", computed " + intTuple(extent[:])})
	}
	a, b, c := extent[0], extent[1], extent[2]
	if spec.flatBag && (add(a, c).Cmp(spec.inner[0]) > 0 || add(b, c).Cmp(spec.inner[1]) > 0) {
		*issues = append(*issues, ResultIssue{"bag_girth_exceeded", label.String() + ": a + c = " + add(a, c).String() +
			", b + c = " + add(b, c).String() + ", flat size " + spec.inner[0].String() + " x " + spec.inner[1].String()})
	}
}

func optionalTuple(values []interface{}) string {
	if values == nil {
		return "None"
	}
	return valueTuple(values)
}

// reportedTicks is (value.get(side) or {}).get("ticks") per side, or nil when value is not an
// object.
func reportedTicks(value interface{}) []interface{} {
	if !isDict(value) {
		return nil
	}
	ticks := make([]interface{}, len(sideNames))
	for index, side := range sideNames {
		ticks[index] = nestedTicks(value, side)
	}
	return ticks
}

func nestedTicks(block interface{}, key string) interface{} {
	value := get(block, key, nil)
	if !truthy(value) {
		return nil
	}
	return get(value, "ticks", nil)
}

// checkMadeToMeasure recomputes a made-to-measure box from its placements alone and returns
// the spec every later check uses: the recomputed box, never the reported one. O(p).
func checkMadeToMeasure(packed interface{}, compact bool, spec *containerSpec, context *checkContext,
	issues *[]ResultIssue) *containerSpec {
	containerID := pyStr(get(packed, "id", "?"))
	block := get(packed, "made_to_measure", nil)
	machine := spec.madeToMeasure
	if machine == nil {
		if block != nil {
			*issues = append(*issues, ResultIssue{"made_to_measure_unexpected", containerID})
		}
		return spec
	}
	if !isDict(block) {
		*issues = append(*issues, ResultIssue{"made_to_measure_missing", containerID})
		block = newJSONObject()
	}
	if compact {
		*issues = append(*issues, ResultIssue{"lattice_summary_on_made_to_measure", containerID})
	}
	achieved := achievedExtent(packed, context.clearance)
	var fabricated, outer [3]*big.Int
	for axis := 0; axis < 3; axis++ {
		minimum, increment := machine.minimum[axis], machine.increment[axis]
		steps := ceilDiv(sub(maxInt(achieved[axis], minimum), minimum), increment)
		fabricated[axis] = add(minimum, mul(steps, increment))
		outer[axis] = add(fabricated[axis], machine.allowance[axis])
	}
	for axis := 0; axis < 3; axis++ {
		if fabricated[axis].Cmp(spec.inner[axis]) > 0 {
			*issues = append(*issues, ResultIssue{"fabricated_dimensions_exceed_machine",
				containerID + ": " + intTuple(fabricated[:]) + " beyond " + intTuple(spec.inner[:])})
			break
		}
	}
	if reported := reportedTicks(get(packed, "inner_dimensions", nil)); !matchesTicks(reported, fabricated[:]) {
		*issues = append(*issues, ResultIssue{"fabricated_dimensions_mismatch",
			containerID + ": reported " + optionalTuple(reported) + ", computed " + intTuple(fabricated[:])})
	}
	if reported := reportedTicks(get(packed, "outer_dimensions", nil)); !matchesTicks(reported, outer[:]) {
		*issues = append(*issues, ResultIssue{"outer_dimensions_mismatch",
			containerID + ": reported " + optionalTuple(reported) + ", computed " + intTuple(outer[:])})
	}
	cost := add(spec.costMinor, perSquareMetreCeiling(machine.pricePerSquareMetreMinor, fabricated))
	if reported := get(block, "cost_minor", nil); !equalsInt(reported, cost) {
		*issues = append(*issues, ResultIssue{"made_to_measure_cost_mismatch",
			containerID + ": reported " + pyStr(reported) + ", computed " + cost.String()})
	}
	tare := add(spec.tareWeight, perSquareMetreCeiling(machine.tarePerSquareMetre, fabricated))
	if reported := nestedTicks(block, "tare_weight"); !equalsInt(reported, tare) {
		*issues = append(*issues, ResultIssue{"made_to_measure_tare_mismatch",
			containerID + ": reported " + pyStr(reported) + ", computed " + tare.String()})
	}
	gross := add(tare, payloadOf(packed, context))
	if reported := nestedTicks(packed, "gross_weight"); !equalsInt(reported, gross) {
		*issues = append(*issues, ResultIssue{"gross_weight_mismatch",
			containerID + ": reported " + pyStr(reported) + ", computed " + gross.String()})
	}
	recomputed := *spec
	recomputed.inner, recomputed.outer, recomputed.costMinor, recomputed.tareWeight = fabricated, &outer, cost, tare
	return &recomputed
}

// perSquareMetreCeiling is ceil(rate * 2(LW + LH + WH) / 1 m^2).
func perSquareMetreCeiling(rate *big.Int, sides [3]*big.Int) *big.Int {
	area := mul(bi(2), add(add(mul(sides[0], sides[1]), mul(sides[0], sides[2])), mul(sides[1], sides[2])))
	return ceilDiv(mul(rate, area), squareMetreTicks)
}

// payloadOf is the summed weight of every placement whose type the request declares.
func payloadOf(packed interface{}, context *checkContext) *big.Int {
	payload := bi(0)
	for _, placement := range items(member(packed, "placements"), "placements") {
		if spec := context.item(member(placement, "item_type")); spec != nil {
			payload = add(payload, spec.weight)
		}
	}
	return payload
}

func checkContainer(packed interface{}, spec *containerSpec, context *checkContext, globalSupport *big.Rat,
	issues *[]ResultIssue) {
	label := newPackedLabel(packed)
	placements := items(member(packed, "placements"), "placements")
	boundary := newCuboid(bi(0), bi(0), bi(0), spec.inner[0], spec.inner[1], spec.inner[2])
	byID := newOrderedMap()
	payload := bi(0)
	for _, placement := range placements {
		itemID := member(placement, "item_id")
		itemSpec := context.item(member(placement, "item_type"))
		envelope := envelopeOf(placement, context.clearance)
		byID.set(itemID, &placementEntry{itemID, envelope, placement, itemSpec})
		name := pyStr(itemID)
		if !boundary.contains(envelope) {
			*issues = append(*issues, ResultIssue{"outside_container", name + " in " + label.String()})
		}
		for _, obstacle := range spec.obstacles {
			if envelope.intersects(obstacle) && solidsOverlap(placedHull(envelope, placement, itemSpec), obstacle.vertices()) {
				*issues = append(*issues, ResultIssue{"obstacle_collision", name + " in " + label.String()})
			}
		}
		if itemSpec == nil {
			continue
		}
		payload = add(payload, itemSpec.weight)
		checkRotation(placement, itemSpec, name, issues)
		if len(itemSpec.eligibleContainerTags) > 0 && !itemSpec.eligibleContainerTags.intersects(spec.tags) {
			*issues = append(*issues, ResultIssue{"container_ineligible", name + ": " + pyStr(member(packed, "container_type")) +
				" has none of " + reprList(stringValues(itemSpec.eligibleContainerTags.sorted()))})
		}
		if itemSpec.mustBeOnFloor && envelope.z.Sign() != 0 {
			*issues = append(*issues, ResultIssue{"floor_violation", name + " at z=" + envelope.z.String()})
		}
	}
	entries := make([]*placementEntry, len(byID.values))
	for index, value := range byID.values {
		entries[index] = value.(*placementEntry)
	}
	checkPairs(entries, label, issues)
	supporters, directAbove := supportGraph(entries)
	checkSupportAndLoad(entries, supporters, directAbove, globalSupport, issues)
	loads := propagateLoad(entries, supporters)
	if spec.maxStackDensity != nil {
		checkStackDensity(label, entries, loads, spec.maxStackDensity, issues)
	}
	checkCrush(label, entries, loads, issues)
	checkRouteRemovalOrder(label, entries, directAbove, spec.inner, spec.accessDirections, issues)
	checkLimits(packed, label, placements, spec, context, payload, issues)
	checkVolumes(packed, label, placements, spec, context, byID, loads, issues)
	totalWeight := payloadOf(packed, context)
	checkCentreOfMass(packed, label, placements, spec, context, totalWeight, issues)
	checkAxles(packed, label, placements, spec, context, totalWeight, issues)
	label.verify()
}

func checkRotation(placement interface{}, spec *itemSpec, itemID string, issues *[]ResultIssue) {
	orientation := member(placement, "orientation")
	if !isRotation(orientation) {
		*issues = append(*issues, ResultIssue{"unknown_orientation", itemID + ": " + pyStr(orientation)})
		return
	}
	code := orientation.(string)
	if !spec.allows(code) {
		*issues = append(*issues, ResultIssue{"forbidden_rotation", itemID + ": " + code})
	}
	expected := spec.rotated(code)
	dimensions := member(placement, "dimensions")
	actual := make([]interface{}, len(sideNames))
	for index, side := range sideNames {
		actual[index] = member(member(dimensions, side), "ticks")
	}
	if !matchesTicks(actual, expected) {
		*issues = append(*issues, ResultIssue{"dimension_mismatch", itemID + ": " + code + " of " +
			intTuple(spec.dimensions[:]) + " is " + intTuple(expected) + ", got " + valueTuple(actual)})
	}
}

func checkPairs(entries []*placementEntry, label *packedLabel, issues *[]ResultIssue) {
	for first, left := range entries {
		for _, right := range entries[first+1:] {
			if left.envelope.intersects(right.envelope) &&
				solidsOverlap(placedHull(left.envelope, left.placement, left.spec),
					placedHull(right.envelope, right.placement, right.spec)) &&
				!validNesting(left, right) {
				*issues = append(*issues, ResultIssue{"collision", pyStr(left.id) + " with " + pyStr(right.id) + " in " + label.String()})
			}
			if left.spec != nil && right.spec != nil && (left.spec.incompatibleTags.intersects(right.spec.tags) ||
				right.spec.incompatibleTags.intersects(left.spec.tags)) {
				*issues = append(*issues, ResultIssue{"incompatible_items",
					pyStr(left.id) + " is incompatible with " + pyStr(right.id) + " in " + label.String()})
			}
		}
	}
}

func checkLimits(packed interface{}, label *packedLabel, placements []interface{}, spec *containerSpec,
	context *checkContext, payload *big.Int, issues *[]ResultIssue) {
	if spec.maxPayload != nil && payload.Cmp(spec.maxPayload) > 0 {
		*issues = append(*issues, ResultIssue{"payload_exceeded",
			label.String() + ": " + payload.String() + " > " + spec.maxPayload.String() + " ticks"})
	}
	if reported := get(get(packed, "payload_weight", newJSONObject()), "ticks", nil); reported != nil && !equalsInt(reported, payload) {
		*issues = append(*issues, ResultIssue{"payload_weight_mismatch",
			label.String() + ": reported " + pyStr(reported) + ", computed " + payload.String()})
	}
	count := bi(int64(len(placements)))
	if spec.maxItems != nil && count.Cmp(spec.maxItems) > 0 {
		*issues = append(*issues, ResultIssue{"max_items_exceeded", label.String() + ": " + count.String() + " > " + spec.maxItems.String()})
	}
	for _, limit := range spec.tagLimits {
		tagged := int64(0)
		for _, placement := range placements {
			if item := context.item(member(placement, "item_type")); item != nil && item.tags[limit.tag] {
				tagged++
			}
		}
		if bi(tagged).Cmp(limit.limit) > 0 {
			*issues = append(*issues, ResultIssue{"tag_count_exceeded", label.String() + ": " + limit.tag + " count " +
				bi(tagged).String() + " > limit " + limit.limit.String()})
		}
	}
}

func checkVolumes(packed interface{}, label *packedLabel, placements []interface{}, spec *containerSpec,
	context *checkContext, byID *orderedMap, loads []*big.Int, issues *[]ResultIssue) {
	// Every placement counts, through the envelope its item id resolves to: a repeated id
	// resolves to its last placement, as the reference's dict does.
	used := bi(0)
	for _, placement := range placements {
		index, _ := byID.position(member(placement, "item_id"))
		entry := byID.values[index].(*placementEntry)
		used = add(used, occupiedVolume(entry.envelope, placement, context.item(member(placement, "item_type")), loads[index]))
	}
	for first := 0; first < len(byID.values); first++ {
		for second := first + 1; second < len(byID.values); second++ {
			left, right := byID.values[first].(*placementEntry), byID.values[second].(*placementEntry)
			if validNesting(left, right) {
				used = sub(used, mul(mul(left.spec.nestingHeight, left.envelope.length), left.envelope.width))
			}
		}
	}
	if reported := get(packed, "used_volume_ticks3", nil); reported != nil && pyInt(reported, "used_volume_ticks3").Cmp(used) != 0 {
		*issues = append(*issues, ResultIssue{"used_volume_mismatch",
			label.String() + ": reported " + pyStr(reported) + ", computed " + used.String()})
	}
	volume := spec.volume()
	scaled := bi(ratioPPM(spec.voidFillReserveRatio))
	scale := bi(scoreScale)
	reserved := add(mul(floorQuo(volume, scale), scaled), floorQuo(mul(mod(volume, scale), scaled), scale))
	reported := get(packed, "void_fill_reserve_ticks3", nil)
	switch {
	case reported != nil && pyInt(reported, "void_fill_reserve_ticks3").Cmp(reserved) != 0:
		*issues = append(*issues, ResultIssue{"void_fill_reserve_mismatch",
			label.String() + ": reported " + pyStr(reported) + ", computed " + reserved.String()})
	case reserved.Sign() != 0 && reported == nil:
		*issues = append(*issues, ResultIssue{"void_fill_reserve_mismatch",
			label.String() + ": non-zero reserve " + reserved.String() + " is not reported"})
	}
	if usable := sub(volume, reserved); used.Cmp(usable) > 0 {
		*issues = append(*issues, ResultIssue{"void_fill_reserve_exceeded",
			label.String() + ": used " + used.String() + " > usable " + usable.String()})
	}
}

// doubledMoment is sum(weight * (2 * position + extent)) along one axis, over declared types.
func doubledMoment(placements []interface{}, context *checkContext, axis, side string) *big.Int {
	moment := bi(0)
	for _, placement := range placements {
		spec := context.item(member(placement, "item_type"))
		if spec == nil {
			continue
		}
		position := integer(member(member(member(placement, "position"), axis), "ticks"), "placement ticks")
		extent := integer(member(member(member(placement, "dimensions"), side), "ticks"), "placement ticks")
		moment = add(moment, mul(spec.weight, add(mul(bi(2), position), extent)))
	}
	return moment
}

func checkCentreOfMass(packed interface{}, label *packedLabel, placements []interface{}, spec *containerSpec,
	context *checkContext, totalWeight *big.Int, issues *[]ResultIssue) {
	expected := bi(0)
	if totalWeight.Sign() != 0 {
		offset := func(doubled, side *big.Int) *big.Int {
			centred := mul(totalWeight, side)
			return floorQuo(mul(abs(sub(doubled, centred)), bi(scoreScale)), centred)
		}
		expected = maxInt(offset(doubledMoment(placements, context, "x", "length"), spec.inner[0]),
			offset(doubledMoment(placements, context, "y", "width"), spec.inner[1]))
	}
	if reported := get(packed, "centre_of_mass_offset_ppm", nil); !equalsInt(reported, expected) {
		*issues = append(*issues, ResultIssue{"centre_of_mass_mismatch",
			label.String() + ": reported " + pyStr(reported) + ", computed " + expected.String()})
	}
}

func checkAxles(packed interface{}, label *packedLabel, placements []interface{}, spec *containerSpec,
	context *checkContext, totalWeight *big.Int, issues *[]ResultIssue) {
	if !spec.hasAxles {
		if has(packed, "axle_reactions") {
			*issues = append(*issues, ResultIssue{"axle_reaction_mismatch", label.String() + ": reactions reported without configured axles"})
		}
		return
	}
	if len(spec.axles) != 2 {
		panic(shapeRefusal(label.String() + ": axles must name exactly a front and a rear axle"))
	}
	front, rear := spec.axles[0], spec.axles[1]
	denominator := mul(bi(2), sub(rear.position, front.position))
	gross := add(spec.tareWeight, totalWeight)
	doubledX := add(mul(spec.tareWeight, spec.inner[0]), doubledMoment(placements, context, "x", "length"))
	frontNumerator := sub(mul(mul(bi(2), gross), rear.position), doubledX)
	rearNumerator := sub(doubledX, mul(mul(bi(2), gross), front.position))
	expected := newJSONObject()
	expected.set("basis", "gross")
	expected.set("denominator", denominator.String())
	expected.set("front_numerator", frontNumerator.String())
	expected.set("rear_numerator", rearNumerator.String())
	if reported := get(packed, "axle_reactions", nil); !equals(reported, expected) {
		*issues = append(*issues, ResultIssue{"axle_reaction_mismatch",
			label.String() + ": reported " + pyStr(reported) + ", computed " + pyRepr(expected)})
	}
	over := func(limit, numerator *big.Int) bool {
		return limit != nil && numerator.Cmp(mul(limit, denominator)) > 0
	}
	if over(front.limit, frontNumerator) || over(rear.limit, rearNumerator) {
		*issues = append(*issues, ResultIssue{"axle_overloaded", label.String()})
	}
}
