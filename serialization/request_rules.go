package serialization

import (
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/canonicaljson"
	"github.com/packvium/packvium-go/units"
)

// checkRequest walks the request schema's rules over the raw JSON before any model is built
// and refuses the request with its first violation, in the order every engine follows:
// units, output, configuration, each item, item ids, each container, container ids. O(size of
// the request).
func checkRequest(request map[string]interface{}) error {
	unit, err := checkUnits(request["units"])
	if err != nil {
		return err
	}
	if err := checkOutput(request["output"]); err != nil {
		return err
	}
	if err := checkConfiguration(request["configuration"], unit); err != nil {
		return err
	}
	items, err := requiredList(request, "items", "")
	if err != nil {
		return err
	}
	for index, raw := range items {
		if err := checkItem(raw, domain.Pointer("items", strconv.Itoa(index)), unit); err != nil {
			return err
		}
	}
	if err := requireUniqueIDs(items, "items"); err != nil {
		return err
	}
	containers, err := requiredList(request, "containers", "")
	if err != nil {
		return err
	}
	for index, raw := range containers {
		if err := checkContainer(raw, domain.Pointer("containers", strconv.Itoa(index)), unit); err != nil {
			return err
		}
	}
	return requireUniqueIDs(containers, "containers")
}

func refusal(reason, field, detail string) error {
	return domain.NewInvalidRequestError(reason, field, detail)
}

// ------------------------------------------------------------------------------ primitives

type rule func(value interface{}, field string) error

func atLeast(minimum int64) rule {
	return func(value interface{}, field string) error {
		number, ok := canonicaljson.Integer(value)
		if !ok {
			return refuseNonInteger(value, field, minimum)
		}
		if number < minimum {
			return refusal("below_minimum", field, "must be at least "+strconv.FormatInt(minimum, 10))
		}
		return nil
	}
}

// refuseNonInteger says which way a whole number past 2^53 - 1 is out of range rather than
// calling it mistyped.
func refuseNonInteger(value interface{}, field string, minimum int64) error {
	var sign int
	whole := false
	switch typed := value.(type) {
	case int64:
		whole, sign = true, compareInt64(typed, minimum)
	case *big.Int:
		whole, sign = true, typed.Cmp(big.NewInt(minimum))
	case float64:
		if typed == math.Trunc(typed) && !math.IsInf(typed, 0) {
			whole, sign = true, compareFloat(typed, float64(minimum))
		}
	}
	if whole && sign < 0 {
		return refusal("below_minimum", field, "must be at least "+strconv.FormatInt(minimum, 10))
	}
	if whole {
		return refusal("above_maximum", field, "must be at most "+strconv.FormatInt(canonicaljson.MaxExactMagnitude, 10))
	}
	return refusal("wrong_type", field, "must be an integer")
}

func compareInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	}
	return 0
}

func compareFloat(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	}
	return 0
}

// ratio is a number from 0, bounded above by maximum unless it is nil.
func ratio(maximum *int64) rule {
	return func(value interface{}, field string) error {
		var number float64
		switch typed := value.(type) {
		case int64:
			number = float64(typed)
		case *big.Int:
			number = float64(typed.Sign()) * math.Inf(1)
		case float64:
			number = typed
		default:
			return refusal("wrong_type", field, "must be a number")
		}
		if number < 0 {
			return refusal("below_minimum", field, "must be at least 0")
		}
		if maximum != nil && number > float64(*maximum) {
			return refusal("above_maximum", field, "must be at most "+strconv.FormatInt(*maximum, 10))
		}
		return nil
	}
}

var one = int64(1)

func oneOf(allowed []string) rule {
	return func(value interface{}, field string) error {
		if text, ok := value.(string); ok {
			for _, candidate := range allowed {
				if text == candidate {
					return nil
				}
			}
		}
		spelled := make([]interface{}, len(allowed))
		for index, candidate := range allowed {
			spelled[index] = candidate
		}
		return refusal("not_allowed", field, "must be one of "+canonicaljson.Spelling(spelled))
	}
}

func objectAt(value interface{}, field string) (map[string]interface{}, error) {
	typed, ok := value.(map[string]interface{})
	if !ok {
		return nil, refusal("wrong_type", field, "must be an object")
	}
	return typed, nil
}

