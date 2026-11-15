// Package artifacts builds the portable operational artifact and exports it as JSON, CSV and a
// printable HTML work order (docs/OPERATIONAL-ARTIFACTS.md in the suite repository).
//
// An execution plan tells an operator what to do first; it does not say how big the boxes
// are, what the load weighs or which request and solver produced it. The artifact is the one
// document that can be drawn, printed and traced offline, and it is built so that it adds
// nothing a solver decided.
//
// It reads the request, the result and the optional loading orders -- the plan's own inputs
// -- and imports no solver, validator, renderer or clock. The plan inside it is exactly what
// execution.BuildPlan emits for the same inputs. Placements are found by the plan's placement
// reference, never by item_id, so there is one address format to drift. Every Packvium engine
// is held to byte-identical canonical output.
//
// Documents are taken and returned in the decoded JSON form of serialization.Pack and
// jsonvalue: nil, bool, string, int, int64, *big.Int, float64, []interface{} and
// map[string]interface{}. Parse and FromJSON read JSON text into that form.
package artifacts

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/packvium/packvium-go/execution"
	"github.com/packvium/packvium-go/internal/canonicaljson"
)

// Format is the artifact's own format tag.
const Format = "packvium-operational-artifact/v1"

// SuiteVersion is the suite version of this builder, the same string in every engine of one
// release. The engine's own name is deliberately not recorded: correct builders naming
// themselves would emit different documents.
const SuiteVersion = "1.3.0"

// SolverFields are the deterministic part of result.algorithm. duration_ms is wall-clock time
// and never enters an artifact.
var SolverFields = []string{"profile", "solver", "seed", "time_limit_reached", "effort_limit_reached"}

var (
	dimensionAxes = []string{"length", "width", "height"}
	positionAxes  = []string{"x", "y", "z"}
)

// The refusal codes, a closed set shared by every engine.
const (
	CodeInvalidRequest   = "invalid_request"
	CodeInvalidResult    = "invalid_result"
	CodeInvalidPlanInput = "invalid_plan_input"
	CodeMixedUnits       = "mixed_units"
	CodeNumberOutOfRange = canonicaljson.CodeNumberOutOfRange
	CodeInvalidString    = canonicaljson.CodeInvalidString
	CodeInvalidValue     = canonicaljson.CodeInvalidValue
	CodeUnknownFormat    = "unknown_format"
	CodeInvalidJSON      = "invalid_json"
)

// Error is something an artifact cannot carry. Code is one of the Code constants.
type Error struct {
	Code    string
	Message string
}

func (err *Error) Error() string { return err.Message }

func refuse(code, format string, arguments ...interface{}) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, arguments...)}
}

// Build builds the artifact for one validated result. request and result must be JSON
// objects (map[string]interface{}); they are taken as interface{} so that anything else is
// refused by code rather than by the compiler of one language. loadingOrders is passed
// straight to execution.BuildPlan. O(R + P) for a request of size R and P placements, plus the
// key sorts of the canonical check.
func Build(request, result interface{}, loadingOrders map[int][]int) (map[string]interface{}, error) {
	requestObject, ok := request.(map[string]interface{})
	if !ok {
		return nil, refuse(CodeInvalidRequest, "a request is a JSON object")
	}
	resultObject, ok := result.(map[string]interface{})
	if !ok {
		return nil, refuse(CodeInvalidResult, "a result is a JSON object")
	}
	if err := requireObjects(resultObject); err != nil {
		return nil, err
	}
	plan, err := execution.BuildPlan(requestObject, resultObject, loadingOrders)
	if err != nil {
		return nil, refuse(CodeInvalidPlanInput, "%s", err.Error())
	}
	containers, err := listOf(resultObject["containers"], "result.containers")
	if err != nil {
		return nil, err
	}
	lengthUnit, weightUnit, err := units(containers)
	if err != nil {
		return nil, err
	}
	// Built in the reference's order, so a result with two defects is refused for the same one
	// in every engine.
	provenance, err := buildProvenance(requestObject, resultObject)
	if err != nil {
		return nil, err
	}
	geometry, err := buildGeometry(containers)
	if err != nil {
		return nil, err
	}
	workOrder, err := buildWorkOrder(containers, plan, lengthUnit, weightUnit)
	if err != nil {
		return nil, err
	}
	artifact := map[string]interface{}{
		"format":        Format,
		"suite_version": SuiteVersion,
		"provenance":    provenance,
		"plan":          plan,
		"geometry":      map[string]interface{}{"containers": geometry},
		"work_order":    workOrder,
	}
	// Refused here rather than when someone serializes it: an artifact that exists must have
	// one spelling in every engine.
	if _, err := Canonical(artifact); err != nil {
		return nil, err
	}
	return artifact, nil
}

