package validation

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"reflect"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/sequence"
	"github.com/packvium/packvium-go/units"
)

// testdata/reference-scenes.json holds random scenes run through the v1.0.0 reference
// engine: every default constraint's verdict on each candidate (with one constraint list per
// scene, so the per-solve caches are exercised as a solver would), the whole-scene load
// analysis, every sequence entry point, and the independent validator's issue list.

type itemCase struct {
	ID                        string   `json:"id"`
	Dims                      [3]int64 `json:"dims"`
	Weight                    int64    `json:"weight"`
	Quantity                  int64    `json:"quantity"`
	Stackable                 bool     `json:"stackable"`
	MaxTopLoad                *int64   `json:"max_top_load"`
	MaxStackedItems           *int64   `json:"max_stacked_items"`
	MinimumSupportRatio       float64  `json:"minimum_support_ratio"`
	GroundContactRule         *string  `json:"ground_contact_rule"`
	Tags                      []string `json:"tags"`
	IncompatibleTags          []string `json:"incompatible_tags"`
	EligibleContainerTags     []string `json:"eligible_container_tags"`
	StopIndex                 *int64   `json:"stop_index"`
	MustBeOnFloor             bool     `json:"must_be_on_floor"`
	Group                     *string  `json:"group"`
	NestingHeight             *int64   `json:"nesting_height"`
	ShapeType                 *string  `json:"shape_type"`
	CompressionRatioPPM       *int64   `json:"compression_ratio_ppm"`
	MaxCompressionPressureKPA *int64   `json:"max_compression_pressure_kpa"`
}

type containerCase struct {
	Dims            [3]int64         `json:"dims"`
	Tags            []string         `json:"tags"`
	Tare            int64            `json:"tare"`
	TagLimits       map[string]int64 `json:"tag_limits"`
	MaxStackDensity *int64           `json:"max_stack_density"`
	Axles           [][2]*int64      `json:"axles"`
	MaxItems        *int64           `json:"max_items"`
	MaxPayload      *int64           `json:"max_payload"`
}