func listAt(value interface{}, field string) ([]interface{}, error) {
	typed, ok := value.([]interface{})
	if !ok {
		return nil, refusal("wrong_type", field, "must be a list")
	}
	return typed, nil
}

// measureKind is a length or a weight: its unit table and its parser.
type measureKind struct {
	knownUnit func(string) bool
	parse     func(value interface{}, unit string) error
}

var (
	lengthMeasure = measureKind{
		func(unit string) bool { _, err := units.LengthUnitTicks(unit); return err == nil },
		func(value interface{}, unit string) error { _, err := parseLength(value, unit); return err },
	}
	weightMeasure = measureKind{
		func(unit string) bool { _, err := units.WeightUnitTicks(unit); return err == nil },
		func(value interface{}, unit string) error { _, err := parseWeight(value, unit); return err },
	}
)

// measure is an integer, a string or {value, unit}, and never negative.
func measure(kind measureKind, unit string) rule {
	return func(value interface{}, field string) error {
		switch typed := value.(type) {
		case int64, *big.Int, string:
		case float64:
			if _, ok := canonicaljson.Integer(typed); !ok {
				return refusal("wrong_type", field, "must be a measure")
			}
		case map[string]interface{}:
			if _, present := typed["value"]; !present {
				return refusal("wrong_type", field, "must be a measure")
			}
			stated, present := typed["unit"]
			if !present {
				stated = unit
			}
			if text, ok := stated.(string); !ok || !kind.knownUnit(text) {
				return refusal("invalid_unit", field, "has an unknown unit "+canonicaljson.Spelling(stated))
			}
		default:
			return refusal("wrong_type", field, "must be a measure")
		}
		if err := kind.parse(value, unit); err != nil {
			if isNegative(err) {
				return refusal("negative_measure", field, "cannot be negative")
			}
			return refusal("wrong_type", field, "must be a measure")
		}
		return nil
	}
}

// ------------------------------------------------------------------------------ the rules

var configurationIntegers = []struct {
	name    string
	minimum int64
}{
	{"time_limit_ms", 1}, {"alternatives", 1}, {"max_containers", 1}, {"exact_item_limit", 1},
	{"multi_start_orders", 1}, {"max_candidates_per_item", 1}, {"max_candidate_points", 16},
	{"container_plan_beam_width", 1}, {"container_plan_node_limit", 1}, {"dimensional_weight_divisor", 1},
}

var effortLimits = []string{"max_candidates_evaluated", "max_placement_attempts", "max_search_nodes", "max_restarts"}

var solverProfiles = []string{"fast", "balanced", "quality", "exact_small"}

var objectives = []string{"default", "lowest_cost", "shipping_cost", "lowest_landed_cost", "open_dimension_height", "maximum_value",
	"maximum_volume"}

var accessDirections = []string{"+x", "-x", "+y", "-y", "+z", "-z"}

// The schema's other closed vocabularies, each refused as not_allowed at its own pointer.
var (
	solverNames         = []string{"grid", "extreme_points", "homogeneous_blocks", "layer", "maximal_spaces", "exact_small"}
	rotationCodes       = []string{"LWH", "LHW", "WLH", "WHL", "HLW", "HWL"}
	groundContactRules  = []string{"free", "covered", "single", "multiple"}
	shapeTypes          = []string{"rigid_cuboid", "convex_hull", "compressible"}
	lengthUnitCodes     = units.LengthUnitCodes()
	weightUnitCodes     = units.WeightUnitCodes()
	lengthUnitCodeRule  = oneOf(lengthUnitCodes)
	weightUnitCodeRule  = oneOf(weightUnitCodes)
	rotationCodesRule   = eachOneOf(rotationCodes)
	solverNamesRule     = eachOneOf(solverNames)
	groundContactRule   = oneOf(groundContactRules)
	shapeTypeRule       = oneOf(shapeTypes)
	accessDirectionRule = eachOneOf(accessDirections)
)