// Canonical is the artifact's RFC 8785 canonical form, the bytes the engines are compared on.
func Canonical(artifact map[string]interface{}) (string, error) {
	text, err := canonicaljson.Marshal(artifact)
	if refusal, ok := err.(*canonicaljson.Error); ok {
		return "", &Error{Code: refusal.Code, Message: refusal.Message}
	}
	return text, err
}

// ------------------------------------------------------------------------------ provenance

func buildProvenance(request, result map[string]interface{}) (map[string]interface{}, error) {
	solver, err := buildSolver(result["algorithm"])
	if err != nil {
		return nil, err
	}
	catalogs, err := listOf(result["catalog_versions_used"], "result.catalog_versions_used")
	if err != nil {
		return nil, err
	}
	for _, catalog := range catalogs {
		if err := checkCatalog(catalog); err != nil {
			return nil, err
		}
	}
	provenance := map[string]interface{}{
		// Embedded, not digested: only the request itself lets someone replay the artifact
		// without a lookup.
		"request":               request,
		"catalog_versions_used": catalogs,
		"solver":                nil,
		"replay":                replay(solver),
	}
	if solver != nil {
		provenance["solver"] = solver
	}
	return provenance, nil
}

// checkCatalog holds a catalog reference to the result schema's closed shape. The work order
// prints these fields, so a mistyped one would print differently in every engine.
func checkCatalog(catalog interface{}) error {
	identifier, err := field(catalog, "catalog_id", "catalog_versions_used[]")
	if err != nil {
		return err
	}
	if _, ok := identifier.(string); !ok {
		return refuse(CodeInvalidResult, "catalog_versions_used[].catalog_id is not a string")
	}
	for _, name := range []string{"version", "effective_at", "resolved_at"} {
		value, err := field(catalog, name, "catalog_versions_used[]")
		if err != nil {
			return err
		}
		if !isInteger(value) {
			return refuse(CodeInvalidResult, "catalog_versions_used[].%s is not an integer", name)
		}
	}
	return nil
}

