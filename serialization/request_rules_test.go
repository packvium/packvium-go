package serialization

import (
	"strings"
	"testing"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
)

// The structured request errors, after the reference's tests/test_request_errors.py.

func validRequest() map[string]interface{} {
	return map[string]interface{}{
		"items": []interface{}{map[string]interface{}{"id": "cube", "quantity": int64(2), "weight": "1000",
			"dimensions": map[string]interface{}{"length": "100", "width": "100", "height": "100"}}},
		"containers": []interface{}{map[string]interface{}{"id": "box", "quantity": int64(2),
			"inner_dimensions": map[string]interface{}{"length": "200", "width": "100", "height": "200"}}},
		"configuration": map[string]interface{}{"solver_profile": "fast"},
	}
}

func refusalOf(t *testing.T, edit func(map[string]interface{})) *domain.InvalidRequestError {
	t.Helper()
	request := validRequest()
	edit(request)
	_, err := Pack(request)
	refusal, ok := err.(*domain.InvalidRequestError)
	if !ok {
		t.Fatalf("want a request error, got %#v", err)
	}
	return refusal
}

func itemSide(side string, value interface{}) func(map[string]interface{}) {
	return func(r map[string]interface{}) { firstItem(r)["dimensions"].(map[string]interface{})[side] = value }
}

func setItem(key string, value interface{}) func(map[string]interface{}) {
	return func(r map[string]interface{}) { firstItem(r)[key] = value }
}

func setContainer(key string, value interface{}) func(map[string]interface{}) {
	return func(r map[string]interface{}) { firstContainer(r)[key] = value }
}

func TestAMalformedRequestNamesTheValueAndTheRule(t *testing.T) {
	cube := func(r map[string]interface{}) map[string]interface{} { return firstItem(r) }
	cases := []struct {
		edit                  func(map[string]interface{})
		reason, field, detail string
	}{
		{setItem("quantity", int64(0)), "below_minimum", "/items/0/quantity", "must be at least 1"},
		{setItem("quantity", "2"), "wrong_type", "/items/0/quantity", "must be an integer"},
		{setItem("quantity", int64(1)<<53), "above_maximum", "/items/0/quantity", "must be at most 9007199254740991"},
		{setItem("quantity", -int64(1)<<60), "below_minimum", "/items/0/quantity", "must be at least 1"},
		{itemSide("width", "-1"), "negative_measure", "/items/0/dimensions/width", "cannot be negative"},
		{itemSide("width", map[string]interface{}{"value": "1", "unit": "furlong"}), "invalid_unit",
			"/items/0/dimensions/width", `has an unknown unit "furlong"`},
		{itemSide("width", map[string]interface{}{"unit": "mm"}), "wrong_type", "/items/0/dimensions/width", "must be a measure"},
		{itemSide("width", 1.5), "wrong_type", "/items/0/dimensions/width", "must be a measure"},
		{func(r map[string]interface{}) { delete(cube(r)["dimensions"].(map[string]interface{}), "height") },
			"missing_field", "/items/0/dimensions/height", "is required"},
		{func(r map[string]interface{}) { r["items"] = append(r["items"].([]interface{}), copyMap(cube(r))) },
			"duplicate_id", "/items/1/id", `repeats the id "cube"`},
		{setItem("minimum_support_ratio", int64(2)), "above_maximum", "/items/0/minimum_support_ratio", "must be at most 1"},
		{setItem("minimum_support_ratio", "x"), "wrong_type", "/items/0/minimum_support_ratio", "must be a number"},
		{setItem("compression_ratio", int64(-1)), "below_minimum", "/items/0/compression_ratio", "must be at least 0"},
		{setContainer("tag_limits", map[string]interface{}{"a/b~c": int64(0)}), "below_minimum",
			"/containers/0/tag_limits/a~1b~0c", "must be at least 1"},
		{setContainer("rate_table", map[string]interface{}{"prices_minor": []interface{}{int64(1), int64(-2)}}),
			"below_minimum", "/containers/0/rate_table/prices_minor/1", "must be at least 0"},
		{func(r map[string]interface{}) { r["units"] = map[string]interface{}{"length": int64(5)} },
			"invalid_unit", "/units/length", "has an unknown unit 5"},
		{func(r map[string]interface{}) { r["items"] = map[string]interface{}{"cube": int64(1)} },
			"wrong_type", "/items", "must be a list"},
		{setItem("id", int64(7)), "wrong_type", "/items/0/id", "must be a string"},
		{itemSide("width", "ten"), "wrong_type", "/items/0/dimensions/width", "must be a measure"},
		{func(r map[string]interface{}) {
			r["configuration"].(map[string]interface{})["solver_profile"] = "fastest"
		}, "not_allowed", "/configuration/solver_profile", `must be one of ["fast","balanced","quality","exact_small"]`},
		{func(r map[string]interface{}) {
			r["configuration"].(map[string]interface{})["effort_budget"] = map[string]interface{}{"max_restarts": int64(0)}
		}, "below_minimum", "/configuration/effort_budget/max_restarts", "must be at least 1"},
	}
	for _, testCase := range cases {
		refusal := refusalOf(t, testCase.edit)
		if refusal.Code != "invalid_request" || refusal.Reason != testCase.reason || refusal.Field != testCase.field ||
			refusal.Detail != testCase.detail {
			t.Errorf("%s: %#v", testCase.field, refusal)
		}
		if want := "invalid_request: " + testCase.field + ": " + testCase.detail; refusal.Error() != want {
			t.Errorf("%s: message %q", testCase.field, refusal.Error())
		}
	}
}