func eachOneOf(allowed []string) rule {
	member := oneOf(allowed)
	return func(raw interface{}, where string) error {
		values, err := listAt(raw, where)
		if err != nil {
			return err
		}
		for index, value := range values {
			if err := member(value, where+domain.Pointer(strconv.Itoa(index))); err != nil {
				return err
			}
		}
		return nil
	}
}

func checkOutput(raw interface{}) error {
	if raw == nil {
		return nil
	}
	output, err := objectAt(raw, "/output")
	if err != nil {
		return err
	}
	return checkFields(output, "/output", []fieldRule{{"length_unit", lengthUnitCodeRule}, {"weight_unit", weightUnitCodeRule}})
}

// configurationFields is every key the request schema's `configuration` declares; it sets
// `additionalProperties: false`.
var configurationFields = []string{"alternatives", "clearance", "container_plan_beam_width",
	"container_plan_node_limit", "dimensional_weight_divisor", "dimensional_weight_length_unit",
	"dimensional_weight_weight_unit", "effort_budget", "exact_item_limit", "max_candidate_points",
	"max_candidates_per_item", "max_containers", "minimum_support_ratio", "multi_start_orders",
	"objective", "require_placement_coordinates", "seed", "solver_profile", "solvers", "time_limit_ms"}

var sides = []string{"length", "width", "height"}

func checkUnits(raw interface{}) (string, error) {
	if raw == nil {
		return "mm", nil
	}
	block, err := objectAt(raw, "/units")
	if err != nil {
		return "", err
	}
	length := block["length"]
	if length == nil {
		return "mm", nil
	}
	if text, ok := length.(string); ok && lengthMeasure.knownUnit(text) {
		return text, nil
	}
	return "", refusal("invalid_unit", "/units/length", "has an unknown unit "+canonicaljson.Spelling(length))
}

func checkConfiguration(raw interface{}, unit string) error {
	if raw == nil {
		return nil
	}
	configuration, err := objectAt(raw, "/configuration")
	if err != nil {
		return err
	}
	where := "/configuration"
	if err := knownFields(configuration, where, configurationFields); err != nil {
		return err
	}
	if err := optional(configuration, "solver_profile", where, oneOf(solverProfiles)); err != nil {
		return err
	}
	if err := optional(configuration, "objective", where, oneOf(objectives)); err != nil {
		return err
	}
	if err := checkFields(configuration, where, []fieldRule{{"solvers", solverNamesRule},
		{"dimensional_weight_length_unit", lengthUnitCodeRule}, {"dimensional_weight_weight_unit", weightUnitCodeRule}}); err != nil {
		return err
	}
	for _, field := range configurationIntegers {
		if err := optional(configuration, field.name, where, atLeast(field.minimum)); err != nil {
			return err
		}
	}
	if err := optional(configuration, "minimum_support_ratio", where, ratio(&one)); err != nil {
		return err
	}
	if err := optional(configuration, "clearance", where, measure(lengthMeasure, unit)); err != nil {
		return err
	}
	effort := configuration["effort_budget"]
	if effort == nil {
		return nil
	}
	budget, err := objectAt(effort, "/configuration/effort_budget")
	if err != nil {
		return err
	}
	if err := knownFields(budget, "/configuration/effort_budget", effortLimits); err != nil {
		return err
	}
	for _, name := range effortLimits {
		if err := optional(budget, name, "/configuration/effort_budget", atLeast(1)); err != nil {
			return err
		}
	}
	return nil
}

type fieldRule struct {
	name  string
	check rule
}

func checkFields(record map[string]interface{}, where string, rules []fieldRule) error {
	for _, field := range rules {
		if err := optional(record, field.name, where, field.check); err != nil {
			return err
		}
	}
	return nil
}