type outcome struct {
	OK      json.RawMessage `json:"ok"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

type sceneCase struct {
	Items       []itemCase    `json:"items"`
	Container   containerCase `json:"container"`
	Placements  [][5]int64    `json:"placements"`
	Access      []string      `json:"access"`
	Support     float64       `json:"support"`
	Evaluations []struct {
		Prefix  int             `json:"prefix"`
		Point   [3]int64        `json:"point"`
		Stack   bool            `json:"stack"`
		Route   bool            `json:"route"`
		Results [][]interface{} `json:"results"`
	} `json:"evaluations"`
	Loads struct {
		TopLoads     []int64       `json:"top_loads"`
		RestingAbove [][]int       `json:"resting_above"`
		Overloaded   []string      `json:"overloaded"`
		Crushed      []string      `json:"crushed"`
		StackLimit   []string      `json:"stack_limit"`
		Density      []string      `json:"density"`
		Graph        [][][2]int64  `json:"graph"`
		Axles        []json.Number `json:"axles"`
		Balanced     []int64       `json:"balanced"`
	} `json:"loads"`
	Sequence struct {
		Directions    []string `json:"directions"`
		Removal       outcome  `json:"removal"`
		Loading       outcome  `json:"loading"`
		Route         outcome  `json:"route"`
		Reach         outcome  `json:"reach"`
		ForPlacements outcome  `json:"for_placements"`
	} `json:"sequence"`
	Validation struct {
		Ratio    float64     `json:"ratio"`
		Unpacked [][2]string `json:"unpacked"`
		Issues   [][2]string `json:"issues"`
	} `json:"validation"`
}

func weightPointer(ticks *int64) *units.Weight {
	if ticks == nil {
		return nil
	}
	weight := units.MustWeight(*ticks)
	return &weight
}

func buildItem(t *testing.T, spec itemCase) *domain.Item {
	return newItem(t, spec.ID, domain.MustDimensions(spec.Dims[0], spec.Dims[1], spec.Dims[2]), func(s *domain.ItemSpec) {
		s.Weight, s.Quantity, s.Stackable = units.MustWeight(spec.Weight), spec.Quantity, spec.Stackable
		s.MaxTopLoad, s.MaxStackedItems = weightPointer(spec.MaxTopLoad), spec.MaxStackedItems
		s.MinimumSupportRatio, s.GroundContactRule = spec.MinimumSupportRatio, spec.GroundContactRule
		s.Tags, s.IncompatibleTags, s.EligibleContainerTags = spec.Tags, spec.IncompatibleTags, spec.EligibleContainerTags
		s.StopIndex, s.MustBeOnFloor, s.Group = spec.StopIndex, spec.MustBeOnFloor, spec.Group
		if spec.NestingHeight != nil {
			depth := units.MustLength(*spec.NestingHeight)
			s.NestingHeight = &depth
		}
		if spec.ShapeType != nil {
			s.ShapeType = domain.ShapeType(*spec.ShapeType)
			s.CompressionRatioPPM, s.MaxCompressionPressureKPA = spec.CompressionRatioPPM, spec.MaxCompressionPressureKPA
		}
	})
}

func buildContainer(t *testing.T, spec containerCase) *domain.Container {
	built := domain.ContainerSpec{ID: "C", InnerDimensions: domain.MustDimensions(spec.Dims[0], spec.Dims[1], spec.Dims[2]),
		Tags: spec.Tags, TareWeight: units.MustWeight(spec.Tare), TagLimits: spec.TagLimits,
		MaxStackDensity: weightPointer(spec.MaxStackDensity), MaxItems: spec.MaxItems, MaxPayload: weightPointer(spec.MaxPayload)}
	if spec.Axles != nil {
		var axles [2]domain.Axle
		for index, axle := range spec.Axles {
			axles[index] = domain.NewAxle(units.MustLength(*axle[0]), weightPointer(axle[1]))
		}
		built.Axles = &axles
	}
	return newContainer(t, built)
}

func violationPair(failure *constraint.Violation) []string {
	if failure == nil {
		return nil
	}
	return []string{failure.Code, failure.Detail}
}

// errorOutcome is a Go error as the reference names its exception type.
func errorOutcome(err error) (string, string) {
	switch err.(type) {
	case *sequence.SequenceError:
		return "SequenceError", err.Error()
	case *sequence.RouteSequenceError:
		return "RouteSequenceError", err.Error()
	case *sequence.SequenceReplayError:
		return "SequenceReplayError", err.Error()
	case *sequence.InvalidDirectionError:
		return "InvalidDirectionError", err.Error()
	}
	return fmt.Sprintf("%T", err), err.Error()
}

func checkOutcome(t *testing.T, label string, expected outcome, actual interface{}, err error) {
	t.Helper()
	if err != nil || expected.Error != "" {
		name, message := "", ""
		if err != nil {
			name, message = errorOutcome(err)
		}
		if name != expected.Error || message != expected.Message {
			t.Errorf("%s: expected %s %q, got %s %q (%v)", label, expected.Error, expected.Message, name, message, actual)
		}
		return
	}
	encoded, _ := json.Marshal(actual)
	var want, got interface{}
	if json.Unmarshal(expected.OK, &want) != nil || json.Unmarshal(encoded, &got) != nil || !reflect.DeepEqual(want, got) {
		t.Errorf("%s: expected %s, got %s", label, expected.OK, encoded)
	}
}

func maps(values interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	switch typed := values.(type) {
	case []sequence.SequenceStep:
		for _, step := range typed {
			out = append(out, step.ToMap())
		}
	case []sequence.Reachability:
		for _, reach := range typed {
			out = append(out, reach.ToMap())
		}
	}
	return out
}

func TestMatchesTheReferenceOnRandomScenes(t *testing.T) {
	data, err := ioutil.ReadFile("testdata/reference-scenes.json")
	if err != nil {
		t.Fatal(err)
	}
	var scenes []sceneCase
	if err := json.Unmarshal(data, &scenes); err != nil {
		t.Fatal(err)
	}
	for index, scene := range scenes {
		t.Run(fmt.Sprint(index), func(t *testing.T) { checkScene(t, scene) })
	}
}

func checkScene(t *testing.T, scene sceneCase) {
	items := make([]*domain.Item, len(scene.Items))
	for index, spec := range scene.Items {
		items[index] = buildItem(t, spec)
	}
	container := buildContainer(t, scene.Container)
	placements := make([]domain.Placement, len(scene.Placements))
	for index, ref := range scene.Placements {
		item := items[ref[0]]
		point := domain.MustPoint(ref[2], ref[3], ref[4])
		placements[index] = domain.NewPlacement(instance(item, ref[1]), point, domain.LWH, item.Dimensions(), point, item.Dimensions(), 1)
	}
	checkEvaluations(t, scene, container, placements)
	checkLoads(t, scene, container, placements)
	checkSequence(t, scene, container, placements)
	checkValidation(t, scene, items, container, placements)
}

func checkEvaluations(t *testing.T, scene sceneCase, container *domain.Container, placements []domain.Placement) {
	constraints, err := constraint.Defaults(scene.Support, scene.Access)
	if err != nil {
		t.Fatal(err)
	}
	for _, evaluation := range scene.Evaluations {
		candidate := placements[evaluation.Prefix]
		point := domain.MustPoint(evaluation.Point[0], evaluation.Point[1], evaluation.Point[2])
		context := constraint.NewContext(container, placements[:evaluation.Prefix], candidate.Instance(), point, domain.LWH,
			candidate.Dimensions(), candidate.EnvelopeDimensions()).WithSensitivity(evaluation.Stack, evaluation.Route)
		for position, rule := range constraints {
			result := rule.Evaluate(context)
			actual := []interface{}{result.Allowed(), result.Code(), result.Detail()}
			if !sameVerdict(rule, evaluation.Results[position], actual) {
				t.Errorf("prefix %d at %v constraint %T: expected %v, got %v", evaluation.Prefix, evaluation.Point, rule,
					evaluation.Results[position], actual)
			}
		}
	}
}

// sameVerdict compares a verdict exactly, except which blocker a route order refusal names.
// The reference picks the first member of a hash set of indices, an order that depends on
// its hash table's layout; this port names the first one reached up the support graph. The
// blocked item, the code and whether a blocker exists all still have to match, and the
// detail never reaches a packing result (only the code is recorded).
func sameVerdict(rule constraint.PlacementConstraint, expected, actual []interface{}) bool {
	if _, route := rule.(constraint.RouteOrderConstraint); route && expected[1] == "unloading_order_violation" {
		blocked := func(detail interface{}) string { return strings.SplitN(detail.(string), " blocked by ", 2)[0] }
		return reflect.DeepEqual(expected[:2], actual[:2]) && blocked(expected[2]) == blocked(actual[2])
	}
	return reflect.DeepEqual(expected, actual)
}

func checkLoads(t *testing.T, scene sceneCase, container *domain.Container, placements []domain.Placement) {
	loadUnits := constraint.Units(placements)
	expected := scene.Loads
	graph := constraint.NewLoadSupportGraph(loadUnits, 1)
	for index := range loadUnits {
		var edges [][2]int64
		for _, edge := range graph.Supporters(index) {
			edges = append(edges, [2]int64{int64(edge.Index), edge.Area})
		}
		if len(edges) != len(expected.Graph[index]) || (len(edges) > 0 && !reflect.DeepEqual(expected.Graph[index], edges)) {
			t.Errorf("supporters of %d: expected %v, got %v", index, expected.Graph[index], edges)
		}
	}
	analysis := constraint.NewLoadAnalysis(loadUnits, graph)
	if !reflect.DeepEqual(expected.TopLoads, analysis.TopLoads()) {
		t.Errorf("top loads: expected %v, got %v", expected.TopLoads, analysis.TopLoads())
	}
	for index, above := range analysis.RestingAbove() {
		sorted := append([]int(nil), above...)
		sortInts(sorted)
		if len(sorted) != len(expected.RestingAbove[index]) || (len(sorted) > 0 && !reflect.DeepEqual(expected.RestingAbove[index], sorted)) {
			t.Errorf("resting above %d: expected %v, got %v", index, expected.RestingAbove[index], sorted)
		}
	}
	pairs := [][2][]string{
		{expected.Overloaded, violationPair(constraint.Overloaded(loadUnits))},
		{expected.Crushed, violationPair(constraint.Crushed(loadUnits))},
		{expected.StackLimit, violationPair(constraint.StackLimitExceeded(loadUnits))},
		{expected.Density, violationPair(constraint.StackDensityExceeded(loadUnits, constraint.DensityLimitTicks(container)))},
	}
	for _, pair := range pairs {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("load check: expected %v, got %v", pair[0], pair[1])
		}
	}
	if axles := container.Axles(); axles != nil {
		tare, length := container.TareWeight().Ticks(), container.InnerDimensions().Length().Ticks()
		reactions := constraint.Reactions(*axles, loadUnits, tare, length)
		actual := []string{reactions.Denominator.String(), reactions.FrontNumerator.String(), reactions.RearNumerator.String()}
		if fmt.Sprint(expected.Axles) != fmt.Sprint(actual) {
			t.Errorf("axle reactions: expected %v, got %v", expected.Axles, actual)
		}
		last := loadUnits[len(loadUnits)-1]
		origins := constraint.AxleBalancedOrigins(*axles, loadUnits[:len(loadUnits)-1], tare, length, last.WeightTicks,
			last.Box.Dimensions().Length().Ticks())
		if fmt.Sprint(expected.Balanced) != fmt.Sprint(origins) {
			t.Errorf("balanced origins: expected %v, got %v", expected.Balanced, origins)
		}
	}
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func checkSequence(t *testing.T, scene sceneCase, container *domain.Container, placements []domain.Placement) {
	boxes := make([]domain.Box, len(placements))
	stops := make([]*int64, len(placements))
	for index, placement := range placements {
		boxes[index], stops[index] = placement.EnvelopeBox(), placement.Instance().Item().StopIndex()
	}
	inner, directions, expected := container.InnerDimensions(), scene.Sequence.Directions, scene.Sequence
	removal, err := sequence.SafeRemovalOrder(boxes, inner, directions)
	checkOutcome(t, "removal", expected.Removal, removal, err)
	loading, err := sequence.SafeLoadingOrderWithEvidence(boxes, inner, directions)
	checkOutcome(t, "loading", expected.Loading, maps(loading), err)
	route, err := sequence.SafeRouteRemovalOrder(boxes, stops, inner, directions)
	if route == nil && err == nil {
		route = []int{}
	}
	checkOutcome(t, "route", expected.Route, route, err)
	reach, err := sequence.PlacementReachability(boxes, inner, stops, directions)
	checkOutcome(t, "reach", expected.Reach, maps(reach), err)
	order, err := sequence.SafeLoadingOrderForPlacements(placements, container, directions)
	checkOutcome(t, "for placements", expected.ForPlacements, order, err)
}

func checkValidation(t *testing.T, scene sceneCase, items []*domain.Item, container *domain.Container, placements []domain.Placement) {
	request, err := domain.NewPackingRequest(items, []*domain.Container{container})
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]domain.ItemInstance)
	for _, instance := range request.Instances() {
		byID[instance.ID()] = instance
	}
	var unpacked []domain.UnpackedItem
	for _, entry := range scene.Validation.Unpacked {
		unpacked = append(unpacked, domain.NewUnpackedItem(byID[entry[0]], entry[1], nil))
	}
	report := ValidateWithUnpacked(request, []*domain.PackedContainer{domain.NewPackedContainer(container, 1, placements)},
		scene.Validation.Ratio, nil, unpacked)
	var actual [][2]string
	for _, issue := range report.Issues() {
		actual = append(actual, [2]string{issue.Code, issue.Detail})
	}
	if len(actual) != len(scene.Validation.Issues) || (len(actual) > 0 && !reflect.DeepEqual(scene.Validation.Issues, actual)) {
		t.Errorf("validation: expected\n%v\ngot\n%v", scene.Validation.Issues, actual)
	}
}
