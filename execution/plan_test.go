package execution

import (
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/internal/jsonvalue"
)

// testdata/reference-cases.json was produced by running each result through the reference
// engine's packvium.execution at v1.3.0: "plan" is canonical_plan_json(build_execution_plan(
// {}, result, loading_orders=...)), "error" the ExecutionPlanError message and
// "canonical_error" the code canonical_plan_json refused the plan with. The cases cover what
// the golden corpus cannot: alternatives, floats, unplaced items, injected orders, integers
// past int64 and 2^53, keys outside the Basic Multilingual Plane and the characters a JSON
// writer may treat specially.
func TestReferenceCases(t *testing.T) {
	data, err := ioutil.ReadFile("testdata/reference-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jsonvalue.DecodeBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	cases := decoded.([]interface{})
	if len(cases) == 0 {
		t.Fatal("no reference cases")
	}
	for _, entry := range cases {
		testCase := entry.(map[string]interface{})
		t.Run(testCase["name"].(string), func(t *testing.T) {
			plan, err := BuildPlan(map[string]interface{}{}, testCase["result"].(map[string]interface{}), loadingOrders(testCase))
			if expected, refused := testCase["error"].(string); refused {
				if _, typed := err.(*PlanError); !typed || err.Error() != expected {
					t.Fatalf("got %v (%T), want PlanError %q", err, err, expected)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := CanonicalPlanJSON(plan)
			if code, refused := testCase["canonical_error"].(string); refused {
				if typed, ok := err.(*PlanError); !ok || typed.Code != code {
					t.Fatalf("got %v (%T), want a PlanError with code %s", err, err, code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if expected := testCase["plan"].(string); canonical != expected {
				t.Fatalf("\n got %s\nwant %s", canonical, expected)
			}
		})
	}
}

func loadingOrders(testCase map[string]interface{}) map[int][]int {
	pairs, ok := testCase["loading_orders"].([]interface{})
	if !ok {
		return nil
	}
	orders := map[int][]int{}
	for _, pair := range pairs {
		entry := pair.([]interface{})
		order := []int{}
		for _, index := range entry[1].([]interface{}) {
			order = append(order, int(index.(int64)))
		}
		orders[int(entry[0].(int64))] = order
	}
	return orders
}

func TestThePlanDoesNotAliasTheResultsLists(t *testing.T) {
	score := []interface{}{int64(0), int64(1)}
	result := map[string]interface{}{"status": "feasible", "score": score, "containers": []interface{}{}}
	plan, err := BuildPlan(nil, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	score[1] = int64(9)
	if got := plan["facts"].(map[string]interface{})["score"].([]interface{})[1]; got != int64(1) {
		t.Fatalf("the plan's score changed with the result's: %v", got)
	}
}

// The adapter has no way to produce a placement, because it has no solver, and no way to
// bless one, because it has no validator; nor may it read a clock or a random source. Checked
// over everything it imports from this module, not only its own files.
func TestItImportsNoSolverValidatorOrClock(t *testing.T) {
	const module = "github.com/packvium/packvium-go/"
	forbidden := map[string]bool{
		module + "algorithm": true, module + "packer": true, module + "validation": true,
		module + "nested": true, module + "serialization": true, module + "constraint": true,
		module + "sequence": true, "time": true, "math/rand": true,
	}
	visited := map[string]bool{}
	pending := []string{module + "execution"}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if visited[current] {
			continue
		}
		visited[current] = true
		for _, imported := range packageImports(t, filepath.Join("..", strings.TrimPrefix(current, module))) {
			if forbidden[imported] {
				t.Errorf("%s imports %s", current, imported)
			}
			if strings.HasPrefix(imported, module) {
				pending = append(pending, imported)
			}
		}
	}
}

func packageImports(t *testing.T, directory string) []string {
	t.Helper()
	packages, err := parser.ParseDir(token.NewFileSet(), directory, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, parsed := range packages {
		for _, file := range parsed.Files {
			for _, spec := range file.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				imports = append(imports, path)
			}
		}
	}
	return imports
}