func checkItem(raw interface{}, where, unit string) error {
	item, err := objectAt(raw, where)
	if err != nil {
		return err
	}
	if err := requiredString(item, "id", where); err != nil {
		return err
	}
	if err := optional(item, "quantity", where, atLeast(1)); err != nil {
		return err
	}
	sides, err := required(item, "dimensions", where)
	if err != nil {
		return err
	}
	if err := dimensions(sides, where+"/dimensions", unit); err != nil {
		return err
	}
	return checkFields(item, where, []fieldRule{
		{"weight", measure(weightMeasure, weightUnit)}, {"max_top_load", measure(weightMeasure, weightUnit)},
		{"nesting_height", measure(lengthMeasure, unit)},
		{"nesting_height", func(value interface{}, field string) error { return nestingBelowHeight(value, field, sides, unit) }},
		{"max_stacked_items", atLeast(1)},
		{"stop_index", atLeast(0)}, {"value", atLeast(0)},
		{"max_compression_pressure_kpa", atLeast(0)}, {"minimum_support_ratio", ratio(&one)},
		{"compression_ratio", ratio(nil)}, {"stacking_group", stackingGroupName},
		{"accepts_stacking_groups", stackingGroupNames}, {"allowed_rotations", rotationCodesRule},
		{"ground_contact_rule", groundContactRule}, {"shape_type", shapeTypeRule},
	})
}

// nestingBelowHeight holds a nested copy to sinking by less than its own height, or it would
// vanish into the one below; both measures have passed their own rules already. O(1).
func nestingBelowHeight(value interface{}, field string, sides interface{}, unit string) error {
	nesting, _ := parseLength(value, unit)
	height, _ := parseLength(sides.(map[string]interface{})["height"], unit)
	if nesting.Ticks() >= height.Ticks() {
		return refusal("invalid_value", field, "must be less than the item's height")
	}
	return nil
}

func stackingGroupName(value interface{}, field string) error {
	text, ok := value.(string)
	if !ok {
		return refusal("wrong_type", field, "must be a string")
	}
	if text == "" {
		return refusal("invalid_value", field, "must not be empty")
	}
	return nil
}

func stackingGroupNames(raw interface{}, where string) error {
	names, err := listAt(raw, where)
	if err != nil {
		return err
	}
	for index, name := range names {
		if err := stackingGroupName(name, where+domain.Pointer(strconv.Itoa(index))); err != nil {
			return err
		}
	}
	return nil
}

func checkContainer(raw interface{}, where, unit string) error {
	container, err := objectAt(raw, where)
	if err != nil {
		return err
	}
	if err := requiredString(container, "id", where); err != nil {
		return err
	}
	if err := optional(container, "quantity", where, atLeast(1)); err != nil {
		return err
	}
	inner, err := required(container, "inner_dimensions", where)
	if err != nil {
		return err
	}
	if err := dimensions(inner, where+"/inner_dimensions", unit); err != nil {
		return err
	}
	return checkFields(container, where, []fieldRule{
		{"outer_dimensions", func(value interface{}, field string) error { return dimensions(value, field, unit) }},
		{"tare_weight", measure(weightMeasure, weightUnit)}, {"max_payload", measure(weightMeasure, weightUnit)},
		{"max_stack_density", measure(weightMeasure, weightUnit)}, {"max_items", atLeast(1)},
		{"cost_minor", atLeast(0)}, {"void_fill_reserve_ratio", ratio(&one)},
		{"access_directions", accessDirectionRule},
		{"tag_limits", checkTagLimits}, {"rate_table", checkRateTable},
		{"obstacles", func(value interface{}, field string) error { return checkObstacles(value, field, unit) }},
		{"deformable_boundary", func(value interface{}, field string) error {
			return checkDeformableBoundary(container, value, field, where)
		}},
		{"made_to_measure", func(value interface{}, field string) error {
			return checkMadeToMeasure(container, value, field, where, unit)
		}},
	})
}

// bagExclusions are the container fields a flat bag refuses, in reporting order.
var bagExclusions = []string{"obstacles", "axles", "access_directions", "max_stack_density",
	"outer_dimensions", "void_fill_reserve_ratio", "made_to_measure"}

func checkDeformableBoundary(container map[string]interface{}, raw interface{}, field, where string) error {
	boundary, err := objectAt(raw, field)
	if err != nil {
		return err
	}
	kind, err := required(boundary, "kind", field)
	if err != nil {
		return err
	}
	if err := oneOf(domain.DeformableBoundaryKinds)(kind, field+domain.Pointer("kind")); err != nil {
		return err
	}
	if err := knownFields(boundary, field, []string{"kind"}); err != nil {
		return err
	}
	for _, name := range bagExclusions {
		if setBesideABag(name, container[name]) {
			return refusal("not_allowed", where+domain.Pointer(name), "cannot be combined with deformable_boundary")
		}
	}
	return nil
}

