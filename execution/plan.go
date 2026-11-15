// Package execution derives an execution plan from an already validated packing result.
//
// A packing result answers what goes where; an operator needs what to do first, and why
// this carton. This package turns the first into the second and is built so that it cannot
// do anything else: it imports no solver and no validator, holds no registry and reads no
// clock. Everything it emits is a function of the request and result it was handed, so the
// same pair yields the same plan forever, which is the only reason a plan can be printed,
// signed and audited.
//
// Authoritative facts and presentation text are separated in the output, not just in the
// prose. Anything the solver or validator decided appears under "facts"; anything a human
// reads appears under "presentation", and every entry there names the fields it was derived
// from in "cites". A downstream system that only ever reads "facts" loses nothing it is
// entitled to rely on.
//
// A placement is referenced by what the cross-language contract promises, not by its id.
// The cross-language projection drops "item_id" and the container's "id" as instance
// counts, and four engines need not number instances alike, so citing them would let two
// correct systems disagree about which box a label names. The reference is built from fields
// the projection keeps, and reads a position's exact "ticks" rather than its rendered
// "value".
//
// Results are taken in the decoded JSON form: what serialization.Pack returns, or a result
// document decoded with integers kept exact. Values the plan carries through unchanged
// (feasibility, optimality, status strings) are shared with the result, not copied.
//
// Operator locks, which re-solve a request with chosen placements pinned, are in package
// execution/locks. They call the engine; this package does not, and does not import them.
package execution

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/packvium/packvium-go/internal/canonicaljson"
	"github.com/packvium/packvium-go/internal/jsonvalue"
)

// Format is the plan's own format tag. It is not the packing schema's version and does not
// move with it: a result can gain fields without changing what a plan says.
const Format = "packvium-execution-plan/v1"

// unnamedAxis is how a score index is named. What an index means is a property of the
// request's objective, which the adapter does not know, and naming it would be inventing
// meaning.
const unnamedAxis = "unnamed objective axis"

// PlanError reports a result the adapter cannot describe, or a plan with no canonical
// spelling. Code is empty for the adapter's own refusals; for CanonicalPlanJSON's it is the
// RFC 8785 rule the plan broke: number_out_of_range, invalid_string or invalid_value.
type PlanError struct {
	Code    string
	Message string
}

func (err *PlanError) Error() string { return err.Message }

func planError(format string, arguments ...interface{}) error {
	return &PlanError{Message: fmt.Sprintf(format, arguments...)}
}

// PlacementReference is a reference two languages agree on, for one placement in one
// container. containerIndex is the container's position in the result, not its id, and the
// position is read from "ticks", never from "value".
func PlacementReference(containerIndex int, placement interface{}) (map[string]interface{}, error) {
	fields, ok := placement.(map[string]interface{})
	if !ok {
		return nil, missingField(fmt.Sprintf("%T is not an object", placement))
	}
	// Checked in the reference's order, so the same malformed placement names the same field.
	position, present := fields["position"]
	if !present {
		return nil, missingField("'position'")
	}
	reference := map[string]interface{}{"container_index": containerIndex}
	for _, key := range []string{"item_type", "orientation"} {
		value, present := fields[key]
		if !present {
			return nil, missingField("'" + key + "'")
		}
		reference[key] = value
	}
	ticks, err := positionTicks(position)
	if err != nil {
		return nil, err
	}
	reference["position_ticks"] = ticks
	return reference, nil
}

func positionTicks(position interface{}) (map[string]interface{}, error) {
	axes, ok := position.(map[string]interface{})
	if !ok {
		return nil, missingField(fmt.Sprintf("position %T is not an object", position))
	}
	ticks := make(map[string]interface{}, 3)
	for _, axis := range []string{"x", "y", "z"} {
		scalar, ok := axes[axis].(map[string]interface{})
		if !ok {
			return nil, missingField("'" + axis + "'")
		}
		value, present := scalar["ticks"]
		if !present {
			return nil, missingField("'ticks'")
		}
		integer, err := integerOf(value)
		if err != nil {
			return nil, err
		}
		ticks[axis] = integer
	}
	return ticks, nil
}

