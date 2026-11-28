package validation

import (
	"math/big"
	"strings"
)

// The summary, the item accounting, and the proof behind every unpacked reason.

var (
	provenReasons = []string{"no_compatible_container_dimensions", "payload_exceeded", "no_eligible_container",
		"rotation_restricted", "policy_rule"}
	limitedReasons  = []string{"time_limit", "effort_limit"}
	observedReasons = []string{"no_feasible_placement", "search_exhausted", "exact_search_incomplete",
		"insufficient_support"}
)

func checkStructure(result interface{}, issues *[]ResultIssue) {
	summary := member(result, "summary")
	containers := items(member(result, "containers"), "result.containers")
	packed := int64(0)
	for _, container := range containers {
		packed += int64(len(items(member(container, "placements"), "placements")))
	}
	unpacked := int64(len(items(member(result, "unpacked_items"), "result.unpacked_items")))
	for _, field := range []struct {
		name   string
		actual int64
	}{{"container_count", int64(len(containers))}, {"packed_item_count", packed}, {"unpacked_item_count", unpacked}} {
		reported := member(summary, field.name)
		if !equalsInt(reported, bi(field.actual)) {
			*issues = append(*issues, ResultIssue{"summary_mismatch",
				field.name + " " + pyStr(reported) + " != " + bi(field.actual).String()})
		}
	}
	complete := member(result, "complete")
	if !equals(complete, unpacked == 0) {
		*issues = append(*issues, ResultIssue{"complete_mismatch",
			"complete=" + pyStr(complete) + " with " + bi(unpacked).String() + " unpacked"})
	}
}

// containerTagRules are the participating require_container_tag rules, resolved from the
// request alone: a second resolution, so a bug shared with an engine cannot hide behind
// agreement with it.
func containerTagRules(request interface{}) []interface{} {
	policy := get(request, "policy", nil)
	if !isDict(policy) {
		return nil
	}
	declared, ok := get(policy, "rules", nil).([]interface{})
	if !ok || len(declared) == 0 {
		return nil
	}
	asOf := intInstance(get(policy, "as_of", nil))
	if asOf == nil {
		return nil
	}
	shipment := get(policy, "shipment", nil)
	if !truthy(shipment) {
		shipment = newJSONObject()
	}
	latest := newOrderedMap()
	for _, rule := range declared {
		if !isDict(rule) || !has(rule, "require_container_tag") || !ruleApplies(rule, shipment) {
			continue
		}
		if compare(get(rule, "effective_at", bi(0)), asOf) > 0 {
			continue
		}
		id := get(rule, "id", nil)
		current, present := latest.get(id)
		if !present || compare([]interface{}{get(rule, "effective_at", bi(0)), get(rule, "version", bi(0))},
			[]interface{}{get(current, "effective_at", bi(0)), get(current, "version", bi(0))}) > 0 {
			latest.set(id, rule)
		}
	}
	return latest.values
}

func ruleApplies(rule, shipment interface{}) bool {
	appliesTo := get(rule, "applies_to", nil)
	if !truthy(appliesTo) {
		return true
	}
	facts, ok := appliesTo.(*jsonObject)
	if !ok {
		panic(shapeRefusal("applies_to must be an object"))
	}
	for _, fact := range facts.keys {
		if !equals(get(shipment, fact, nil), facts.values[fact]) {
			return false
		}
	}
	return true
}