func copyMap(source map[string]interface{}) map[string]interface{} {
	copied := map[string]interface{}{}
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

func TestARequestThatIsNotAnObjectIsRefusedAsAWhole(t *testing.T) {
	_, err := Pack([]interface{}{validRequest()})
	refusal, ok := err.(*domain.InvalidRequestError)
	if !ok || refusal.Field != "" || refusal.Error() != "invalid_request: must be an object" {
		t.Errorf("%#v", err)
	}
}

func TestWhatTheRulesDoNotNameStillArrivesAsARequestError(t *testing.T) {
	refusal := refusalOf(t, setItem("shape_type", "blob"))
	if refusal.Code != "invalid_request" || refusal.Reason != "invalid_value" || refusal.Field != "" {
		t.Errorf("%#v", refusal)
	}
}

func TestNullOptionalsAreAbsentAndAValidRequestPasses(t *testing.T) {
	request := validRequest()
	for _, key := range []string{"max_stacked_items", "stop_index", "weight"} {
		firstItem(request)[key] = nil
	}
	request["catalog_versions_used"] = nil
	if _, err := Pack(request); err != nil {
		t.Error(err)
	}
}

func TestAPointerEscapesTildeAndSlash(t *testing.T) {
	if got := domain.Pointer("containers", "0", "tag_limits", "a/b~c"); got != "/containers/0/tag_limits/a~1b~0c" {
		t.Error(got)
	}
}

func TestTheTypedAPIKeepsItsOwnGuards(t *testing.T) {
	zero := int64(0)
	cases := []struct {
		edit    func(*config.Options)
		message string
	}{
		{func(o *config.Options) { o.MaxContainers = &zero }, "max_containers must be at least 1"},
		{func(o *config.Options) { o.MaxCandidatePoints = 15 }, "max_candidate_points must be at least 16"},
		{func(o *config.Options) { o.MinimumSupportRatio = 2 }, "minimum_support_ratio must be between 0 and 1"},
		{func(o *config.Options) { o.DimensionalWeightDivisor = &zero }, "dimensional_weight_divisor must be positive"},
		{func(o *config.Options) { o.TimeLimitMS = 0 }, "positive configuration values required"},
		{func(o *config.Options) { o.EffortBudget = &config.EffortBudget{MaxSearchNodes: &zero} },
			"effort_budget.max_search_nodes must be at least 1"},
	}
	for _, testCase := range cases {
		options := config.Default(config.Balanced)
		testCase.edit(&options)
		if _, err := config.New(options); err == nil || !strings.Contains(err.Error(), testCase.message) {
			t.Errorf("%s: %v", testCase.message, err)
		}
	}
	max := int64(0)
	if _, err := domain.NewContainer(domain.ContainerSpec{ID: "box", InnerDimensions: domain.MustDimensions(1, 1, 1),
		MaxItems: &max}); err == nil || err.Error() != "container max_items must be at least 1" {
		t.Errorf("max_items: %v", err)
	}
}

func withObstacle(origin map[string]interface{}, id interface{}) func(map[string]interface{}) {
	return setContainer("obstacles", []interface{}{map[string]interface{}{"id": id, "origin": origin,
		"dimensions": map[string]interface{}{"length": "10", "width": "10", "height": "10"}}})
}

func TestAnObstacleWithANullIDIsPackedAsTheReferenceDoes(t *testing.T) {
	request := validRequest()
	withObstacle(map[string]interface{}{"x": "190"}, nil)(request)
	if _, err := Pack(request); err != nil {
		t.Error(err)
	}
}

func TestRefusalsTheRuleTableLeavesToTheModelBuilders(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(map[string]interface{})
		message string
	}{
		{"null obstacle origin axis", withObstacle(map[string]interface{}{"x": nil}, "p"),
			"invalid_request: [<class 'decimal.ConversionSyntax'>]"},
		{"list tag", setItem("tags", []interface{}{[]interface{}{}}), "invalid_request: unhashable type: 'list'"},
		{"object tag", setItem("tags", []interface{}{map[string]interface{}{}}), "invalid_request: unhashable type: 'dict'"},
		{"list incompatible tag", setItem("incompatible_tags", []interface{}{[]interface{}{int64(1)}}),
			"invalid_request: unhashable type: 'list'"},
		{"object container tag", setContainer("tags", []interface{}{map[string]interface{}{"k": int64(1)}}),
			"invalid_request: unhashable type: 'dict'"},
		{"null length unit", func(r map[string]interface{}) { r["units"] = map[string]interface{}{"length": nil} },
			"invalid_request: unsupported length unit: None"},
		{"list seed", setConfiguration("seed", []interface{}{int64(1)}),
			"invalid_request: int() argument must be a string, a bytes-like object or a real number, not 'list'"},
		{"object seed", setConfiguration("seed", map[string]interface{}{"value": "1"}),
			"invalid_request: int() argument must be a string, a bytes-like object or a real number, not 'dict'"},
		{"number solvers", setConfiguration("solvers", int64(1)), "invalid_request: 'int' object is not iterable"},
		{"boolean solvers", setConfiguration("solvers", true), "invalid_request: 'bool' object is not iterable"},
	}
	for _, testCase := range cases {
		refusal := refusalOf(t, testCase.edit)
		if refusal.Reason != "invalid_value" || refusal.Field != "" || refusal.Error() != testCase.message {
			t.Errorf("%s: %#v", testCase.name, refusal)
		}
	}
}

