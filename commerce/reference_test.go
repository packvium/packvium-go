package commerce

import (
	"io/ioutil"
	"testing"
)

// testdata/reference-cases.json was produced by running each case through the
// reference engine's packvium.commerce at v1.0.0. Every entry carries exactly one of:
// "result" (the canonical JSON answer), "input_error" (the CommerceInputError message)
// or "failure" (an error the reference raises outside its input-error type).
func TestReferenceCases(t *testing.T) {
	data, err := ioutil.ReadFile("testdata/reference-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	cases := decoded.([]interface{})
	if len(cases) == 0 {
		t.Fatal("no reference cases")
	}
	operations := map[string]func(document, request interface{}) (Result, error){
		"quote":                Quote,
		"evaluate_policy":      EvaluatePolicy,
		"catalog_version_info": CatalogVersionInfo,
	}
	for _, entry := range cases {
		testCase := entry.(*Object)
		name := testCase.lookup("name").(string)
		t.Run(name, func(t *testing.T) {
			call := operations[testCase.lookup("operation").(string)]
			result, err := call(testCase.lookup("document"), testCase.lookup("request"))
			checkReferenceOutcome(t, testCase, result, err)
		})
	}
}

func checkReferenceOutcome(t *testing.T, testCase *Object, result Result, err error) {
	t.Helper()
	if expected, present := testCase.Get("result"); present {
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		observed, encodeErr := CanonicalJSON(result)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if observed != expected.(string) {
			t.Fatalf("\nexpected %s\nobserved %s", expected, observed)
		}
		return
	}
	if expected, present := testCase.Get("input_error"); present {
		if _, isInput := err.(*InputError); !isInput {
			t.Fatalf("expected input error %q, got %v (%T)", expected, err, err)
		}
		if err.Error() != expected.(string) {
			t.Fatalf("\nexpected %s\nobserved %s", expected, err)
		}
		return
	}
	expected := testCase.lookup("failure").(string)
	if err == nil {
		t.Fatalf("expected failure %q, got a result", expected)
	}
	if _, isInput := err.(*InputError); isInput {
		t.Fatalf("expected a failure outside the input-error type, got input error %v", err)
	}
	if err.Error() != expected {
		t.Fatalf("\nexpected %s\nobserved %s", expected, err)
	}
}
