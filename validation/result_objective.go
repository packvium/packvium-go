package validation

import (
	"math/big"
	"sort"
	"strings"
)

// The objective vector recomputed from the result alone, and the request-level rules only the
// whole result can answer: fixed placements, groups and solver selection.

type billing struct {
	divisor, lengthUnit, weightUnit interface{}
}

func checkObjective(request, result interface{}, context *checkContext, fabricated *orderedMap, issues *[]ResultIssue) {
	configuration := get(request, "configuration", newJSONObject())
	selected := get(configuration, "objective", "default")
	reportedName := get(result, "objective", nil)
	if (has(configuration, "objective") || reportedName != nil) && !equals(reportedName, selected) {
		*issues = append(*issues, ResultIssue{"objective_mismatch",
			"reported " + pyRepr(reportedName) + ", requested " + pyRepr(selected)})
	}
	var reported []*big.Int
	for _, component := range items(member(result, "score"), "score") {
		reported = append(reported, pyInt(component, "score"))
	}
	terms := billing{get(configuration, "dimensional_weight_divisor", nil),
		get(configuration, "dimensional_weight_length_unit", "in"), get(configuration, "dimensional_weight_weight_unit", "lb")}
	recomputed := objectiveOf(result, context, fabricated, selected, terms)
	same := len(reported) == len(recomputed)
	for index := range recomputed {
		same = same && reported[index].Cmp(recomputed[index]) == 0
	}
	if !same {
		*issues = append(*issues, ResultIssue{"score_mismatch", "reported " + intList(reported) + ", recomputed " + intList(recomputed)})
	}
}

func objectiveOf(result interface{}, context *checkContext, fabricated *orderedMap, name interface{}, terms billing) []*big.Int {
	cost, unused, height, billable, achievedHeight, landed := bi(0), bi(0), bi(0), bi(0), bi(0), bi(0)
	billed := isText(name, "shipping_cost") || isText(name, "lowest_landed_cost")
	scale := bi(scoreScale)
	containers := items(member(result, "containers"), "result.containers")
	for _, packed := range containers {
		var spec *containerSpec
		if value, present := fabricated.get(get(packed, "id", nil)); present {
			spec = value.(*containerSpec)
		} else {
			spec = context.container(member(packed, "container_type"))
		}
		if spec == nil {
			continue
		}
		cost = add(cost, spec.costMinor)
		volume := spec.volume()
		if volume.Sign() == 0 {
			continue
		}
		used := pyInt(member(packed, "used_volume_ticks3"), "used_volume_ticks3")
		unused = add(unused, floorQuo(mul(sub(volume, used), scale), volume))
		top := bi(0)
		for index, placement := range items(member(packed, "placements"), "placements") {
			z := integer(member(member(member(placement, "position"), "z"), "ticks"), "placement ticks")
			extent := integer(member(member(member(placement, "dimensions"), "height"), "ticks"), "placement ticks")
			reach := add(add(z, extent), context.clearance)
			if index == 0 || reach.Cmp(top) > 0 {
				top = reach
			}
		}
		height = add(height, floorQuo(mul(top, scale), spec.inner[2]))
		achievedHeight = add(achievedHeight, top)
		if billed && terms.divisor != nil {
			gross := pyInt(get(get(packed, "gross_weight", newJSONObject()), "ticks", bi(0)), "gross_weight")
			size := spec.inner
			switch {
			case spec.flatBag:
				size = achievedExtent(packed, context.clearance)
			case spec.outer != nil:
				size = *spec.outer
			}
			charged := maxInt(gross, dimensionalWeight(size, integer(terms.divisor, "dimensional_weight_divisor"),
				text(terms.lengthUnit, "dimensional_weight_length_unit"), text(terms.weightUnit, "dimensional_weight_weight_unit")))
			billable = add(billable, charged)
			if isText(name, "lowest_landed_cost") {
				landed = add(landed, landedCharge(spec, charged))
			}
		}
	}
	unpackedItems := items(member(result, "unpacked_items"), "result.unpacked_items")
	unpacked, opened := bi(int64(len(unpackedItems))), bi(int64(len(containers)))
	objective, ok := name.(string)
	if !ok {
		panic(contractRefusal("unknown requested objective " + pyRepr(name)))
	}
	requiresDivisor := func() {
		if terms.divisor == nil {
			panic(contractRefusal(objective + " objective requires configuration.dimensional_weight_divisor"))
		}
	}
	switch objective {
	case "default":
		return []*big.Int{unpacked, opened, cost, unused, height}
	case "lowest_cost":
		return []*big.Int{unpacked, cost, opened, unused, height}
	case "shipping_cost":
		requiresDivisor()
		return []*big.Int{unpacked, billable, opened, unused, height}
	case "lowest_landed_cost":
		requiresDivisor()
		return []*big.Int{unpacked, landed, opened, unused, height}
	case "open_dimension_height":
		return []*big.Int{unpacked, achievedHeight, opened, cost, unused}
	case "maximum_value":
		forgone := bi(0)
		for _, item := range unpackedItems {
			if spec := context.item(member(item, "item_type")); spec != nil && truthy(spec.value) {
				forgone = add(forgone, integer(spec.value, "value"))
			}
		}
		return []*big.Int{unpacked, forgone, opened, cost, unused}
	case "maximum_volume":
		unpackedVolume, requested := bi(0), bi(0)
		for _, item := range unpackedItems {
			if spec := context.item(member(item, "item_type")); spec != nil {
				unpackedVolume = add(unpackedVolume, spec.nominalVolume())
			}
		}
		for _, value := range context.items.values {
			spec := value.(*itemSpec)
			requested = add(requested, mul(spec.nominalVolume(), integer(spec.quantity, "quantity")))
		}
		unpackedPPM := bi(0)
		if requested.Sign() != 0 {
			unpackedPPM = ceilDiv(mul(unpackedVolume, scale), requested)
		}
		return []*big.Int{unpackedPPM, unpacked, opened, cost, unused}
	}
	panic(contractRefusal("unknown requested objective " + pyRepr(name)))
}