func setConfiguration(key string, value interface{}) func(map[string]interface{}) {
	return func(r map[string]interface{}) { r["configuration"].(map[string]interface{})[key] = value }
}

// TestSolversAreIteratedAsTheReferenceIteratesThem: an empty object is no solver at all, and
// a string is its characters, each an unknown solver name.
func TestSolversAreIteratedAsTheReferenceIteratesThem(t *testing.T) {
	request := validRequest()
	setConfiguration("solvers", map[string]interface{}{})(request)
	if _, err := Pack(request); err != nil {
		t.Fatalf("an empty object of solvers was refused: %v", err)
	}
	request = validRequest()
	setConfiguration("solvers", "x")(request)
	if _, err := Pack(request); err == nil || !strings.Contains(err.Error(), "unknown solver name(s) ['x']") {
		t.Fatalf("got %v", err)
	}
}

// TestAPolicyIsRefusedBeforeAnItemAsTheReferenceOrdersThem: the reference compiles the
// policy after the configuration and the catalog references and before any item.
func TestAPolicyIsRefusedBeforeAnItemAsTheReferenceOrdersThem(t *testing.T) {
	badPolicy := func(r map[string]interface{}) { r["policy"] = "not a rule set" }
	alone := refusalOf(t, badPolicy)
	both := refusalOf(t, func(r map[string]interface{}) {
		badPolicy(r)
		setItem("shape_type", "blob")(r)
	})
	if both.Error() != alone.Error() || both.Reason != "invalid_value" {
		t.Errorf("refused for %q, the policy alone for %q", both.Error(), alone.Error())
	}
	item := refusalOf(t, setItem("shape_type", "blob"))
	if item.Error() == alone.Error() {
		t.Fatal("the two refusals cannot be told apart")
	}
}

func sidesOf(length, width, height string) map[string]interface{} {
	return map[string]interface{}{"length": length, "width": width, "height": height}
}

func machine(fields map[string]interface{}) map[string]interface{} {
	spec := map[string]interface{}{"minimum": sidesOf("50", "50", "50"), "increment": sidesOf("10", "10", "10")}
	for key, value := range fields {
		spec[key] = value
	}
	return spec
}

