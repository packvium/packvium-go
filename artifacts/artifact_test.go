package artifacts

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/execution"
	"github.com/packvium/packvium-go/internal/jsonvalue"
)

// The tests follow the reference's test_artifacts.py one to one in name and intent: what the
// contract promises the artifact will not do -- re-derive the plan or its order, address a
// placement by item_id, let wall-clock time in, claim an exact replay it cannot give, or
// re-render a value the result already rendered.

const ticksPerMM = 16000

func scalar(ticks int64, unit string) map[string]interface{} {
	perUnit := int64(ticksPerMM)
	if unit != "mm" {
		perUnit = 8000
	}
	return map[string]interface{}{"ticks": ticks, "unit": unit, "value": strconv.FormatInt(ticks/perUnit, 10)}
}

func placement(itemType string, xMM, lengthMM int64) map[string]interface{} {
	position, dimensions := map[string]interface{}{}, map[string]interface{}{}
	for _, axis := range positionAxes {
		offset := int64(0)
		if axis == "x" {
			offset = xMM
		}
		position[axis] = scalar(offset*ticksPerMM, "mm")
	}
	for _, axis := range dimensionAxes {
		size := int64(10)
		if axis == "length" {
			size = lengthMM
		}
		dimensions[axis] = scalar(size*ticksPerMM, "mm")
	}
	return map[string]interface{}{
		"item_id": fmt.Sprintf("%s#%d", itemType, xMM), "item_type": itemType, "orientation": "LWH",
		"position": position, "dimensions": dimensions, "support_ratio": "1.000000", "top_load": scalar(0, "g"),
	}
}

func algorithm() map[string]interface{} {
	return map[string]interface{}{
		"profile": "balanced", "solver": "extreme_point", "seed": int64(7), "duration_ms": int64(41),
		"time_limit_reached": false, "effort_limit_reached": false,
	}
}

func testResult(placements ...interface{}) map[string]interface{} {
	if len(placements) == 0 {
		placements = []interface{}{placement("box", 0, 10), placement("tin", 10, 5)}
	}
	inner := map[string]interface{}{}
	for _, axis := range dimensionAxes {
		inner[axis] = scalar(100*ticksPerMM, "mm")
	}
	return map[string]interface{}{
		"status": "feasible", "objective": "default", "score": []interface{}{int64(1), int64(0), int64(250)},
		"feasibility": map[string]interface{}{"code": "all_items_packed"}, "optimality": nil,
		"containers": []interface{}{map[string]interface{}{
			"id": "crate#1", "container_type": "crate", "inner_dimensions": inner,
			"payload_weight": scalar(8000, "g"), "gross_weight": scalar(16000, "g"),
			"volume_utilization": "0.002000", "placements": placements,
		}},
		"unpacked_items": []interface{}{},
		"catalog_versions_used": []interface{}{map[string]interface{}{
			"catalog_id": "cartons", "version": int64(3), "effective_at": int64(10), "resolved_at": int64(11),
		}},
		"algorithm": algorithm(),
	}
}

func testRequest() map[string]interface{} {
	return map[string]interface{}{
		"items": []interface{}{map[string]interface{}{
			"id": "box", "dimensions": map[string]interface{}{"length": int64(10), "width": int64(10), "height": int64(10)},
			"minimum_support_ratio": 0.25,
		}},
		"containers": []interface{}{map[string]interface{}{
			"id": "crate", "inner_dimensions": map[string]interface{}{"length": int64(100), "width": int64(100), "height": int64(100)},
		}},
	}
}

func container(result map[string]interface{}) map[string]interface{} {
	return result["containers"].([]interface{})[0].(map[string]interface{})
}

func firstPlacement(result map[string]interface{}) map[string]interface{} {
	return container(result)["placements"].([]interface{})[0].(map[string]interface{})
}

func axisOf(object map[string]interface{}, group, axis string) map[string]interface{} {
	return object[group].(map[string]interface{})[axis].(map[string]interface{})
}

func reversed() map[int][]int { return map[int][]int{0: {1, 0}} }