func checkAccounting(result interface{}, context *checkContext, issues *[]ResultIssue) {
	seen := newOrderedMap()
	perType := newOrderedMap()
	for _, container := range items(member(result, "containers"), "result.containers") {
		for _, placement := range items(member(container, "placements"), "placements") {
			itemID := member(placement, "item_id")
			if seen.has(itemID) {
				*issues = append(*issues, ResultIssue{"duplicate_item", pyStr(itemID)})
			}
			seen.set(itemID, true)
			itemType := member(placement, "item_type")
			perType.set(itemType, perType.countOf(itemType)+1)
		}
	}
	algorithm := member(result, "algorithm")
	timedOut := truthy(member(algorithm, "time_limit_reached"))
	effortLimited := truthy(member(algorithm, "effort_limit_reached"))
	for _, unpacked := range items(member(result, "unpacked_items"), "result.unpacked_items") {
		itemID := member(unpacked, "item_id")
		if seen.has(itemID) {
			*issues = append(*issues, ResultIssue{"duplicate_item", pyStr(itemID) + " is both packed and unpacked"})
		}
		seen.set(itemID, true)
		itemType := member(unpacked, "item_type")
		perType.set(itemType, perType.countOf(itemType)+1)
		if !truthy(get(unpacked, "reason", nil)) {
			*issues = append(*issues, ResultIssue{"missing_reason", pyStr(itemID)})
		}
		checkReasonProof(unpacked, context.item(itemType), context, timedOut, effortLimited, issues)
	}
	for index, itemType := range perType.keys {
		count := bi(perType.values[index].(int64))
		spec := context.item(itemType)
		switch {
		case spec == nil:
			*issues = append(*issues, ResultIssue{"unknown_item", pyStr(itemType)})
		case !equalsInt(spec.quantity, count):
			*issues = append(*issues, ResultIssue{"quantity_mismatch",
				pyStr(itemType) + ": " + count.String() + " accounted for, " + pyStr(spec.quantity) + " requested"})
		}
	}
	for index, itemType := range context.items.keys {
		if !perType.has(itemType) {
			spec := context.items.values[index].(*itemSpec)
			*issues = append(*issues, ResultIssue{"quantity_mismatch",
				pyStr(itemType) + ": 0 accounted for, " + pyStr(spec.quantity) + " requested"})
		}
	}
}

// fitsEmpty is whether one envelope of this size fits the empty container: its frame and,
// for a flat bag, both girth sums with the envelope alone at the origin.
func fitsEmpty(container *containerSpec, size [3]*big.Int) bool {
	for axis := 0; axis < 3; axis++ {
		if size[axis].Cmp(container.inner[axis]) > 0 {
			return false
		}
	}
	return !container.flatBag || (add(size[0], size[2]).Cmp(container.inner[0]) <= 0 &&
		add(size[1], size[2]).Cmp(container.inner[1]) <= 0)
}

// fitsSome stops at the first fit, as the reference's generator does, so an unknown rotation
// is an error only when the search reaches it.
func fitsSome(spec *itemSpec, context *checkContext, rotations []interface{}) bool {
	margin := mul(bi(2), context.clearance)
	for _, value := range context.containers.values {
		container := value.(*containerSpec)
		for _, rotation := range rotations {
			axes := axesOf(text(rotation, "a rotation"))
			size := [3]*big.Int{add(spec.dimensions[axes[0]], margin), add(spec.dimensions[axes[1]], margin),
				add(spec.dimensions[axes[2]], margin)}
			if fitsEmpty(container, size) {
				return true
			}
		}
	}
	return false
}

func among(value interface{}, names []string) bool {
	typed, ok := value.(string)
	if !ok {
		return false
	}
	for _, name := range names {
		if typed == name {
			return true
		}
	}
	return false
}

func checkReasonProof(unpacked interface{}, spec *itemSpec, context *checkContext, timedOut, effortLimited bool,
	issues *[]ResultIssue) {
	itemID := pyStr(get(unpacked, "item_id", "?"))
	reason := get(unpacked, "reason", "")
	proof := get(unpacked, "proof", nil)
	if !isDict(proof) {
		*issues = append(*issues, ResultIssue{"missing_reason_proof", itemID})
		return
	}
	hashKey(reason)
	expected := "inferred"
	switch {
	case among(reason, provenReasons):
		expected = "proven"
	case among(reason, limitedReasons):
		expected = "unknown_due_to_limit"
	case among(reason, observedReasons):
		expected = "observed"
	}
	if level := get(proof, "level", nil); !isText(level, expected) {
		*issues = append(*issues, ResultIssue{"reason_proof_mismatch",
			itemID + ": " + pyRepr(reason) + " has proof " + pyRepr(level) + ", expected " + pyRepr(expected)})
	}
	if isText(reason, "time_limit") && !timedOut {
		*issues = append(*issues, ResultIssue{"reason_limit_mismatch", itemID + ": time_limit reason without a reached time limit"})
	}
	if isText(reason, "effort_limit") && !effortLimited {
		*issues = append(*issues, ResultIssue{"reason_limit_mismatch", itemID + ": effort_limit reason without a reached effort limit"})
	}
	if spec != nil {
		checkProvenReason(unpacked, reason, spec, context, itemID, issues)
	}
	checkObservations(proof, reason, itemID, issues)
}

