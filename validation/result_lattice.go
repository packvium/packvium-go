package validation

import (
	"math/big"
	"strconv"
)

// Compact lattice results, expanded independently before ordinary validation. The summary is
// untrusted: its parameters must agree with the request before they are used to synthesize
// placements, which then pass through the same checks as explicit ones.

var sideNames = []string{"length", "width", "height"}

type latticeFields struct {
	orientation                          interface{}
	physical, envelope                   []interface{}
	nx, ny, layersUsed, layerStep, count interface{}
}

// materializeLattices is the result with every valid summary expanded into placements; a
// result without one is returned as it is.
func materializeLattices(request, result interface{}, context *checkContext, issues *[]ResultIssue) interface{} {
	summarized := false
	for _, packed := range items(member(result, "containers"), "result.containers") {
		summarized = summarized || has(packed, "lattice_summary")
	}
	if !summarized {
		return result
	}
	expanded := result.(*jsonObject).clone()
	optional := isFalse(get(get(request, "configuration", newJSONObject()), "require_placement_coordinates", nil))
	for _, packed := range items(member(expanded, "containers"), "result.containers") {
		if placements, replace := expandLattice(packed, optional, context, issues); replace {
			packed.(*jsonObject).set("placements", placements)
		}
	}
	return expanded
}

func isFalse(value interface{}) bool { flag, ok := value.(bool); return ok && !flag }

func expandLattice(packed interface{}, optional bool, context *checkContext, issues *[]ResultIssue) ([]interface{}, bool) {
	summary := get(packed, "lattice_summary", nil)
	if summary == nil {
		return nil, false
	}
	containerID := pyStr(get(packed, "id", "?"))
	if !optional {
		*issues = append(*issues, ResultIssue{"unexpected_lattice_summary",
			containerID + ": request requires explicit placement coordinates"})
	}
	if truthy(get(packed, "placements", nil)) {
		*issues = append(*issues, ResultIssue{"lattice_placements_conflict",
			containerID + ": compact summary and explicit placements are mutually exclusive"})
	}
	itemType := get(summary, "item_type", nil)
	item := context.item(itemType)
	container := context.container(get(packed, "container_type", nil))
	if item == nil || container == nil {
		// The ordinary accounting and container checks report the unknown reference.
		return nil, false
	}
	fields := readLatticeFields(summary, containerID)
	problems := latticeProblems(fields, item, itemType, container, context.clearance)
	if len(problems) > 0 {
		for _, problem := range problems {
			*issues = append(*issues, ResultIssue{"lattice_summary_mismatch", containerID + ": " + problem})
		}
		return []interface{}{}, true
	}
	return synthesizeLattice(fields, summary, itemType, containerID, context.clearance), true
}

// readLatticeFields reads the summary's canonical fields; a missing one is the reference's
// contract refusal, naming the container.
func readLatticeFields(summary interface{}, containerID string) (fields latticeFields) {
	defer func() {
		if recovered := recover(); recovered != nil {
			missing, ok := recovered.(*MalformedResult)
			if !ok {
				panic(recovered)
			}
			panic(contractRefusal(containerID + ": lattice_summary is missing a canonical field: " + missing.Message))
		}
	}()
	sides := func(key string) []interface{} {
		block := member(summary, key)
		values := make([]interface{}, len(sideNames))
		for index, side := range sideNames {
			values[index] = member(member(block, side), "ticks")
		}
		return values
	}
	return latticeFields{
		orientation: member(summary, "orientation"), physical: sides("physical_dimensions"),
		envelope: sides("envelope_dimensions"), nx: member(summary, "nx"), ny: member(summary, "ny"),
		layersUsed: member(summary, "layers_used"), layerStep: member(member(summary, "layer_step"), "ticks"),
		count: member(summary, "count"),
	}
}