// setBesideABag reports whether a field is set in a way a bag refuses: an empty obstacle list
// and a zero reserve change nothing, so only they are let through. The field already passed
// its own rule.
func setBesideABag(name string, value interface{}) bool {
	switch name {
	case "obstacles":
		list, _ := value.([]interface{})
		return len(list) > 0
	case "void_fill_reserve_ratio":
		return positiveNumber(value)
	}
	return value != nil
}

func positiveNumber(value interface{}) bool {
	switch typed := value.(type) {
	case int64:
		return typed > 0
	case *big.Int:
		return typed.Sign() > 0
	case float64:
		return typed > 0
	}
	return false
}

// madeToMeasureFields is every key the request schema's made_to_measure declares; it sets
// additionalProperties: false.
var madeToMeasureFields = []string{"cost_per_square_metre_minor", "increment", "minimum", "outer_allowance",
	"tare_per_square_metre"}

// checkMadeToMeasure is the made-to-measure rules: shape top to bottom, then each
// axis, then the price. Run after every other container field, so an existing error always
// wins. O(1).
func checkMadeToMeasure(container map[string]interface{}, raw interface{}, field, where, unit string) error {
	machine, err := objectAt(raw, field)
	if err != nil {
		return err
	}
	if err := knownFields(machine, field, madeToMeasureFields); err != nil {
		return err
	}
	for _, name := range []string{"minimum", "increment"} {
		if _, err := required(machine, name, field); err != nil {
			return err
		}
	}
	box := func(value interface{}, at string) error { return dimensions(value, at, unit) }
	if err := checkFields(machine, field, []fieldRule{{"minimum", box}, {"increment", box}, {"outer_allowance", box},
		{"cost_per_square_metre_minor", atLeast(0)}, {"tare_per_square_metre", measure(weightMeasure, weightUnit)},
	}); err != nil {
		return err
	}
	if container["outer_dimensions"] != nil {
		return refusal("not_allowed", where+"/outer_dimensions", "cannot be combined with made_to_measure")
	}
	inner, minimum, increment := sideTicks(container["inner_dimensions"], unit), sideTicks(machine["minimum"], unit),
		sideTicks(machine["increment"], unit)
	for axis, side := range sides {
		switch {
		case minimum[axis] == 0:
			return refusal("invalid_value", field+domain.Pointer("minimum", side), "must be positive")
		case increment[axis] == 0:
			return refusal("invalid_value", field+domain.Pointer("increment", side), "must be positive")
		case minimum[axis] > inner[axis]:
			return refusal("invalid_value", field+domain.Pointer("minimum", side), "exceeds inner_dimensions")
		case (inner[axis]-minimum[axis])%increment[axis] != 0:
			return refusal("invalid_value", field+domain.Pointer("increment", side),
				"does not divide inner_dimensions minus minimum")
		}
	}
	price, _ := canonicaljson.Integer(machine["cost_per_square_metre_minor"])
	fee, _ := canonicaljson.Integer(container["cost_minor"])
	if domain.BoxCostMinorFor(price, fee, inner).Cmp(big.NewInt(canonicaljson.MaxExactMagnitude)) > 0 {
		return refusal("invalid_value", field+"/cost_per_square_metre_minor",
			"prices the largest box above "+strconv.FormatInt(canonicaljson.MaxExactMagnitude, 10))
	}
	return nil
}

// sideTicks reads dimensions that already passed their rule.
func sideTicks(raw interface{}, unit string) [3]int64 {
	box := raw.(map[string]interface{})
	var ticks [3]int64
	for axis, side := range sides {
		length, _ := parseLength(box[side], unit)
		ticks[axis] = length.Ticks()
	}
	return ticks
}

func dimensions(raw interface{}, where, unit string) error {
	box, err := objectAt(raw, where)
	if err != nil {
		return err
	}
	for _, side := range sides {
		value, err := required(box, side, where)
		if err != nil {
			return err
		}
		if err := measure(lengthMeasure, unit)(value, where+domain.Pointer(side)); err != nil {
			return err
		}
	}
	return nil
}