func checkProvenReason(unpacked, reason interface{}, spec *itemSpec, context *checkContext, itemID string,
	issues *[]ResultIssue) {
	every := stringValues(rotationCodes)
	invalid := func(detail string) {
		*issues = append(*issues, ResultIssue{"invalid_proven_reason", itemID + ": " + detail})
	}
	switch {
	case isText(reason, "no_compatible_container_dimensions"):
		if fitsSome(spec, context, spec.allowedRotations) {
			invalid("at least one allowed orientation fits a container")
		}
		if fitsSome(spec, context, every) {
			invalid("fits at least one container once every rotation is tried, not only its own allowed set -- should be rotation_restricted")
		}
	case isText(reason, "rotation_restricted"):
		if fitsSome(spec, context, spec.allowedRotations) {
			invalid("already fits a container within its own allowed rotations")
		}
		if !fitsSome(spec, context, every) {
			invalid("does not fit any container even with every rotation tried -- should be no_compatible_container_dimensions")
		}
	case isText(reason, "payload_exceeded"):
		for _, value := range context.containers.values {
			if limit := value.(*containerSpec).maxPayload; limit == nil || spec.weight.Cmp(limit) <= 0 {
				invalid("its weight is within at least one container payload limit")
				break
			}
		}
	case isText(reason, "no_eligible_container"):
		for _, value := range context.containers.values {
			if len(spec.eligibleContainerTags) == 0 || spec.eligibleContainerTags.intersects(value.(*containerSpec).tags) {
				invalid("at least one container satisfies its eligible tags")
				break
			}
		}
	case isText(reason, "policy_rule"):
		checkPolicyCitation(unpacked, spec, context, itemID, issues)
	}
}

// checkPolicyCitation checks two claims: that some rule rules this item out of every
// container, and that the rule the result names is that rule.
func checkPolicyCitation(unpacked interface{}, spec *itemSpec, context *checkContext, itemID string,
	issues *[]ResultIssue) {
	var blocking []interface{}
	for _, rule := range context.rules {
		required := member(rule, "require_container_tag")
		if !spec.tags.holds(get(required, "item_tag", nil)) {
			continue
		}
		admitted := false
		for _, value := range context.containers.values {
			if value.(*containerSpec).tags.holds(get(required, "container_tag", nil)) {
				admitted = true
				break
			}
		}
		if !admitted {
			blocking = append(blocking, rule)
		}
	}
	if len(blocking) == 0 {
		*issues = append(*issues, ResultIssue{"invalid_proven_reason",
			itemID + ": no participating policy rule rules it out of every container"})
		return
	}
	cited := ""
	if details := get(unpacked, "details", nil); truthy(details) {
		var parts []string
		for _, detail := range items(details, "details") {
			parts = append(parts, text(detail, "details"))
		}
		cited = strings.Join(parts, " ")
	}
	for _, rule := range blocking {
		if strings.Contains(cited, pyStr(member(rule, "id"))+"@"+pyStr(member(rule, "version"))) {
			return
		}
	}
	*issues = append(*issues, ResultIssue{"invalid_proven_reason",
		itemID + ": cites " + pyRepr(cited) + ", which names no rule that actually blocks it"})
}

func checkObservations(proof, reason interface{}, itemID string, issues *[]ResultIssue) {
	observations, ok := get(proof, "observations", nil).([]interface{})
	if !ok || len(observations) == 0 {
		*issues = append(*issues, ResultIssue{"missing_reason_observations", itemID})
		return
	}
	observed := false
	for _, observation := range observations {
		if !isDict(observation) {
			*issues = append(*issues, ResultIssue{"malformed_reason_observation", itemID})
			continue
		}
		code, isString := get(observation, "code", nil).(string)
		count := intInstance(get(observation, "count", nil))
		if !isString || code == "" || count == nil || count.Cmp(bi(1)) < 0 ||
			!isList(get(observation, "details", nil)) {
			*issues = append(*issues, ResultIssue{"malformed_reason_observation", itemID})
			continue
		}
		if equals(code, reason) {
			observed = true
		}
	}
	if !observed {
		*issues = append(*issues, ResultIssue{"reason_observation_mismatch", itemID + ": " + pyStr(reason)})
	}
}