func buildSolver(algorithm interface{}) (map[string]interface{}, error) {
	if algorithm == nil {
		return nil, nil
	}
	fields, ok := algorithm.(map[string]interface{})
	if !ok {
		return nil, refuse(CodeInvalidResult, "result.algorithm is not an object")
	}
	var missing []string
	for _, name := range SolverFields {
		if _, present := fields[name]; !present {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, refuse(CodeInvalidResult, "result.algorithm has no %s", strings.Join(missing, ", "))
	}
	for _, flag := range []string{"time_limit_reached", "effort_limit_reached"} {
		if _, ok := fields[flag].(bool); !ok {
			return nil, refuse(CodeInvalidResult, "result.algorithm.%s is not a boolean", flag)
		}
	}
	solver := make(map[string]interface{}, len(SolverFields))
	for _, name := range SolverFields {
		solver[name] = fields[name]
	}
	return solver, nil
}

// replay is "exact" only when a replay must reproduce the result. A search stopped by
// wall-clock time cannot be reproduced, and a result that does not say how it was solved
// cannot be promised to; claiming otherwise would be softening a proof level by another name.
func replay(solver map[string]interface{}) map[string]interface{} {
	if solver == nil {
		return map[string]interface{}{"level": "not_guaranteed", "because": "provenance.solver"}
	}
	if solver["time_limit_reached"] == true {
		return map[string]interface{}{"level": "not_guaranteed", "because": "provenance.solver.time_limit_reached"}
	}
	return map[string]interface{}{"level": "exact", "because": nil}
}

// -------------------------------------------------------------------------------- geometry

func buildGeometry(containers []interface{}) ([]interface{}, error) {
	geometry := make([]interface{}, len(containers))
	for index, container := range containers {
		entry, err := containerGeometry(index, container)
		if err != nil {
			return nil, err
		}
		geometry[index] = entry
	}
	return geometry, nil
}

func containerGeometry(index int, container interface{}) (map[string]interface{}, error) {
	inner, err := field(container, "inner_dimensions", fmt.Sprintf("containers[%d]", index))
	if err != nil {
		return nil, err
	}
	innerTicks, err := tickDimensions(inner)
	if err != nil {
		return nil, err
	}
	placements, err := placementsOf(index, container)
	if err != nil {
		return nil, err
	}
	described := make([]interface{}, len(placements))
	for position, placement := range placements {
		reference, err := placementReference(index, placement)
		if err != nil {
			return nil, err
		}
		dimensions, err := field(placement, "dimensions", fmt.Sprintf("containers[%d].placements[]", index))
		if err != nil {
			return nil, err
		}
		ticks, err := tickDimensions(dimensions)
		if err != nil {
			return nil, err
		}
		described[position] = map[string]interface{}{"placement": reference, "dimensions": ticks}
	}
	return map[string]interface{}{
		"container_index": index, "inner_dimensions": innerTicks, "placements": described,
	}, nil
}

// tickDimensions is lengths as decimal strings of ticks: the scene contract's spelling, and
// one no engine has to hold as a native number.
func tickDimensions(dimensions interface{}) (map[string]interface{}, error) {
	spelled := make(map[string]interface{}, len(dimensionAxes))
	for _, axis := range dimensionAxes {
		scalar, err := field(dimensions, axis, "dimensions")
		if err != nil {
			return nil, err
		}
		text, err := ticks(scalar)
		if err != nil {
			return nil, err
		}
		spelled[axis] = text
	}
	return spelled, nil
}

func ticks(scalar interface{}) (string, error) {
	value, err := field(scalar, "ticks", "exact scalar")
	if err != nil {
		return "", err
	}
	if !isInteger(value) {
		return "", refuse(CodeInvalidResult, "ticks %s is not an integer", repr(value))
	}
	// Written as a string, so the canonical writer never sees it as a number; the range is
	// checked here instead, because JavaScript has already rounded such a value while parsing.
	integer := bigInteger(value)
	if integer.CmpAbs(big.NewInt(canonicaljson.MaxExactMagnitude)) > 0 {
		return "", refuse(CodeNumberOutOfRange, "ticks %s is beyond what every engine holds exactly", integer)
	}
	return integer.String(), nil
}

// ------------------------------------------------------------------------------ work order

// units are the display units, read from the result rather than re-derived from request
// defaults: the result already rendered every value in them.
func units(containers []interface{}) (interface{}, interface{}, error) {
	if len(containers) == 0 {
		return nil, nil, nil
	}
	first := containers[0]
	inner, err := field(first, "inner_dimensions", "containers[0]")
	if err != nil {
		return nil, nil, err
	}
	length, err := field(inner, "length", "inner_dimensions")
	if err != nil {
		return nil, nil, err
	}
	lengthUnit, err := field(length, "unit", "length")
	if err != nil {
		return nil, nil, err
	}
	payload, err := field(first, "payload_weight", "containers[0]")
	if err != nil {
		return nil, nil, err
	}
	weightUnit, err := field(payload, "unit", "payload_weight")
	if err != nil {
		return nil, nil, err
	}
	return lengthUnit, weightUnit, nil
}

func buildWorkOrder(containers []interface{}, plan map[string]interface{}, lengthUnit, weightUnit interface{}) (map[string]interface{}, error) {
	planContainers := plan["containers"].([]interface{})
	described := make([]interface{}, len(containers))
	for index, container := range containers {
		entry, err := workOrderContainer(index, container, planContainers[index].(map[string]interface{}), lengthUnit, weightUnit)
		if err != nil {
			return nil, err
		}
		described[index] = entry
	}
	return map[string]interface{}{
		"length_unit": lengthUnit, "weight_unit": weightUnit, "containers": described,
	}, nil
}

func workOrderContainer(index int, container interface{}, planContainer map[string]interface{},
	lengthUnit, weightUnit interface{}) (map[string]interface{}, error) {
	byReference, err := placementsByReference(index, container)
	if err != nil {
		return nil, err
	}
	steps := planContainer["steps"].([]interface{})
	lines := make([]interface{}, len(steps))
	// One line per plan step, in plan step order: the order is the plan's, looked up, never
	// derived a second time.
	for position, entry := range steps {
		line, err := workOrderLine(entry.(map[string]interface{}), byReference, lengthUnit)
		if err != nil {
			return nil, err
		}
		lines[position] = line
	}
	where := fmt.Sprintf("containers[%d]", index)
	payload, err := renderedField(container, "payload_weight", where, weightUnit)
	if err != nil {
		return nil, err
	}
	gross, err := renderedField(container, "gross_weight", where, weightUnit)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"container_index": index,
		"container_type":  container.(map[string]interface{})["container_type"],
		"payload_weight":  payload,
		"gross_weight":    gross,
		"lines":           lines,
	}, nil
}