// dimensionalWeight is floor(volume * weight ticks per unit / (length ticks per unit ^ 3 *
// divisor)).
func dimensionalWeight(size [3]*big.Int, divisor *big.Int, lengthUnit, weightUnit string) *big.Int {
	length, weight := lengthTicks.perUnit(lengthUnit), weightTicks.perUnit(weightUnit)
	volume := mul(mul(size[0], size[1]), size[2])
	return floorQuo(mul(volume, weight), mul(mul(mul(length, length), length), divisor))
}

// landedCharge is one container's carrier charge from the request's own rate card: billed
// weight up to whole grams, the minimum charge, then the fuel surcharge rounded up.
func landedCharge(spec *containerSpec, billedTicks *big.Int) *big.Int {
	if spec.rateTable == nil {
		panic(contractRefusal("container " + pyRepr(spec.id) + " has no rate_table, so lowest_landed_cost cannot price it"))
	}
	table := spec.rateTable
	var brackets, prices []*big.Int
	for _, bound := range items(member(table, "weight_brackets_g"), "weight_brackets_g") {
		brackets = append(brackets, pyInt(bound, "bracket"))
	}
	for _, price := range items(member(table, "prices_minor"), "prices_minor") {
		prices = append(prices, pyInt(price, "price"))
	}
	grams := ceilDiv(billedTicks, bi(ticksPerGram))
	for index, bound := range brackets {
		if index >= len(prices) {
			break
		}
		if grams.Cmp(bound) <= 0 {
			base := maxInt(prices[index], pyInt(get(table, "minimum_charge_minor", bi(0)), "minimum_charge_minor"))
			permille := pyInt(get(table, "fuel_surcharge_permille", bi(0)), "fuel_surcharge_permille")
			return add(base, ceilDiv(mul(base, permille), bi(1000)))
		}
	}
	if len(brackets) == 0 {
		panic(shapeRefusal("an empty rate table"))
	}
	panic(contractRefusal("container " + pyRepr(spec.id) + " bills at " + grams.String() +
		" g, above its rate table's last bracket (" + brackets[len(brackets)-1].String() + " g); the shipment has no published price"))
}

func checkSolverSelection(request, result interface{}, issues *[]ResultIssue) {
	selected := get(get(request, "configuration", newJSONObject()), "solvers", []interface{}{})
	if !truthy(selected) {
		return
	}
	listed := items(selected, "configuration.solvers")
	reported := get(member(result, "algorithm"), "solver", nil)
	base := reported
	if typed, ok := reported.(string); ok {
		base = strings.SplitN(typed, ":", 2)[0]
	}
	for _, solver := range listed {
		if equals(solver, base) {
			return
		}
	}
	*issues = append(*issues, ResultIssue{"solver_selection_mismatch",
		"reported " + pyRepr(reported) + ", requested one of " + pyRepr(selected)})
}

