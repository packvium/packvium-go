package serialization

import (
	"reflect"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/packer"
	"github.com/packvium/packvium-go/units"
	"github.com/packvium/packvium-go/validation"
)

// The wire-level behaviour of fixed placements, after the reference's
// tests/test_fixed_placements.py.

func cubeItem(quantity int64, extra map[string]interface{}) map[string]interface{} {
	item := map[string]interface{}{"id": "cube", "quantity": quantity, "weight": "1000",
		"dimensions": map[string]interface{}{"length": "100", "width": "100", "height": "100"}}
	for key, value := range extra {
		item[key] = value
	}
	return item
}

func boxContainer(quantity int64, extra map[string]interface{}) map[string]interface{} {
	container := map[string]interface{}{"id": "box", "quantity": quantity,
		"inner_dimensions": map[string]interface{}{"length": "200", "width": "100", "height": "200"}}
	for key, value := range extra {
		container[key] = value
	}
	return container
}

func fixedAt(x, z string, instance int64) map[string]interface{} {
	return map[string]interface{}{"item_type": "cube", "container_type": "box", "container_instance": instance,
		"position": map[string]interface{}{"x": x, "y": "0", "z": z}, "orientation": "LWH"}
}

func fixedRequest(items, containers, placements []interface{}, configuration map[string]interface{}) map[string]interface{} {
	if items == nil {
		items = []interface{}{cubeItem(5, nil)}
	}
	if containers == nil {
		containers = []interface{}{boxContainer(3, nil)}
	}
	if placements == nil {
		placements = []interface{}{fixedAt("100", "0", 1)}
	}
	request := map[string]interface{}{"items": items, "containers": containers, "fixed_placements": placements}
	if configuration != nil {
		request["configuration"] = configuration
	}
	return request
}

// fixedRows are container id, item id and x, y, z of every placement marked fixed.
func fixedRows(t *testing.T, result map[string]interface{}) [][5]string {
	t.Helper()
	var rows [][5]string
	for _, raw := range result["containers"].([]interface{}) {
		container := raw.(map[string]interface{})
		placements, _ := container["placements"].([]interface{})
		for _, entry := range placements {
			placement := entry.(map[string]interface{})
			if placement["fixed"] != true {
				continue
			}
			position := placement["position"].(map[string]interface{})
			rows = append(rows, [5]string{container["id"].(string), placement["item_id"].(string),
				axisValue(position, "x"), axisValue(position, "y"), axisValue(position, "z")})
		}
	}
	return rows
}

func axisValue(position map[string]interface{}, axis string) string {
	return position[axis].(map[string]interface{})["value"].(string)
}