// knownFields refuses a key the schema does not name in a closed object, never ignoring it.
// The first unknown key in code point order is named, the order every engine can share; Go
// orders valid UTF-8 by bytes, which is code point order. O(k log k) for k keys.
func knownFields(record map[string]interface{}, where string, known []string) error {
	var unknown []string
	for name := range record {
		if !containsString(known, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return refusal("not_allowed", where+domain.Pointer(unknown[0]), "is not a known field")
}

// checkTagLimits walks tags in code point order, as every engine does: a JavaScript object
// puts integer-like keys first, so insertion order would name a different bad tag there.
func checkTagLimits(raw interface{}, where string) error {
	limits, err := objectAt(raw, where)
	if err != nil {
		return err
	}
	tags := make([]string, 0, len(limits))
	for tag := range limits {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		if err := atLeast(1)(limits[tag], where+domain.Pointer(tag)); err != nil {
			return err
		}
	}
	return nil
}

func checkRateTable(raw interface{}, where string) error {
	table, err := objectAt(raw, where)
	if err != nil {
		return err
	}
	for _, list := range []struct {
		name    string
		minimum int64
	}{{"weight_brackets_g", 1}, {"prices_minor", 0}} {
		values := table[list.name]
		if values == nil {
			continue
		}
		entries, err := listAt(values, where+domain.Pointer(list.name))
		if err != nil {
			return err
		}
		for index, value := range entries {
			if err := atLeast(list.minimum)(value, where+domain.Pointer(list.name, strconv.Itoa(index))); err != nil {
				return err
			}
		}
	}
	return checkFields(table, where, []fieldRule{
		{"minimum_charge_minor", atLeast(0)}, {"fuel_surcharge_permille", atLeast(0)},
	})
}

func checkObstacles(raw interface{}, where, unit string) error {
	entries, err := listAt(raw, where)
	if err != nil {
		return err
	}
	for index, entry := range entries {
		at := where + domain.Pointer(strconv.Itoa(index))
		obstacle, err := objectAt(entry, at)
		if err != nil {
			return err
		}
		if origin := obstacle["origin"]; origin != nil {
			point, err := objectAt(origin, at+"/origin")
			if err != nil {
				return err
			}
			for _, axis := range []string{"x", "y", "z"} {
				if err := optional(point, axis, at+"/origin", measure(lengthMeasure, unit)); err != nil {
					return err
				}
			}
		}
		box, err := required(obstacle, "dimensions", at)
		if err != nil {
			return err
		}
		if err := dimensions(box, at+"/dimensions", unit); err != nil {
			return err
		}
	}
	return nil
}

// ------------------------------------------------------------------------------ plumbing

// optional is an optional field: absent and null are the default; anything else must pass.
func optional(record map[string]interface{}, name, where string, check rule) error {
	value := record[name]
	if value == nil {
		return nil
	}
	return check(value, where+domain.Pointer(name))
}

func required(record map[string]interface{}, name, where string) (interface{}, error) {
	value := record[name]
	if value == nil {
		return nil, refusal("missing_field", where+domain.Pointer(name), "is required")
	}
	return value, nil
}

func requiredList(record map[string]interface{}, name, where string) ([]interface{}, error) {
	value, err := required(record, name, where)
	if err != nil {
		return nil, err
	}
	return listAt(value, where+domain.Pointer(name))
}

func requiredString(record map[string]interface{}, name, where string) error {
	value, err := required(record, name, where)
	if err != nil {
		return err
	}
	if _, ok := value.(string); !ok {
		return refusal("wrong_type", where+domain.Pointer(name), "must be a string")
	}
	return nil
}

// requireUniqueIDs runs once every entry is well formed, so the later of two equal ids is
// the one named.
func requireUniqueIDs(entries []interface{}, key string) error {
	seen := map[string]bool{}
	for index, entry := range entries {
		id := entry.(map[string]interface{})["id"].(string)
		if seen[id] {
			return refusal("duplicate_id", domain.Pointer(key, strconv.Itoa(index), "id"),
				"repeats the id "+canonicaljson.Spelling(id))
		}
		seen[id] = true
	}
	return nil
}

func isNegative(err error) bool { return strings.Contains(err.Error(), "cannot be negative") }