func mustBuild(t *testing.T, request, result interface{}, orders map[int][]int) map[string]interface{} {
	t.Helper()
	artifact, err := Build(request, result, orders)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func codeOf(err error) string {
	if refusal, ok := err.(*Error); ok {
		return refusal.Code
	}
	return fmt.Sprintf("not an artifact error: %v", err)
}

func buildCode(request, result interface{}, orders map[int][]int) string {
	_, err := Build(request, result, orders)
	return codeOf(err)
}

func mustCanonical(t *testing.T, artifact map[string]interface{}) string {
	t.Helper()
	text, err := Canonical(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// ---------------------------------------------------------------------------- the document

func TestThePlanInsideIsExactlyThePlanBuilderOutput(t *testing.T) {
	artifact := mustBuild(t, testRequest(), testResult(), reversed())
	if artifact["format"] != Format || artifact["suite_version"] != SuiteVersion {
		t.Fatalf("format %v, suite version %v", artifact["format"], artifact["suite_version"])
	}
	plan, err := execution.BuildPlan(testRequest(), testResult(), reversed())
	if err != nil {
		t.Fatal(err)
	}
	inside, _ := execution.CanonicalPlanJSON(artifact["plan"].(map[string]interface{}))
	direct, _ := execution.CanonicalPlanJSON(plan)
	if inside != direct {
		t.Fatalf("\n got %s\nwant %s", inside, direct)
	}
}

func TestTheRequestIsEmbeddedAsGivenWithItsFractionalNumbers(t *testing.T) {
	artifact := mustBuild(t, testRequest(), testResult(), nil)
	embedded := artifact["provenance"].(map[string]interface{})["request"]
	if !reflect.DeepEqual(embedded, testRequest()) {
		t.Fatalf("embedded %v", embedded)
	}
	if !strings.Contains(mustCanonical(t, artifact), `"minimum_support_ratio":0.25`) {
		t.Fatal("the fractional number is not in the canonical form")
	}
}

func TestGeometryIsInResultOrderInTickStringsAddressedByPlacementReference(t *testing.T) {
	artifact := mustBuild(t, testRequest(), testResult(), nil)
	geometry := artifact["geometry"].(map[string]interface{})["containers"].([]interface{})[0].(map[string]interface{})
	want := map[string]interface{}{"length": "1600000", "width": "1600000", "height": "1600000"}
	if !reflect.DeepEqual(geometry["inner_dimensions"], want) {
		t.Fatalf("inner dimensions %v", geometry["inner_dimensions"])
	}
	placements := geometry["placements"].([]interface{})
	var types []interface{}
	for _, entry := range placements {
		types = append(types, entry.(map[string]interface{})["placement"].(map[string]interface{})["item_type"])
	}
	if !reflect.DeepEqual(types, []interface{}{"box", "tin"}) {
		t.Fatalf("order %v", types)
	}
	if length := placements[1].(map[string]interface{})["dimensions"].(map[string]interface{})["length"]; length != "80000" {
		t.Fatalf("length %v", length)
	}
	text, _ := jsonvalue.Compact(geometry)
	if strings.Contains(text, "item_id") {
		t.Fatal("geometry names an item_id")
	}
}

func TestWorkOrderLinesFollowThePlanStepsAndCopyRenderedValues(t *testing.T) {
	artifact := mustBuild(t, testRequest(), testResult(), reversed())
	workOrder := artifact["work_order"].(map[string]interface{})
	entry := workOrder["containers"].([]interface{})[0].(map[string]interface{})
	if workOrder["length_unit"] != "mm" || workOrder["weight_unit"] != "g" {
		t.Fatalf("units %v %v", workOrder["length_unit"], workOrder["weight_unit"])
	}
	if entry["payload_weight"] != "1" || entry["gross_weight"] != "2" {
		t.Fatalf("weights %v %v", entry["payload_weight"], entry["gross_weight"])
	}
	lines := entry["lines"].([]interface{})
	var sequence []string
	for _, line := range lines {
		fields := line.(map[string]interface{})
		sequence = append(sequence, fmt.Sprintf("%v %v", fields["sequence"], fields["placement"].(map[string]interface{})["item_type"]))
	}
	if !reflect.DeepEqual(sequence, []string{"1 tin", "2 box"}) {
		t.Fatalf("lines %v", sequence)
	}
	if position := lines[0].(map[string]interface{})["position"]; !reflect.DeepEqual(position, map[string]interface{}{"x": "10", "y": "0", "z": "0"}) {
		t.Fatalf("position %v", position)
	}
	steps := artifact["plan"].(map[string]interface{})["containers"].([]interface{})[0].(map[string]interface{})["steps"].([]interface{})
	for index, step := range steps {
		if !reflect.DeepEqual(step.(map[string]interface{})["placement"], lines[index].(map[string]interface{})["placement"]) {
			t.Fatalf("line %d is not the plan's step", index)
		}
	}
}

func TestWithoutALoadingOrderNoLineIsNumbered(t *testing.T) {
	artifact := mustBuild(t, testRequest(), testResult(), nil)
	lines := artifact["work_order"].(map[string]interface{})["containers"].([]interface{})[0].(map[string]interface{})["lines"].([]interface{})
	for _, line := range lines {
		if _, numbered := line.(map[string]interface{})["sequence"]; numbered {
			t.Fatal("a line is numbered without an order")
		}
	}
}

func TestAResultWithoutContainersHasNoUnitsAndNoLines(t *testing.T) {
	result := testResult()
	result["containers"] = []interface{}{}
	workOrder := mustBuild(t, testRequest(), result, nil)["work_order"]
	want := map[string]interface{}{"length_unit": nil, "weight_unit": nil, "containers": []interface{}{}}
	if !reflect.DeepEqual(workOrder, want) {
		t.Fatalf("work order %v", workOrder)
	}
}

// ------------------------------------------------------------------------------ provenance

func TestTheSolverIsTheDeterministicPartOfTheAlgorithmAndWallClockNeverEnters(t *testing.T) {
	artifact := mustBuild(t, testRequest(), testResult(), nil)
	provenance := artifact["provenance"].(map[string]interface{})
	want := algorithm()
	delete(want, "duration_ms")
	if !reflect.DeepEqual(provenance["solver"], want) {
		t.Fatalf("solver %v", provenance["solver"])
	}
	if !reflect.DeepEqual(provenance["replay"], map[string]interface{}{"level": "exact", "because": nil}) {
		t.Fatalf("replay %v", provenance["replay"])
	}
	if strings.Contains(mustCanonical(t, artifact), "duration_ms") {
		t.Fatal("wall-clock time entered the artifact")
	}
}

func TestASearchStoppedByTheClockIsNotPromisedAnExactReplay(t *testing.T) {
	result := testResult()
	result["algorithm"].(map[string]interface{})["time_limit_reached"] = true
	replay := mustBuild(t, testRequest(), result, nil)["provenance"].(map[string]interface{})["replay"]
	if !reflect.DeepEqual(replay, map[string]interface{}{"level": "not_guaranteed", "because": "provenance.solver.time_limit_reached"}) {
		t.Fatalf("replay %v", replay)
	}
}

func TestAResultThatDoesNotSayHowItWasSolvedIsNotPromisedOneEither(t *testing.T) {
	result := testResult()
	delete(result, "algorithm")
	artifact := mustBuild(t, testRequest(), result, nil)
	provenance := artifact["provenance"].(map[string]interface{})
	if provenance["solver"] != nil {
		t.Fatalf("solver %v", provenance["solver"])
	}
	if !reflect.DeepEqual(provenance["replay"], map[string]interface{}{"level": "not_guaranteed", "because": "provenance.solver"}) {
		t.Fatalf("replay %v", provenance["replay"])
	}
	if !strings.Contains(mustCanonical(t, artifact), `"solver":null`) {
		t.Fatal("an absent solver is not written as null")
	}
}

func TestReplayLevelIsThePromiseTheArtifactWouldMake(t *testing.T) {
	stopped := testResult()
	stopped["algorithm"].(map[string]interface{})["time_limit_reached"] = true
	unsolved := testResult()
	unsolved["algorithm"] = nil
	for _, result := range []map[string]interface{}{testResult(), stopped, unsolved} {
		expected := mustBuild(t, testRequest(), result, nil)["provenance"].(map[string]interface{})["replay"].(map[string]interface{})["level"]
		level, err := ReplayLevel(result)
		if err != nil || level != expected {
			t.Fatalf("replay level %q (%v), artifact says %v", level, err, expected)
		}
	}
	if level, _ := ReplayLevel(testResult()); level != "exact" {
		t.Fatalf("replay level %q", level)
	}
}

// -------------------------------------------------------------------------------- refusals

func TestARefusalNamesItsCode(t *testing.T) {
	mutated := func(change func(map[string]interface{})) map[string]interface{} {
		result := testResult()
		change(result)
		return result
	}
	request := testRequest()
	request["metadata"] = map[string]interface{}{"order": int64(1 << 53)}
	cases := []struct {
		name     string
		request  interface{}
		result   interface{}
		orders   map[int][]int
		expected string
	}{
		{"a request that is not an object", []interface{}{}, testResult(), nil, CodeInvalidRequest},
		{"a result that is not an object", testRequest(), []interface{}{}, nil, CodeInvalidResult},
		{"a loading order that is not a permutation", testRequest(), testResult(), map[int][]int{0: {0, 0}}, CodeInvalidPlanInput},
		{"values in two units", testRequest(), mutated(func(r map[string]interface{}) { axisOf(firstPlacement(r), "position", "x")["unit"] = "cm" }), nil, CodeMixedUnits},
		{"two placements with one reference", testRequest(), testResult(placement("box", 0, 10), placement("box", 0, 10)), nil, CodeInvalidResult},
		{"a container without inner dimensions", testRequest(), mutated(func(r map[string]interface{}) { delete(container(r), "inner_dimensions") }), nil, CodeInvalidResult},
		{"an incomplete algorithm record", testRequest(), mutated(func(r map[string]interface{}) { r["algorithm"] = map[string]interface{}{"profile": "fast"} }), nil, CodeInvalidResult},
		{"a mistyped algorithm flag", testRequest(), mutated(func(r map[string]interface{}) { r["algorithm"].(map[string]interface{})["time_limit_reached"] = "no" }), nil, CodeInvalidResult},
		{"an algorithm that is not an object", testRequest(), mutated(func(r map[string]interface{}) { r["algorithm"] = "fast" }), nil, CodeInvalidResult},
		{"a request number JavaScript cannot hold", request, testResult(), nil, CodeNumberOutOfRange},
		{"a container that is not an object", testRequest(), mutated(func(r map[string]interface{}) { r["containers"] = []interface{}{"crate"} }), nil, CodeInvalidResult},
		{"geometry ticks JavaScript cannot hold", testRequest(), mutated(func(r map[string]interface{}) {
			axisOf(firstPlacement(r), "dimensions", "length")["ticks"] = int64(1 << 53)
		}), nil, CodeNumberOutOfRange},
		{"a fractional catalog version", testRequest(), mutated(func(r map[string]interface{}) {
			r["catalog_versions_used"].([]interface{})[0].(map[string]interface{})["version"] = 1.5
		}), nil, CodeInvalidResult},
		{"a catalog id that is not a string", testRequest(), mutated(func(r map[string]interface{}) {
			r["catalog_versions_used"].([]interface{})[0].(map[string]interface{})["catalog_id"] = int64(7)
		}), nil, CodeInvalidResult},
		{"fractional geometry ticks", testRequest(), mutated(func(r map[string]interface{}) { axisOf(firstPlacement(r), "dimensions", "length")["ticks"] = 1.5 }), nil, CodeInvalidResult},
		{"boolean geometry ticks", testRequest(), mutated(func(r map[string]interface{}) { axisOf(firstPlacement(r), "dimensions", "length")["ticks"] = true }), nil, CodeInvalidResult},
		{"string geometry ticks", testRequest(), mutated(func(r map[string]interface{}) { axisOf(firstPlacement(r), "dimensions", "length")["ticks"] = "160000" }), nil, CodeInvalidResult},
		{"a rendered value that is not a string", testRequest(), mutated(func(r map[string]interface{}) { axisOf(firstPlacement(r), "position", "x")["value"] = int64(0) }), nil, CodeInvalidResult},
		{"a placement that is not an object", testRequest(), testResult(placement("box", 0, 10), "tin"), nil, CodeInvalidResult},
		{"containers that are not a list", testRequest(), mutated(func(r map[string]interface{}) { r["containers"] = map[string]interface{}{"not": "a list"} }), nil, CodeInvalidResult},
		{"unpacked items that are not a list", testRequest(), mutated(func(r map[string]interface{}) { r["unpacked_items"] = map[string]interface{}{"not": "a list"} }), nil, CodeInvalidResult},
		{"catalogs that are not a list", testRequest(), mutated(func(r map[string]interface{}) { r["catalog_versions_used"] = map[string]interface{}{"not": "a list"} }), nil, CodeInvalidResult},
		{"a string that is not UTF-8", testRequest(), mutated(func(r map[string]interface{}) { r["objective"] = "\xed\xa0\x80" }), nil, CodeInvalidString},
		{"a Go value JSON cannot spell", testRequest(), mutated(func(r map[string]interface{}) { r["objective"] = struct{}{} }), nil, CodeInvalidValue},
	}
	for _, testCase := range cases {
		if got := buildCode(testCase.request, testCase.result, testCase.orders); got != testCase.expected {
			t.Errorf("%s: got %s, want %s", testCase.name, got, testCase.expected)
		}
	}
}

func TestParseRefusesWhatItCannotReadExactly(t *testing.T) {
	cases := map[string]string{
		`{"bad":"\ud800"}`:       CodeInvalidString,
		`{"bad":"\udc00\ud800"}`: CodeInvalidString,
		`{"bad":`:                CodeInvalidJSON,
		"{\"bad\":\"\xff\"}":     CodeInvalidJSON,
		`{"a":1} {"b":2}`:        CodeInvalidJSON,
		`{"ok":"😀\\ud800"}`:      "",
	}
	for text, expected := range cases {
		_, err := Parse([]byte(text))
		got := ""
		if err != nil {
			got = codeOf(err)
		}
		if got != expected {
			t.Errorf("%s: got %q, want %q", text, got, expected)
		}
	}
}

func TestParseKeepsIntegersExactAndApartFromFloats(t *testing.T) {
	value, err := Parse([]byte(`{"whole":1.0,"integer":1,"beyond":9007199254740992}`))
	if err != nil {
		t.Fatal(err)
	}
	fields := value.(map[string]interface{})
	if _, isFloat := fields["whole"].(float64); !isFloat {
		t.Fatalf("1.0 read as %T", fields["whole"])
	}
	if _, isInteger := fields["integer"].(int64); !isInteger {
		t.Fatalf("1 read as %T", fields["integer"])
	}
	request := testRequest()
	request["metadata"] = fields
	if got := buildCode(request, testResult(), nil); got != CodeNumberOutOfRange {
		t.Fatalf("got %s", got)
	}
}

// TestTheBuilderImportsNoSolverValidatorRendererOrClock walks the package's whole import
// closure within the module.
func TestTheBuilderImportsNoSolverValidatorRendererOrClock(t *testing.T) {
	const module = "github.com/packvium/packvium-go/"
	allowed := map[string]bool{
		module + "execution": true, module + "internal/canonicaljson": true,
		module + "internal/jsonvalue": true, module + "internal/pyrepr": true,
	}
	forbidden := map[string]bool{"time": true, "math/rand": true, "net/http": true}
	visited := map[string]bool{}
	pending := []string{module + "artifacts"}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if visited[current] {
			continue
		}
		visited[current] = true
		for _, imported := range packageImports(t, filepath.Join("..", strings.TrimPrefix(current, module))) {
			inModule := strings.HasPrefix(imported, module)
			if forbidden[imported] || (inModule && !allowed[imported]) {
				t.Errorf("%s imports %s", current, imported)
			}
			if inModule && allowed[imported] {
				pending = append(pending, imported)
			}
		}
	}
}

func packageImports(t *testing.T, directory string) []string {
	t.Helper()
	files, err := ioutil.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, file := range files {
		name := file.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range parsed.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			imports = append(imports, path)
		}
	}
	return imports
}

// ----------------------------------------------------------------------------------- exports

func testArtifact(t *testing.T, result map[string]interface{}) map[string]interface{} {
	t.Helper()
	return mustBuild(t, testRequest(), result, reversed())
}

var exporters = map[string]func(interface{}) (string, error){
	"json": ExportJSON, "csv": ExportCSV, "html": ExportWorkOrderHTML,
}

func mustExport(t *testing.T, export func(interface{}) (string, error), artifact interface{}) string {
	t.Helper()
	text, err := export(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestTheJSONExportIsTheCanonicalArtifact(t *testing.T) {
	artifact := testArtifact(t, testResult())
	text := mustExport(t, ExportJSON, artifact)
	if text != mustCanonical(t, artifact) {
		t.Fatal("the JSON export is not the canonical form")
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatal(err)
	}
}

func TestEveryExportRefusesADocumentItCannotRead(t *testing.T) {
	foreign := testArtifact(t, testResult())
	foreign["format"] = "packvium-operational-artifact/v2"
	missing := testArtifact(t, testResult())
	delete(missing, "work_order")
	for name, export := range exporters {
		for _, document := range []interface{}{foreign, []interface{}{"not", "an", "artifact"}, nil} {
			if _, err := export(document); codeOf(err) != CodeUnknownFormat {
				t.Errorf("%s: got %v", name, err)
			}
		}
		if name == "json" {
			continue
		}
		if _, err := export(missing); codeOf(err) != CodeUnknownFormat {
			t.Errorf("%s of a document without its work order: got %v", name, err)
		}
	}
}

// Python would print True, PHP 1, JavaScript true: none of them is the answer.
func TestAValueWithNoSingleRenderingIsRefusedRatherThanPrintedFourWays(t *testing.T) {
	for _, value := range []interface{}{true, 1.5, map[string]interface{}{}} {
		result := testResult()
		container(result)["container_type"] = value
		artifact := mustBuild(t, testRequest(), result, nil)
		for _, name := range []string{"csv", "html"} {
			if _, err := exporters[name](artifact); codeOf(err) != CodeInvalidValue {
				t.Errorf("%s of container_type %v: got %v", name, value, err)
			}
		}
	}
}

// A 1.0 seed in memory is the 1 every other engine reads back from the same bytes.
func TestAnExportDependsOnTheCanonicalBytesNotOnHowGoHoldsThem(t *testing.T) {
	inMemory := testArtifact(t, testResult())
	inMemory["provenance"].(map[string]interface{})["solver"].(map[string]interface{})["seed"] = 7.0
	readBack, err := Parse([]byte(mustCanonical(t, inMemory)))
	if err != nil {
		t.Fatal(err)
	}
	for name, export := range exporters {
		if mustExport(t, export, inMemory) != mustExport(t, export, readBack) {
			t.Errorf("%s differs between the document and its canonical bytes", name)
		}
	}
	if !strings.Contains(mustExport(t, ExportWorkOrderHTML, inMemory), "seed 7</td>") {
		t.Fatal("the seed is not printed as the integer the bytes hold")
	}
}

func TestAHandEditedContainerIndexIsRefusedRatherThanNumbered(t *testing.T) {
	for _, index := range []interface{}{0.5, true, "0", nil} {
		artifact := testArtifact(t, testResult())
		artifact["work_order"].(map[string]interface{})["containers"].([]interface{})[0].(map[string]interface{})["container_index"] = index
		if _, err := ExportWorkOrderHTML(artifact); codeOf(err) != CodeInvalidValue {
			t.Errorf("container_index %v: got %v", index, err)
		}
	}
}

func TestTheCSVHasAHeaderOneRowPerStepInOrderAndOnePerUnplacedItem(t *testing.T) {
	result := testResult()
	result["unpacked_items"] = []interface{}{map[string]interface{}{
		"item_id": "jack#1", "item_type": "jack", "reason": "no_compatible_container_dimensions",
		"details": []interface{}{}, "proof": map[string]interface{}{"level": "proven"},
	}}
	rows := strings.Split(mustExport(t, ExportCSV, testArtifact(t, result)), "\r\n")
	want := []string{
		strings.Join(CSVColumns, ","),
		"step,0,crate,1,tin,LWH,160000,0,0,10,0,0,5,10,10,mm,,",
		"step,0,crate,2,box,LWH,0,0,0,0,0,0,10,10,10,mm,,",
		"unplaced,,,,jack,,,,,,,,,,,,no_compatible_container_dimensions,proven",
		"",
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows\n%q\nwant\n%q", rows, want)
	}
}

func TestACSVFieldIsQuotedOnlyWhenItMustBeAndNeverRewritten(t *testing.T) {
	result := testResult(placement(`a,"b"`, 0, 10), placement("=SUM(A1)", 10, 10), placement("two\nlines", 20, 10),
		placement("cr\rhere", 30, 10), placement("plain 'quote'", 40, 10))
	text := mustExport(t, ExportCSV, mustBuild(t, testRequest(), result, nil))
	for _, fragment := range []string{`,"a,""b""",`, ",=SUM(A1),", ",\"two\nlines\",", ",\"cr\rhere\",", ",plain 'quote',"} {
		if !strings.Contains(text, fragment) {
			t.Errorf("%q not in the CSV", fragment)
		}
	}
}

func TestTheWorkOrderIsSelfContainedEscapedAndListsEveryStep(t *testing.T) {
	result := testResult(placement(`<b>&'"`, 0, 10), placement("tin", 10, 5))
	html := mustExport(t, ExportWorkOrderHTML, mustBuild(t, testRequest(), result, reversed()))
	checks := map[string]bool{
		"starts with the doctype":     strings.HasPrefix(html, "<!DOCTYPE html>\n"),
		"ends with the closing tag":   strings.HasSuffix(html, "</html>\n"),
		"has no script":               !strings.Contains(html, "<script"),
		"loads nothing":               !strings.Contains(html, "http") && !strings.Contains(html, " src="),
		"escapes every character":     strings.Contains(html, "&lt;b&gt;&amp;&#39;&quot;") && !strings.Contains(html, "<b>&'"),
		"has one checkbox per step":   strings.Count(html, "&#9744;") == 2,
		"says the order is a loading": strings.Contains(html, "Order: loading."),
		"names the solver":            strings.Contains(html, "extreme_point"),
		"names the catalog":           strings.Contains(html, "cartons v3"),
		"is ASCII":                    isASCII(html),
	}
	for check, holds := range checks {
		if !holds {
			t.Errorf("the work order %s", check)
		}
	}
}

func TestTheWorkOrderSaysWhenThereIsNoOrderAndWhenEverythingWasPacked(t *testing.T) {
	html := mustExport(t, ExportWorkOrderHTML, mustBuild(t, testRequest(), testResult(), nil))
	if !strings.Contains(html, "Order: unavailable.") || !strings.Contains(html, "<p>Every item was packed.</p>") {
		t.Fatal(html)
	}
}

// The exact page for the small result, line for line, as the reference prints it.
func TestTheWorkOrderIsTheReferencePage(t *testing.T) {
	html := mustExport(t, ExportWorkOrderHTML, testArtifact(t, testResult()))
	lines := strings.Split(html, "\n")
	want := []string{
		"<tr><td>Status</td><td>feasible</td></tr>",
		"<tr><td>Objective</td><td>default</td></tr>",
		"<tr><td>Containers</td><td>1</td></tr>",
		"<tr><td>Score</td><td>[1, 0, 250]</td></tr>",
		"<tr><td>Feasibility</td><td>all_items_packed</td></tr>",
		"<tr><td>Replay</td><td>exact</td></tr>",
		"<tr><td>Solver</td><td>balanced / extreme_point / seed 7</td></tr>",
		"<tr><td>Catalogs</td><td>cartons v3</td></tr>",
		"<tr><td>Packvium</td><td>1.7.0</td></tr>",
		"</table>",
		`<section class="container">`,
		"<h2>Container 1: crate</h2>",
		"<p>Payload 1 g &middot; Gross 2 g &middot; Utilization 0.002000</p>",
		"<p>Order: loading. Follow the steps in sequence.</p>",
		"<table>",
		"<thead><tr><th>Step</th><th>Item</th><th>Orientation</th><th>Position x, y, z (mm)</th><th>Size l &times; w &times; h (mm)</th><th>Done</th></tr></thead>",
		"<tbody>",
		"<tr><td>1</td><td>tin</td><td>LWH</td><td>10, 0, 0</td><td>5 &times; 10 &times; 10</td><td>&#9744;</td></tr>",
		"<tr><td>2</td><td>box</td><td>LWH</td><td>0, 0, 0</td><td>10 &times; 10 &times; 10</td><td>&#9744;</td></tr>",
		"</tbody>", "</table>", "</section>", "<section>", "<h2>Not packed</h2>", "<p>Every item was packed.</p>",
		"</section>", "</body>", "</html>", "",
	}
	if got := lines[10:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestExportsAreDeterministicAndLeaveTheArtifactUntouched(t *testing.T) {
	artifact := testArtifact(t, testResult())
	before := mustCanonical(t, artifact)
	for name, export := range exporters {
		if mustExport(t, export, artifact) != mustExport(t, export, artifact) {
			t.Errorf("%s is not deterministic", name)
		}
	}
	if mustCanonical(t, artifact) != before {
		t.Fatal("an export changed the artifact")
	}
}

func isASCII(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] >= 0x80 {
			return false
		}
	}
	return true
}