func latticeProblems(fields latticeFields, item *itemSpec, itemType interface{}, container *containerSpec,
	clearance *big.Int) []string {
	var problems []string
	if !isRotation(fields.orientation) {
		problems = append(problems, "unknown orientation "+pyRepr(fields.orientation))
	} else {
		orientation := fields.orientation.(string)
		expected := item.rotated(orientation)
		if !matchesTicks(fields.physical, expected) {
			problems = append(problems, "physical dimensions "+valueTuple(fields.physical)+" != rotated item "+
				intTuple(expected))
		}
		if !item.allows(orientation) {
			problems = append(problems, "orientation "+orientation+" is forbidden for "+pyStr(itemType))
		}
	}
	margin := mul(bi(2), clearance)
	expectedEnvelope := make([]*big.Int, len(fields.physical))
	for index, side := range fields.physical {
		expectedEnvelope[index] = add(integer(side, "physical dimension"), margin)
	}
	if !matchesTicks(fields.envelope, expectedEnvelope) {
		problems = append(problems, "envelope dimensions "+valueTuple(fields.envelope)+" != physical+clearance "+
			intTuple(expectedEnvelope))
	}
	if item.nestingHeight != nil {
		problems = append(problems, "compact lattice form is not valid for nesting items")
	}
	if !equals(fields.layerStep, fields.envelope[2]) {
		problems = append(problems, "layer_step "+pyStr(fields.layerStep)+" != envelope height "+pyStr(fields.envelope[2]))
	}
	for _, field := range []struct {
		name  string
		value interface{}
	}{{"nx", fields.nx}, {"ny", fields.ny}, {"layers_used", fields.layersUsed}, {"layer_step", fields.layerStep},
		{"count", fields.count}} {
		if value := strictInt(field.value); value == nil || value.Cmp(bi(1)) < 0 {
			problems = append(problems, field.name+" must be a positive integer, got "+pyRepr(field.value))
		}
	}
	if len(problems) == 0 {
		problems = latticeCounts(fields, item, container)
	}
	return problems
}

// latticeCounts holds the summary to the counts computed from the request; reached only once
// every field is a positive integer and the envelope is the expected one.
func latticeCounts(fields latticeFields, item *itemSpec, container *containerSpec) []string {
	var problems []string
	nx, ny, layersUsed, layerStep, count := fields.nx.(*big.Int), fields.ny.(*big.Int), fields.layersUsed.(*big.Int),
		fields.layerStep.(*big.Int), fields.count.(*big.Int)
	expectedNX := floorQuo(container.inner[0], integer(fields.envelope[0], "envelope"))
	expectedNY := floorQuo(container.inner[1], integer(fields.envelope[1], "envelope"))
	expectedLayers := ceilDiv(count, mul(nx, ny))
	if nx.Cmp(expectedNX) != 0 {
		problems = append(problems, "nx "+nx.String()+" != independently computed "+expectedNX.String())
	}
	if ny.Cmp(expectedNY) != 0 {
		problems = append(problems, "ny "+ny.String()+" != independently computed "+expectedNY.String())
	}
	if layersUsed.Cmp(expectedLayers) != 0 {
		problems = append(problems, "layers_used "+layersUsed.String()+" != independently computed "+expectedLayers.String())
	}
	if count.Cmp(integer(item.quantity, "quantity")) > 0 {
		problems = append(problems, "count "+count.String()+" exceeds requested quantity "+pyStr(item.quantity))
	}
	if capacity := floorQuo(container.inner[2], layerStep); layersUsed.Cmp(capacity) > 0 {
		problems = append(problems, "layers_used "+layersUsed.String()+" exceeds vertical capacity "+capacity.String())
	}
	return problems
}

func synthesizeLattice(fields latticeFields, summary, itemType interface{}, containerID string,
	clearance *big.Int) []interface{} {
	nx, ny, count := fields.nx.(*big.Int).Int64(), fields.ny.(*big.Int).Int64(), fields.count.(*big.Int).Int64()
	envelopeX, envelopeY := integer(fields.envelope[0], "envelope"), integer(fields.envelope[1], "envelope")
	step := fields.layerStep.(*big.Int)
	physical := member(summary, "physical_dimensions")
	tick := func(value *big.Int) interface{} {
		block := newJSONObject()
		block.set("ticks", value)
		return block
	}
	placements := make([]interface{}, 0, count)
	for index := int64(0); index < count; index++ {
		position := newJSONObject()
		position.set("x", tick(add(mul(bi(index%nx), envelopeX), clearance)))
		position.set("y", tick(add(mul(bi((index/nx)%ny), envelopeY), clearance)))
		position.set("z", tick(add(mul(bi(index/(nx*ny)), step), clearance)))
		placement := newJSONObject()
		placement.set("item_id", pyStr(itemType)+"#lattice:"+containerID+":"+strconv.FormatInt(index+1, 10))
		placement.set("item_type", itemType)
		placement.set("orientation", fields.orientation)
		placement.set("position", position)
		placement.set("dimensions", cloneValue(physical))
		placements = append(placements, placement)
	}
	return placements
}

// matchesTicks is element-wise reported == expected for a tuple of JSON values.
func matchesTicks(reported []interface{}, expected []*big.Int) bool {
	if reported == nil || len(reported) != len(expected) {
		return false
	}
	for index, value := range expected {
		if !equalsInt(reported[index], value) {
			return false
		}
	}
	return true
}