func missingField(detail string) error {
	return planError("placement is missing a field the reference is built from: %s", detail)
}

// BuildPlan derives the execution plan for one validated result.
//
// loadingOrders maps a container's index to the engine-computed order its placements should
// be loaded in. It is optional and injected because the order is a statement about physics
// that this adapter must not make for itself; see steps. request is accepted for the
// contract's signature and read for nothing today. O(p log p + a·s) for p placements and a
// alternatives with s score entries each.
func BuildPlan(request, result map[string]interface{}, loadingOrders map[int][]int) (map[string]interface{}, error) {
	if result["status"] == nil {
		return nil, planError("a result without a status is not a validated result")
	}
	containers, err := listOf(result["containers"], "containers")
	if err != nil {
		return nil, err
	}
	winnerScore, err := listOf(result["score"], "score")
	if err != nil {
		return nil, err
	}
	planContainers, err := buildContainers(containers, loadingOrders)
	if err != nil {
		return nil, err
	}
	unplaced, err := buildUnplaced(result["unpacked_items"])
	if err != nil {
		return nil, err
	}
	alternatives, err := buildAlternatives(winnerScore, result["alternatives"])
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"format":    Format,
		"objective": result["objective"],
		"facts": map[string]interface{}{
			"status": result["status"], "score": winnerScore, "feasibility": result["feasibility"],
			"optimality": result["optimality"], "container_count": len(containers),
		},
		"containers": planContainers,
		// Often empty, and not for one reason: the fast profile runs a single solver, the
		// start loop stops once the grid lattice packs everything, only one start
		// completed, or `alternatives: 1` -- the cap counts the winner. An empty list is
		// well-formed and is never an error.
		"alternatives": alternatives,
		"unplaced":     unplaced,
	}, nil
}

func buildContainers(containers []interface{}, loadingOrders map[int][]int) ([]interface{}, error) {
	planned := make([]interface{}, len(containers))
	for index, entry := range containers {
		container, ok := entry.(map[string]interface{})
		if !ok {
			return nil, planError("container %d is %T, not an object", index, entry)
		}
		placements, err := listOf(container["placements"], "placements")
		if err != nil {
			return nil, err
		}
		order, injected := loadingOrders[index]
		sequence, err := steps(index, placements, order, injected)
		if err != nil {
			return nil, err
		}
		sequence["container_index"] = index
		sequence["facts"] = map[string]interface{}{
			"container_type": container["container_type"], "placement_count": len(placements),
			"volume_utilization": container["volume_utilization"],
		}
		planned[index] = sequence
	}
	return planned, nil
}

// steps is the operator sequence for one container, or an honest absence of one.
//
// The engines compute a loading order under support and accessibility rules, from domain
// objects this adapter never sees, so it is injected: a caller who has the order passes it,
// and one who does not gets no order at all. Falling back to the order placements happen to
// appear in would present an artifact of how the solver walked its candidates as if it were
// a safe order to lift boxes in.
func steps(containerIndex int, placements []interface{}, order []int, injected bool) (map[string]interface{}, error) {
	if !injected {
		listed := make([]interface{}, len(placements))
		for index, placement := range placements {
			reference, err := PlacementReference(containerIndex, placement)
			if err != nil {
				return nil, err
			}
			listed[index] = map[string]interface{}{"placement": reference}
		}
		return map[string]interface{}{"order": "unavailable", "steps": listed}, nil
	}
	if !isPermutation(order, len(placements)) {
		return nil, planError("loading order for container %d is not a permutation of its %d placements",
			containerIndex, len(placements))
	}
	numbered := make([]interface{}, len(order))
	for position, index := range order {
		reference, err := PlacementReference(containerIndex, placements[index])
		if err != nil {
			return nil, err
		}
		numbered[position] = map[string]interface{}{"sequence": position + 1, "placement": reference}
	}
	return map[string]interface{}{"order": "loading", "steps": numbered}, nil
}

// isPermutation is sorted(order) == range(count). O(n log n).
func isPermutation(order []int, count int) bool {
	if len(order) != count {
		return false
	}
	sorted := append([]int(nil), order...)
	sort.Ints(sorted)
	for index, value := range sorted {
		if value != index {
			return false
		}
	}
	return true
}