// The 1.6 fields, refused in the reference's order with its code, reason, field and message.
func TestTheNewFieldsAreRefusedAsTheReferenceRefusesThem(t *testing.T) {
	bag := map[string]interface{}{"kind": "flat_bag"}
	cases := []struct {
		edit                  func(map[string]interface{})
		reason, field, detail string
	}{
		{setItem("stacking_group", int64(1)), "wrong_type", "/items/0/stacking_group", "must be a string"},
		{setItem("stacking_group", ""), "invalid_value", "/items/0/stacking_group", "must not be empty"},
		{setItem("accepts_stacking_groups", "A"), "wrong_type", "/items/0/accepts_stacking_groups", "must be a list"},
		{setItem("accepts_stacking_groups", []interface{}{"A", ""}), "invalid_value",
			"/items/0/accepts_stacking_groups/1", "must not be empty"},
		{setContainer("deformable_boundary", map[string]interface{}{"kind": "sack"}), "not_allowed",
			"/containers/0/deformable_boundary/kind", `must be one of ["flat_bag"]`},
		{setContainer("deformable_boundary", map[string]interface{}{}), "missing_field",
			"/containers/0/deformable_boundary/kind", "is required"},
		{setContainer("deformable_boundary", map[string]interface{}{"kind": "flat_bag", "seal": true}), "not_allowed",
			"/containers/0/deformable_boundary/seal", "is not a known field"},
		{func(r map[string]interface{}) {
			firstContainer(r)["deformable_boundary"] = bag
			firstContainer(r)["access_directions"] = []interface{}{"+x"}
		}, "not_allowed", "/containers/0/access_directions", "cannot be combined with deformable_boundary"},
		{func(r map[string]interface{}) {
			firstContainer(r)["deformable_boundary"] = bag
			firstContainer(r)["made_to_measure"] = machine(nil)
		}, "not_allowed", "/containers/0/made_to_measure", "cannot be combined with deformable_boundary"},
		{setContainer("made_to_measure", map[string]interface{}{"minimum": sidesOf("50", "50", "50")}),
			"missing_field", "/containers/0/made_to_measure/increment", "is required"},
		{setContainer("made_to_measure", machine(map[string]interface{}{"blade": int64(1)})), "not_allowed",
			"/containers/0/made_to_measure/blade", "is not a known field"},
		{setContainer("made_to_measure", machine(map[string]interface{}{"minimum": sidesOf("0", "50", "50")})),
			"invalid_value", "/containers/0/made_to_measure/minimum/length", "must be positive"},
		{setContainer("made_to_measure", machine(map[string]interface{}{"minimum": sidesOf("50", "300", "50")})),
			"invalid_value", "/containers/0/made_to_measure/minimum/width", "exceeds inner_dimensions"},
		{setContainer("made_to_measure", machine(map[string]interface{}{"increment": sidesOf("10", "10", "7")})),
			"invalid_value", "/containers/0/made_to_measure/increment/height",
			"does not divide inner_dimensions minus minimum"},
		{func(r map[string]interface{}) {
			firstContainer(r)["made_to_measure"] = machine(map[string]interface{}{"cost_per_square_metre_minor": int64(1)})
			firstContainer(r)["cost_minor"] = int64(1)<<53 - 1
		}, "invalid_value", "/containers/0/made_to_measure/cost_per_square_metre_minor",
			"prices the largest box above 9007199254740991"},
		{func(r map[string]interface{}) {
			firstContainer(r)["made_to_measure"] = machine(nil)
			firstContainer(r)["outer_dimensions"] = sidesOf("300", "200", "300")
		}, "not_allowed", "/containers/0/outer_dimensions", "cannot be combined with made_to_measure"},
		{func(r map[string]interface{}) {
			r["configuration"].(map[string]interface{})["objective"] = "maximum_area"
		}, "not_allowed", "/configuration/objective",
			`must be one of ["default","lowest_cost","shipping_cost","lowest_landed_cost","open_dimension_height","maximum_value","maximum_volume"]`},
	}
	for _, testCase := range cases {
		refusal := refusalOf(t, testCase.edit)
		if refusal.Reason != testCase.reason || refusal.Field != testCase.field || refusal.Detail != testCase.detail {
			t.Errorf("%s: %#v", testCase.field, refusal)
		}
	}
}

// A made-to-measure machine with axles, obstacles, a reserve or a fixed placement is refused
// as an unsupported feature on the raw request, before any request error.
func TestAMadeToMeasureCombinationIsAnUnsupportedFeature(t *testing.T) {
	request := validRequest()
	firstContainer(request)["made_to_measure"] = machine(nil)
	firstContainer(request)["void_fill_reserve_ratio"] = 0.1
	firstItem(request)["quantity"] = "not a number"
	_, err := Pack(request)
	if _, unsupported := err.(*UnsupportedFeatureError); !unsupported ||
		!strings.Contains(err.Error(), "container.made_to_measure+void_fill_reserve_ratio") {
		t.Fatalf("got %v", err)
	}
}