// checkGroups: a group travels together or not at all, in one container and never
// half-packed.
func checkGroups(request, result interface{}, issues *[]ResultIssue) {
	rawItems := items(member(request, "items"), "request.items")
	groups := newOrderedMap()
	for _, raw := range rawItems {
		if get(raw, "group", nil) != nil {
			groups.set(member(raw, "id"), member(raw, "group"))
		}
	}
	if groups.count() == 0 {
		return
	}
	located, packed := newOrderedMap(), newOrderedMap()
	for _, container := range items(member(result, "containers"), "result.containers") {
		for _, placement := range items(member(container, "placements"), "placements") {
			group, present := groups.get(member(placement, "item_type"))
			if !present || group == nil {
				continue
			}
			places, known := located.get(group)
			if !known {
				places = newOrderedMap()
			}
			places.(*orderedMap).set(member(container, "id"), true)
			located.set(group, places)
			packed.set(group, packed.countOf(group)+1)
		}
	}
	expected := newOrderedMap()
	for _, raw := range rawItems {
		if group := get(raw, "group", nil); group != nil {
			total, present := expected.get(group)
			if !present {
				total = bi(0)
			}
			expected.set(group, add(total.(*big.Int), integer(get(raw, "quantity", bi(1)), "quantity")))
		}
	}
	names := append([]interface{}(nil), located.keys...)
	sortValues(names)
	for _, group := range names {
		value, _ := located.get(group)
		places := value.(*orderedMap)
		if places.count() > 1 {
			var where []string
			for _, place := range places.keys {
				where = append(where, text(place, "a container id"))
			}
			sort.Strings(where)
			*issues = append(*issues, ResultIssue{"group_split", pyStr(group) + ": " + strings.Join(where, ", ")})
		}
		placed := bi(packed.countOf(group))
		wanted := bi(0)
		if total, present := expected.get(group); present {
			wanted = total.(*big.Int)
		}
		if placed.Cmp(wanted) != 0 {
			*issues = append(*issues, ResultIssue{"group_partially_packed",
				pyStr(group) + ": " + placed.String() + " of " + wanted.String() + " placed"})
		}
	}
}

// checkFixedPlacements: every fixed placement is reported where the request put it, and
// nothing else claims to be.
func checkFixedPlacements(request, result interface{}, unit string, issues *[]ResultIssue) {
	requested := newOrderedMap()
	for _, entry := range items(get(request, "fixed_placements", []interface{}{}), "fixed_placements") {
		containerID := pyStr(member(entry, "container_type")) + "#" + pyStr(get(entry, "container_instance", bi(1)))
		position := get(entry, "position", newJSONObject())
		key := []interface{}{containerID, member(entry, "item_type")}
		for _, axis := range []string{"x", "y", "z"} {
			key = append(key, toTicks(get(position, axis, bi(0)), unit, lengthTicks))
		}
		key = append(key, member(entry, "orientation"))
		requested.set(keyHash(key), key)
	}
	reported := newOrderedMap()
	containerTypes := newOrderedMap()
	for _, container := range items(member(result, "containers"), "result.containers") {
		containerID := get(container, "id", "")
		containerTypes.set(containerID, member(container, "container_type"))
		for _, placement := range items(member(container, "placements"), "placements") {
			if get(placement, "fixed", nil) != true {
				continue
			}
			position := member(placement, "position")
			key := []interface{}{containerID, member(placement, "item_type")}
			for _, axis := range []string{"x", "y", "z"} {
				key = append(key, member(member(position, axis), "ticks"))
			}
			key = append(key, member(placement, "orientation"))
			reported.set(keyHash(key), key)
		}
	}
	for _, value := range sortedKeyList(requested.values) {
		key := value.([]interface{})
		containerID := key[0].(string)
		declaredType := containerID
		if hash := strings.LastIndex(containerID, "#"); hash >= 0 {
			declaredType = containerID[:hash]
		}
		containerType, _ := containerTypes.get(containerID)
		switch {
		case !isText(containerType, declaredType):
			*issues = append(*issues, ResultIssue{"fixed_container_missing", containerID})
		case !reported.has(keyHash(key)):
			*issues = append(*issues, ResultIssue{"fixed_placement_moved", pyStr(key[1]) + " fixed in " + containerID +
				" at " + valueTuple(key[2:5]) + " " + pyStr(key[5])})
		}
	}
	var unexpected []interface{}
	for index, hashed := range reported.keys {
		if !requested.has(hashed) {
			unexpected = append(unexpected, reported.values[index])
		}
	}
	for _, value := range sortedKeyList(unexpected) {
		key := value.([]interface{})
		*issues = append(*issues, ResultIssue{"unexpected_fixed_placement", pyStr(key[1]) + " in " + pyStr(key[0]) +
			" at " + valueTuple(key[2:5]) + " " + pyStr(key[5])})
	}
}

func keyHash(key []interface{}) string {
	parts := make([]string, len(key))
	for index, part := range key {
		parts[index] = hashKey(part)
	}
	return strings.Join(parts, "\x00")
}

func sortedKeyList(keys []interface{}) []interface{} {
	sorted := append([]interface{}(nil), keys...)
	sortValues(sorted)
	return sorted
}