func workOrderLine(step map[string]interface{}, byReference map[string]interface{}, lengthUnit interface{}) (map[string]interface{}, error) {
	reference := step["placement"].(map[string]interface{})
	placement := byReference[referenceKey(reference)]
	position, err := field(placement, "position", "placement")
	if err != nil {
		return nil, err
	}
	dimensions, err := field(placement, "dimensions", "placement")
	if err != nil {
		return nil, err
	}
	line := map[string]interface{}{"placement": reference}
	if sequence, numbered := step["sequence"]; numbered {
		line["sequence"] = sequence
	}
	if line["position"], err = renderedValues(position, positionAxes, "position", lengthUnit); err != nil {
		return nil, err
	}
	if line["dimensions"], err = renderedValues(dimensions, dimensionAxes, "dimensions", lengthUnit); err != nil {
		return nil, err
	}
	return line, nil
}

func renderedValues(values interface{}, axes []string, where string, unit interface{}) (map[string]interface{}, error) {
	rendered := make(map[string]interface{}, len(axes))
	for _, axis := range axes {
		value, err := renderedField(values, axis, where, unit)
		if err != nil {
			return nil, err
		}
		rendered[axis] = value
	}
	return rendered, nil
}

func renderedField(object interface{}, name, where string, unit interface{}) (string, error) {
	scalar, err := field(object, name, where)
	if err != nil {
		return "", err
	}
	return renderedValue(scalar, unit)
}

// renderedValue is the result's rendered value, copied and never re-rendered.
func renderedValue(scalar interface{}, unit interface{}) (string, error) {
	found, err := field(scalar, "unit", "exact scalar")
	if err != nil {
		return "", err
	}
	if !sameValue(found, unit) {
		return "", refuse(CodeMixedUnits, "a value in %s where the result uses %s", repr(found), repr(unit))
	}
	value, err := field(scalar, "value", "exact scalar")
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", refuse(CodeInvalidResult, "value %s is not a string", repr(value))
	}
	return text, nil
}

func placementsByReference(index int, container interface{}) (map[string]interface{}, error) {
	placements, err := placementsOf(index, container)
	if err != nil {
		return nil, err
	}
	found := make(map[string]interface{}, len(placements))
	for _, placement := range placements {
		reference, err := placementReference(index, placement)
		if err != nil {
			return nil, err
		}
		found[referenceKey(reference)] = placement
	}
	if len(found) != len(placements) {
		return nil, refuse(CodeInvalidResult, "two placements in container %d share an origin, type and orientation", index)
	}
	return found, nil
}

func placementReference(index int, placement interface{}) (map[string]interface{}, error) {
	reference, err := execution.PlacementReference(index, placement)
	if err != nil {
		// Unreachable after the plan was built from the same placements.
		return nil, refuse(CodeInvalidPlanInput, "%s", err.Error())
	}
	return reference, nil
}

