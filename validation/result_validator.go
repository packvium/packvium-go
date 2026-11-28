package validation

import (
	"math/big"
)

// ResultIssue is one way a result fails to be a sound answer to its request, with the code and
// detail every Packvium package reports for the same documents.
type ResultIssue struct {
	Code   string
	Detail string
}

func (i ResultIssue) String() string { return i.Code + ": " + i.Detail }

// The kinds of MalformedResult.
const (
	// MalformedContract: the result does not have the documented shape at all.
	MalformedContract = "contract"
	// MalformedShape: the documents cannot be read as a request and a result (a missing key,
	// a value of the wrong type, text that is not JSON).
	MalformedShape = "shape"
)

// MalformedResult refuses documents ValidateResult cannot judge: a contract break, not a
// packing defect, so no issue list is returned. Kind is MalformedContract or MalformedShape.
type MalformedResult struct {
	Kind    string
	Message string
}

func (e *MalformedResult) Error() string { return e.Message }

func shapeRefusal(message string) *MalformedResult {
	return &MalformedResult{Kind: MalformedShape, Message: message}
}

func contractRefusal(message string) *MalformedResult {
	return &MalformedResult{Kind: MalformedContract, Message: message}
}

var requiredResultKeys = []string{"status", "feasibility", "termination", "optimality", "complete", "algorithm",
	"summary", "containers", "unpacked_items", "score"}

// ValidateResult is every way result fails to be a sound answer to request, both given as JSON
// text; an empty list means valid. It shares no code with the engine: units, geometry,
// support, load and the objective are recomputed from the request and the reported
// placements, so it judges any engine's result and returns the issues, in the order and with
// the details, the reference validator returns. Documents it cannot judge are refused with a
// *MalformedResult.
//
// Time is dominated by the pairwise placement checks, O(p^2) per container for p placements,
// and by the exact hull tests for convex_hull items, O(v^4) per pair for v vertices.
func ValidateResult(request, result []byte) (issues []ResultIssue, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			refusal, ok := recovered.(*MalformedResult)
			if !ok {
				panic(recovered)
			}
			issues, err = nil, refusal
		}
	}()
	requestValue, err := decodeDocument(request)
	if err != nil {
		return nil, err
	}
	resultValue, err := decodeDocument(result)
	if err != nil {
		return nil, err
	}
	return validateDocuments(requestValue, resultValue), nil
}

func validateDocuments(request, result interface{}) []ResultIssue {
	for _, key := range requiredResultKeys {
		if !has(result, key) {
			panic(contractRefusal("result is missing " + pyRepr(key)))
		}
	}
	unit := text(get(get(request, "units", newJSONObject()), "length", "mm"), "units.length")
	configuration := get(request, "configuration", newJSONObject())
	clearance := toTicks(get(configuration, "clearance", bi(0)), unit, lengthTicks)
	globalSupport := ratioOf(get(configuration, "minimum_support_ratio", bi(0)))
	context := &checkContext{items: parseItems(request, unit), containers: parseContainers(request, unit),
		clearance: clearance}
	var issues []ResultIssue
	validated := materializeLattices(request, result, context, &issues)
	checkStructure(validated, &issues)
	checkAlgorithmMetrics(validated, &issues)
	checkEffortBudget(request, validated, &issues)
	checkResultFacts(validated, &issues)
	context.rules = containerTagRules(request)
	checkAccounting(validated, context, &issues)
	checkInfeasibleProof(validated, &issues)
	fabricated := checkContainers(validated, result, context, globalSupport, &issues)
	checkContainerCounts(validated, configuration, context, &issues)
	checkFixedPlacements(request, validated, unit, &issues)
	checkGroups(request, validated, &issues)
	checkSolverSelection(request, validated, &issues)
	checkObjective(request, validated, context, fabricated, &issues)
	if issues == nil {
		issues = []ResultIssue{}
	}
	return issues
}

// checkContext is the parts of the request every per-item check reads.
type checkContext struct {
	items      *orderedMap // id -> *itemSpec
	containers *orderedMap // id -> *containerSpec
	clearance  *big.Int
	rules      []interface{}
}

func (c *checkContext) item(id interface{}) *itemSpec {
	spec, present := c.items.get(id)
	if !present {
		return nil
	}
	return spec.(*itemSpec)
}

func (c *checkContext) container(id interface{}) *containerSpec {
	spec, present := c.containers.get(id)
	if !present {
		return nil
	}
	return spec.(*containerSpec)
}

func checkInfeasibleProof(result interface{}, issues *[]ResultIssue) {
	if !isText(member(result, "status"), "infeasible") {
		return
	}
	for _, unpacked := range items(member(result, "unpacked_items"), "result.unpacked_items") {
		if !isText(get(get(unpacked, "proof", newJSONObject()), "level", nil), "proven") {
			*issues = append(*issues, ResultIssue{"unproven_infeasible",
				"infeasible status requires a proven rejection for every unpacked item"})
			return
		}
	}
}

// checkContainers checks each packed container against its type, recomputing a
// made-to-measure box first, and returns those boxes by container id for the objective.
func checkContainers(validated, reported interface{}, context *checkContext, globalSupport *big.Rat,
	issues *[]ResultIssue) *orderedMap {
	fabricated := newOrderedMap()
	originals := items(member(reported, "containers"), "result.containers")
	for index, packed := range items(member(validated, "containers"), "result.containers") {
		if index >= len(originals) {
			break
		}
		containerType := member(packed, "container_type")
		spec := context.container(containerType)
		if spec == nil {
			*issues = append(*issues, ResultIssue{"unknown_container_type", pyStr(containerType)})
			continue
		}
		spec = checkMadeToMeasure(packed, has(originals[index], "lattice_summary"), spec, context, issues)
		if spec.madeToMeasure != nil {
			fabricated.set(member(packed, "id"), spec)
		}
		checkContainer(packed, spec, context, globalSupport, issues)
		checkAchievedExtent(packed, spec, context.clearance, issues)
	}
	return fabricated
}

func checkContainerCounts(validated, configuration interface{}, context *checkContext, issues *[]ResultIssue) {
	opened := items(member(validated, "containers"), "result.containers")
	inventory := newOrderedMap()
	for _, packed := range opened {
		containerType := member(packed, "container_type")
		if context.containers.has(containerType) {
			inventory.set(containerType, inventory.countOf(containerType)+1)
		}
	}
	for index, containerType := range inventory.keys {
		count := inventory.values[index].(int64)
		quantity := context.container(containerType).quantity
		if quantity != nil && bi(count).Cmp(quantity) > 0 {
			*issues = append(*issues, ResultIssue{"container_inventory_exceeded",
				pyStr(containerType) + ": used " + bi(count).String() + ", available " + quantity.String()})
		}
	}
	limit := optionalInteger(get(configuration, "max_containers", nil), "max_containers")
	if limit != nil && bi(int64(len(opened))).Cmp(limit) > 0 {
		*issues = append(*issues, ResultIssue{"max_containers_exceeded",
			bi(int64(len(opened))).String() + " containers opened, limit " + limit.String()})
	}
}