func buildUnplaced(value interface{}) ([]interface{}, error) {
	items, err := listOf(value, "unpacked_items")
	if err != nil {
		return nil, err
	}
	unplaced := make([]interface{}, len(items))
	for index, entry := range items {
		item, ok := entry.(map[string]interface{})
		if !ok {
			return nil, planError("unpacked item %d is %T, not an object", index, entry)
		}
		level, err := proofLevel(item["proof"])
		if err != nil {
			return nil, err
		}
		details, err := listOf(item["details"], "details")
		if err != nil {
			return nil, err
		}
		reason, err := text(item["reason"])
		if err != nil {
			return nil, err
		}
		levelText, err := text(level)
		if err != nil {
			return nil, err
		}
		unplaced[index] = map[string]interface{}{
			"facts": map[string]interface{}{
				"item_type": item["item_type"], "reason": item["reason"],
				// The proof's level is carried through unchanged. Softening "observed" into
				// "could not fit" would turn an honest limit into a false certainty.
				"proof_level": level, "details": details,
			},
			"presentation": map[string]interface{}{
				"summary": "Not packed: " + reason + " (" + levelText + ").",
				"cites":   []interface{}{"unpacked_items[].reason", "unpacked_items[].proof.level"},
			},
		}
	}
	return unplaced, nil
}

func proofLevel(proof interface{}) (interface{}, error) {
	if falsy(proof) {
		return nil, nil
	}
	fields, ok := proof.(map[string]interface{})
	if !ok {
		return nil, planError("an unpacked item's proof is %T, not an object", proof)
	}
	return fields["level"], nil
}

func buildAlternatives(winnerScore []interface{}, value interface{}) ([]interface{}, error) {
	entries, err := listOf(value, "alternatives")
	if err != nil {
		return nil, err
	}
	alternatives := make([]interface{}, len(entries))
	for index, entry := range entries {
		described, err := alternative(index, winnerScore, entry)
		if err != nil {
			return nil, err
		}
		alternatives[index] = described
	}
	return alternatives, nil
}

func alternative(index int, winnerScore []interface{}, entry interface{}) (map[string]interface{}, error) {
	fields, ok := entry.(map[string]interface{})
	if !ok {
		return nil, planError("alternative %d is %T, not an object", index, entry)
	}
	score, err := listOf(fields["score"], "score")
	if err != nil {
		return nil, err
	}
	difference, err := firstDifference(winnerScore, score)
	if err != nil {
		return nil, err
	}
	summary := "This option scored identically to the chosen one on every objective axis; " +
		"the score does not record why one was taken."
	facts := map[string]interface{}{
		"alternative_index": index, "score": score, "status": fields["status"], "first_difference": nil,
	}
	if difference != nil {
		facts["first_difference"] = difference
		summary = fmt.Sprintf("This option differs first at objective axis %d (%s): chosen %v, this %v.",
			difference["index"], unnamedAxis, difference["winner"], difference["alternative"])
	}
	return map[string]interface{}{
		"facts": facts,
		// Deliberately not "it lost because it is taller". The solver recorded a score, not a
		// cause; a sentence naming a cause would be a claim nothing in the result supports.
		"presentation": map[string]interface{}{
			"summary": summary, "cites": []interface{}{"score", "alternatives[].score"},
		},
	}, nil
}

// firstDifference is the first index at which two score vectors differ, and by how much.
//
// Never a blended number. The portfolio compared the vectors lexicographically, so the
// first differing index is the decision; summing or weighting them would replace a decision
// that was made with one that was not. O(s).
func firstDifference(winner, loser []interface{}) (map[string]interface{}, error) {
	for index := 0; index < len(winner) && index < len(loser); index++ {
		a, err := scoreEntry(winner[index])
		if err != nil {
			return nil, err
		}
		b, err := scoreEntry(loser[index])
		if err != nil {
			return nil, err
		}
		if a.Cmp(b) != 0 {
			return map[string]interface{}{
				"index": index, "winner": winner[index], "alternative": loser[index],
				"difference": compactInteger(new(big.Int).Sub(b, a)),
			}, nil
		}
	}
	if len(winner) != len(loser) {
		return nil, planError("score vectors of different length cannot be compared lexicographically")
	}
	return nil, nil
}