func pack(t *testing.T, request map[string]interface{}) map[string]interface{} {
	t.Helper()
	result, err := Pack(roundTrip(t, request))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// roundTrip is the request as a JSON caller sends it, so integers are int64 as decoded.
func roundTrip(t *testing.T, value interface{}) interface{} {
	t.Helper()
	text, err := jsonvalue.Compact(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jsonvalue.DecodeBytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func placementCount(result map[string]interface{}) int {
	count := 0
	for _, raw := range result["containers"].([]interface{}) {
		placements, _ := raw.(map[string]interface{})["placements"].([]interface{})
		count += len(placements)
	}
	return count
}

func TestAFixedItemIsReportedWhereTheRequestPutIt(t *testing.T) {
	for _, profile := range []string{"fast", "balanced", "quality", "exact_small"} {
		result := pack(t, fixedRequest(nil, nil, nil, map[string]interface{}{"solver_profile": profile}))
		if status := result["status"]; status != "feasible" && status != "optimal" {
			t.Errorf("%s: status %v", profile, status)
		}
		if rows := fixedRows(t, result); !reflect.DeepEqual(rows, [][5]string{{"box#1", "cube#1", "100", "0", "0"}}) {
			t.Errorf("%s: fixed rows %v", profile, rows)
		}
		if count := placementCount(result); count != 5 {
			t.Errorf("%s: %d placements", profile, count)
		}
	}
}

func TestEverySolverPacksAroundFixedItems(t *testing.T) {
	for _, solver := range []string{"grid", "homogeneous_blocks", "maximal_spaces", "layer", "extreme_points"} {
		result := pack(t, fixedRequest(nil, nil, nil, map[string]interface{}{"solvers": []interface{}{solver}}))
		if rows := fixedRows(t, result); !reflect.DeepEqual(rows, [][5]string{{"box#1", "cube#1", "100", "0", "0"}}) {
			t.Errorf("%s: fixed rows %v", solver, rows)
		}
		if warnings := result["warnings"].([]interface{}); len(warnings) > 0 {
			t.Errorf("%s: %v", solver, warnings)
		}
		if count := placementCount(result); count != 5 {
			t.Errorf("%s: %d placements", solver, count)
		}
	}
}

func TestARequestWithoutFixedPlacementsAnswersAsBefore(t *testing.T) {
	absent := fixedRequest(nil, nil, nil, nil)
	delete(absent, "fixed_placements")
	empty := fixedRequest(nil, nil, []interface{}{}, nil)
	if !reflect.DeepEqual(withoutDurations(pack(t, absent)), withoutDurations(pack(t, empty))) {
		t.Error("an empty fixed_placements changed the answer")
	}
}

func withoutDurations(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := map[string]interface{}{}
		for key, element := range typed {
			if key != "duration_ms" {
				out[key] = withoutDurations(element)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, element := range typed {
			out[index] = withoutDurations(element)
		}
		return out
	}
	return value
}

func TestFixedItemsTakeTheFirstInstancesInListedOrder(t *testing.T) {
	result := pack(t, fixedRequest(nil, nil, []interface{}{fixedAt("100", "0", 1), fixedAt("0", "0", 1)}, nil))
	want := [][5]string{{"box#1", "cube#1", "100", "0", "0"}, {"box#1", "cube#2", "0", "0", "0"}}
	if rows := fixedRows(t, result); !reflect.DeepEqual(rows, want) {
		t.Errorf("fixed rows %v", rows)
	}
}

func TestFixedContainersOpenFirstAndFreeOnesAreNumberedAfter(t *testing.T) {
	result := pack(t, fixedRequest([]interface{}{cubeItem(9, nil)}, nil,
		[]interface{}{fixedAt("0", "0", 1), fixedAt("0", "0", 2)}, nil))
	var ids []string
	for _, raw := range result["containers"].([]interface{}) {
		ids = append(ids, raw.(map[string]interface{})["id"].(string))
	}
	if !reflect.DeepEqual(ids, []string{"box#1", "box#2", "box#3"}) {
		t.Errorf("container ids %v", ids)
	}
}

func TestAFixedContainerSurvivesASearchThatRunsOutOfEffort(t *testing.T) {
	result := pack(t, fixedRequest([]interface{}{cubeItem(40, nil)}, []interface{}{boxContainer(20, nil)},
		[]interface{}{fixedAt("0", "0", 1), fixedAt("0", "0", 2)},
		map[string]interface{}{"effort_budget": map[string]interface{}{"max_search_nodes": 1}}))
	containers := map[string]bool{}
	for _, row := range fixedRows(t, result) {
		containers[row[0]] = true
	}
	if !containers["box#1"] || !containers["box#2"] || len(containers) != 2 {
		t.Errorf("fixed containers %v", containers)
	}
	if warnings := result["warnings"].([]interface{}); len(warnings) > 0 {
		t.Error(warnings)
	}
}

func TestAFreeItemRestingOnAFixedOneLoadsIt(t *testing.T) {
	container := boxContainer(1, nil)
	container["inner_dimensions"].(map[string]interface{})["length"] = "100"
	result := pack(t, fixedRequest([]interface{}{cubeItem(2, nil)}, []interface{}{container},
		[]interface{}{fixedAt("0", "0", 1)}, map[string]interface{}{"solvers": []interface{}{"extreme_points"}}))
	placements := result["containers"].([]interface{})[0].(map[string]interface{})["placements"].([]interface{})
	byID := map[string]map[string]interface{}{}
	for _, entry := range placements {
		placement := entry.(map[string]interface{})
		byID[placement["item_id"].(string)] = placement
	}
	if z := axisValue(byID["cube#2"]["position"].(map[string]interface{}), "z"); z != "100" {
		t.Errorf("cube#2 at z %s", z)
	}
	if load := byID["cube#1"]["top_load"].(map[string]interface{})["value"]; load != "1000" {
		t.Errorf("cube#1 bears %v", load)
	}
}

func TestAFixedSetThatCannotHoldIsRefusedBeforeSearch(t *testing.T) {
	cases := []struct {
		fragment string
		mutate   func(map[string]interface{})
	}{
		{"collision", func(r map[string]interface{}) { appendFixed(r, fixedAt("100", "0", 1)) }},
		{"unknown item type", func(r map[string]interface{}) { firstFixed(r)["item_type"] = "crate" }},
		{"unknown container type", func(r map[string]interface{}) { firstFixed(r)["container_type"] = "crate" }},
		{"[2] are not numbered 1..1", func(r map[string]interface{}) { firstFixed(r)["container_instance"] = int64(2) }},
		{"outside_container", func(r map[string]interface{}) {
			firstFixed(r)["position"].(map[string]interface{})["x"] = "150"
		}},
		{"2 cube fixed, 1 requested", func(r map[string]interface{}) {
			firstItem(r)["quantity"] = int64(1)
			appendFixed(r, fixedAt("0", "0", 1))
		}},
		{"orientation HWL", func(r map[string]interface{}) {
			firstItem(r)["keep_upright"] = true
			firstFixed(r)["orientation"] = "HWL"
		}},
		{"obstacle_collision", func(r map[string]interface{}) {
			firstContainer(r)["obstacles"] = []interface{}{map[string]interface{}{"id": "pillar",
				"origin":     map[string]interface{}{"x": "150"},
				"dimensions": map[string]interface{}{"length": "10", "width": "10", "height": "10"}}}
		}},
		{"payload_exceeded", func(r map[string]interface{}) { firstContainer(r)["max_payload"] = "500" }},
		{"outside_container", func(r map[string]interface{}) {
			r["configuration"] = map[string]interface{}{"clearance": "1"}
		}},
		{"insufficient_support", func(r map[string]interface{}) {
			r["configuration"] = map[string]interface{}{"minimum_support_ratio": int64(1)}
			firstFixed(r)["position"].(map[string]interface{})["z"] = "50"
		}},
		{"2 box named, 1 available", func(r map[string]interface{}) {
			firstContainer(r)["quantity"] = int64(1)
			appendFixed(r, fixedAt("0", "0", 2))
		}},
		{"max_containers is 1", func(r map[string]interface{}) {
			r["configuration"] = map[string]interface{}{"max_containers": int64(1)}
			appendFixed(r, fixedAt("0", "0", 2))
		}},
	}
	for _, testCase := range cases {
		request := fixedRequest(nil, nil, nil, nil)
		testCase.mutate(request)
		_, err := Pack(roundTrip(t, request))
		refusal, ok := err.(*domain.InvalidRequestError)
		if !ok || !refusal.IsFixedPlacementError() || refusal.Reason != "cannot_hold" {
			t.Errorf("%s: %v", testCase.fragment, err)
			continue
		}
		if message := refusal.Error(); !strings.HasPrefix(message, "invalid_fixed_placement: ") ||
			!strings.Contains(message, testCase.fragment) {
			t.Errorf("%s: %s", testCase.fragment, message)
		}
	}
}

func appendFixed(request map[string]interface{}, entry map[string]interface{}) {
	request["fixed_placements"] = append(request["fixed_placements"].([]interface{}), entry)
}

func firstFixed(request map[string]interface{}) map[string]interface{} {
	return request["fixed_placements"].([]interface{})[0].(map[string]interface{})
}

func firstItem(request map[string]interface{}) map[string]interface{} {
	return request["items"].([]interface{})[0].(map[string]interface{})
}

func firstContainer(request map[string]interface{}) map[string]interface{} {
	return request["containers"].([]interface{})[0].(map[string]interface{})
}

func TestAMalformedFixedPlacementIsRefusedNotCoerced(t *testing.T) {
	cases := []struct {
		placements interface{}
		detail     string
		field      string
	}{
		{"x", "fixed_placements is a list", "/fixed_placements"},
		{[]interface{}{map[string]interface{}{"item_type": "cube", "container_type": "box", "orientation": "LWH",
			"container_instance": "1"}}, "fixed_placements[0].container_instance counts from 1",
			"/fixed_placements/0/container_instance"},
		{[]interface{}{map[string]interface{}{"item_type": "cube", "container_type": "box", "orientation": "LWH",
			"position": []interface{}{"0", "0", "0"}}}, "fixed_placements[0].position is a point object",
			"/fixed_placements/0/position"},
		{[]interface{}{map[string]interface{}{"item_type": "cube", "container_type": "box", "orientation": "LWH",
			"position": map[string]interface{}{"x": "-1"}}}, "fixed_placements[0].position.x cannot be negative",
			"/fixed_placements/0/position/x"},
	}
	for _, testCase := range cases {
		request := fixedRequest(nil, nil, nil, nil)
		request["fixed_placements"] = testCase.placements
		_, err := Pack(roundTrip(t, request))
		refusal, ok := err.(*domain.InvalidRequestError)
		if !ok || refusal.Reason != "malformed" || refusal.Field != testCase.field ||
			refusal.Error() != "invalid_fixed_placement: "+testCase.detail {
			t.Errorf("%s: %#v", testCase.detail, err)
		}
	}
}

func TestNullFixedPlacementsAndAnIntegralFloatInstanceAreAccepted(t *testing.T) {
	for _, placements := range []interface{}{nil, []interface{}{map[string]interface{}{"item_type": "cube",
		"container_type": "box", "orientation": "LWH", "container_instance": 1.0}}} {
		request := fixedRequest(nil, nil, nil, nil)
		request["fixed_placements"] = placements
		if _, err := Pack(request); err != nil {
			t.Errorf("%v: %v", placements, err)
		}
	}
}

func cubesAndBoxes(t *testing.T) ([]*domain.Item, []*domain.Container) {
	t.Helper()
	side := 100 * units.TicksPerMM
	spec := domain.NewItemSpec("cube", domain.MustDimensions(side, side, side))
	spec.Quantity, spec.Weight = 5, units.MustWeight(1000*8_000_000)
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	quantity := int64(3)
	container, err := domain.NewContainer(domain.ContainerSpec{ID: "box",
		InnerDimensions: domain.MustDimensions(2*side, side, 2*side), Quantity: &quantity})
	if err != nil {
		t.Fatal(err)
	}
	return []*domain.Item{item}, []*domain.Container{container}
}

func fixedCube(t *testing.T, xMM, instance int64) domain.FixedPlacement {
	t.Helper()
	fixed, err := domain.NewFixedPlacement("cube", "box", domain.MustPoint(xMM*units.TicksPerMM, 0, 0), domain.LWH, instance)
	if err != nil {
		t.Fatal(err)
	}
	return fixed
}

func TestTheValidatorCatchesAMovedOrMissingFixedItem(t *testing.T) {
	items, containers := cubesAndBoxes(t)
	balanced, err := config.New(config.Default(config.Balanced))
	if err != nil {
		t.Fatal(err)
	}
	packed, err := packer.New(balanced, nil).PackWithFixedPlacements(items, containers,
		[]domain.FixedPlacement{fixedCube(t, 100, 1)})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := domain.NewPackingRequest(items, containers)
	moved := validation.ValidateWithUnpacked(request.WithFixedPlacements([]domain.FixedPlacement{fixedCube(t, 0, 1)}),
		packed.Containers, 0, nil, packed.Unpacked)
	if codes := issueCodes(moved); !reflect.DeepEqual(codes, map[string]bool{"fixed_placement_moved": true,
		"unexpected_fixed_placement": true}) {
		t.Errorf("moved: %v", codes)
	}
	plain, err := packer.New(balanced, nil).Pack(items, containers)
	if err != nil {
		t.Fatal(err)
	}
	missing := validation.Validate(request.WithFixedPlacements([]domain.FixedPlacement{fixedCube(t, 0, 3)}),
		plain.Containers, 0, nil)
	if !issueCodes(missing)["fixed_container_missing"] {
		t.Errorf("missing: %v", issueCodes(missing))
	}
}

func issueCodes(report validation.ValidationReport) map[string]bool {
	codes := map[string]bool{}
	for _, issue := range report.Issues() {
		codes[issue.Code] = true
	}
	return codes
}