// referenceKey identifies a placement reference by item type, orientation and tick position,
// with the reference's equality: 1, 1.0 and true are one key, "1" another.
func referenceKey(reference map[string]interface{}) string {
	ticks := reference["position_ticks"].(map[string]interface{})
	parts := []interface{}{reference["item_type"], reference["orientation"], ticks["x"], ticks["y"], ticks["z"]}
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(keyPart(part))
		builder.WriteByte(0)
	}
	return builder.String()
}

func keyPart(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return fmt.Sprintf("s%d:%s", len(typed), typed)
	case nil:
		return "n"
	case bool, int, int64, *big.Int:
		return "i" + bigInteger(typed).String()
	case float64:
		if whole, exact := new(big.Float).SetFloat64(typed).Int(nil); exact == big.Exact {
			return "i" + whole.String()
		}
		return fmt.Sprintf("f%b", typed)
	}
	return fmt.Sprintf("o%#v", value)
}

// --------------------------------------------------------------------------------- helpers

// requireObjects checks that every list entry the artifact and its plan read is an object,
// before either reads one, so a malformed result is refused by name in every engine instead
// of failing wherever each language first touches it.
func requireObjects(result map[string]interface{}) error {
	for _, name := range []string{"containers", "unpacked_items", "alternatives", "catalog_versions_used"} {
		entries, err := listOf(result[name], "result."+name)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if _, ok := entry.(map[string]interface{}); !ok {
				return refuse(CodeInvalidResult, "result.%s holds a non-object entry", name)
			}
		}
	}
	containers, _ := listOf(result["containers"], "result.containers")
	for index, container := range containers {
		placements, err := placementsOf(index, container)
		if err != nil {
			return err
		}
		for _, placement := range placements {
			if _, ok := placement.(map[string]interface{}); !ok {
				return refuse(CodeInvalidResult, "containers[%d].placements holds a non-object entry", index)
			}
		}
	}
	return nil
}

func placementsOf(index int, container interface{}) ([]interface{}, error) {
	return listOf(container.(map[string]interface{})["placements"], fmt.Sprintf("containers[%d].placements", index))
}

// listOf is the reference's list of an optional field: absent or null is empty.
func listOf(value interface{}, where string) ([]interface{}, error) {
	if value == nil {
		return []interface{}{}, nil
	}
	list, ok := value.([]interface{})
	if !ok {
		return nil, refuse(CodeInvalidResult, "%s is not a list", where)
	}
	return append([]interface{}(nil), list...), nil
}

func field(object interface{}, name, where string) (interface{}, error) {
	fields, ok := object.(map[string]interface{})
	if !ok {
		return nil, refuse(CodeInvalidResult, "%s has no %s", where, name)
	}
	value, present := fields[name]
	if !present {
		return nil, refuse(CodeInvalidResult, "%s has no %s", where, name)
	}
	return value, nil
}

// isInteger is a JSON integer; a boolean is not one, as in the reference.
func isInteger(value interface{}) bool {
	switch value.(type) {
	case int, int64, *big.Int:
		return true
	}
	return false
}

func bigInteger(value interface{}) *big.Int {
	switch typed := value.(type) {
	case int:
		return big.NewInt(int64(typed))
	case int64:
		return big.NewInt(typed)
	case *big.Int:
		return typed
	case bool:
		if typed {
			return big.NewInt(1)
		}
	}
	return big.NewInt(0)
}

// sameValue is the reference's == between two decoded scalars, for the unit comparison.
func sameValue(a, b interface{}) bool {
	switch left := a.(type) {
	case string:
		right, ok := b.(string)
		return ok && left == right
	case nil:
		return b == nil
	}
	_, aComposite := a.(map[string]interface{})
	_, bComposite := b.(map[string]interface{})
	_, aList := a.([]interface{})
	_, bList := b.([]interface{})
	if aComposite || bComposite || aList || bList {
		left, errLeft := canonicaljson.Marshal(a)
		right, errRight := canonicaljson.Marshal(b)
		return errLeft == nil && errRight == nil && left == right
	}
	return keyPart(a) == keyPart(b)
}