// scoreEntry is one score component as an exact integer; a score vector holds nothing else.
func scoreEntry(value interface{}) (*big.Int, error) {
	switch typed := value.(type) {
	case int:
		return big.NewInt(int64(typed)), nil
	case int64:
		return big.NewInt(typed), nil
	case *big.Int:
		return typed, nil
	}
	return nil, planError("a score component is %T, not an integer", value)
}

// integerOf is the reference's int() of a tick count: an integer as it is, a whole-number
// string parsed, a float truncated toward zero and a boolean as 0 or 1.
func integerOf(value interface{}) (interface{}, error) {
	switch typed := value.(type) {
	case int, int64, *big.Int:
		return typed, nil
	case bool:
		if typed {
			return int64(1), nil
		}
		return int64(0), nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, planError("cannot convert float %s to integer", jsonvalue.FloatRepr(typed))
		}
		whole, _ := new(big.Float).SetFloat64(math.Trunc(typed)).Int(nil)
		return compactInteger(whole), nil
	case string:
		parsed, ok := new(big.Int).SetString(strings.TrimSpace(typed), 10)
		if !ok {
			return nil, planError("invalid literal for int() with base 10: %s", strconv.Quote(typed))
		}
		return compactInteger(parsed), nil
	}
	return nil, missingField(fmt.Sprintf("ticks is %T, not a number", value))
}

// compactInteger keeps an integer as int64 when it fits, the form decoded JSON uses.
func compactInteger(value *big.Int) interface{} {
	if value.IsInt64() {
		return value.Int64()
	}
	return value
}

// listOf is the reference's list(value or ()): absent and empty values are an empty list.
func listOf(value interface{}, field string) ([]interface{}, error) {
	if list, ok := value.([]interface{}); ok {
		return append([]interface{}(nil), list...), nil
	}
	if falsy(value) {
		return []interface{}{}, nil
	}
	return nil, planError("%s is %T, not a list", field, value)
}

// falsy is the reference's truth test for the values a decoded document holds.
func falsy(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case bool:
		return !typed
	case int:
		return typed == 0
	case int64:
		return typed == 0
	case *big.Int:
		return typed.Sign() == 0
	case float64:
		return typed == 0
	case string:
		return typed == ""
	case []interface{}:
		return len(typed) == 0
	case map[string]interface{}:
		return len(typed) == 0
	}
	return false
}

// text is the reference's str() of a scalar a sentence interpolates.
func text(value interface{}) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "None", nil
	case string:
		return typed, nil
	case bool:
		if typed {
			return "True", nil
		}
		return "False", nil
	case int, int64, *big.Int:
		return fmt.Sprint(typed), nil
	case float64:
		switch {
		case math.IsNaN(typed):
			return "nan", nil
		case math.IsInf(typed, 1):
			return "inf", nil
		case math.IsInf(typed, -1):
			return "-inf", nil
		}
		return jsonvalue.FloatRepr(typed), nil
	}
	return "", planError("cannot render a value of type %T in a sentence", value)
}

// CanonicalPlanJSON is the one byte-comparable spelling of a plan. Cross-language equality is
// asserted on this string rather than on a parsed object, so key order and whitespace cannot
// make two identical plans look different. O(n log n) in the plan size, for the key sorts.
//
// The spelling is RFC 8785, shared with the operational artifact. Until 1.3.0 it was
// json.dumps(sort_keys=True), the same bytes for every plan an engine emits but not for a key
// outside the Basic Multilingual Plane or a float, where the engines disagreed. A plan
// carrying a number beyond 2^53 - 1 or a string that is not UTF-8 is refused.
func CanonicalPlanJSON(plan map[string]interface{}) (string, error) {
	text, err := canonicaljson.Marshal(plan)
	if refusal, ok := err.(*canonicaljson.Error); ok {
		return "", &PlanError{Code: refusal.Code, Message: refusal.Message}
	}
	return text, err
}
