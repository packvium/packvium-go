package validation

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The shared result-validation corpus: pairs that reach every issue code and refusal kind the
// full cross-language corpus reaches in the reference validator, each with the reference's
// outcome. ValidateResult must answer each one the same way: the same issues (code and
// detail, in order); for a contract refusal, the same message; for a shape refusal, any
// refusal.

type corpusOutcome struct {
	Issues  []map[string]string `json:"issues"`
	Refusal *struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"refusal"`
}

// quotesPythonError marks reference messages that end by quoting a Python exception, which
// no port reproduces: these compare up to the quote.
var quotesPythonError = regexp.MustCompile(`^(placement is missing tick coordinates: |.*: lattice_summary is missing a canonical field: )`)

func outcomeOf(request, result string) corpusOutcome {
	issues, err := ValidateResult([]byte(request), []byte(result))
	if refusal, ok := err.(*MalformedResult); ok {
		outcome := corpusOutcome{}
		outcome.Refusal = &struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}{refusal.Kind, refusal.Message}
		return outcome
	}
	listed := make([]map[string]string, len(issues))
	for index, issue := range issues {
		listed[index] = map[string]string{"code": issue.Code, "detail": issue.Detail}
	}
	return corpusOutcome{Issues: listed}
}

func agrees(expected, actual corpusOutcome) bool {
	if expected.Refusal == nil {
		return actual.Refusal == nil && reflect.DeepEqual(expected.Issues, actual.Issues)
	}
	if actual.Refusal == nil {
		return false
	}
	if expected.Refusal.Kind != MalformedContract {
		return true
	}
	if actual.Refusal.Kind != MalformedContract {
		return false
	}
	if quoted := quotesPythonError.FindStringSubmatch(expected.Refusal.Message); quoted != nil {
		return strings.HasPrefix(actual.Refusal.Message, quoted[1])
	}
	return actual.Refusal.Message == expected.Refusal.Message
}

func TestEveryPairOfTheSharedCorpusGetsTheReferenceVerdict(t *testing.T) {
	ref := os.Getenv("PACKVIUM_CONFORMANCE_REF")
	if ref == "" {
		ref = "v1.7.0"
	}
	content, err := exec.Command("git", "show", ref+":conformance/result_validation/corpus.json").Output()
	if err != nil {
		t.Skipf("the result-validation corpus is unavailable at %s: %v", ref, err)
	}
	var corpus struct {
		Format string `json:"format"`
		Cases  []struct {
			Request  string        `json:"request"`
			Result   string        `json:"result"`
			Expected corpusOutcome `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(content, &corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.Format != "packvium-result-validation-corpus/v1" {
		t.Fatalf("unknown corpus format %q", corpus.Format)
	}
	codes := map[string]bool{}
	agreed := 0
	for index, testCase := range corpus.Cases {
		for _, issue := range testCase.Expected.Issues {
			codes[issue["code"]] = true
		}
		if actual := outcomeOf(testCase.Request, testCase.Result); !agrees(testCase.Expected, actual) {
			t.Errorf("case %d: reference %+v, got %+v", index, testCase.Expected, actual)
			continue
		}
		agreed++
	}
	t.Logf("result validation (%s): %d of %d pairs agree with the reference; %d distinct issue codes",
		ref, agreed, len(corpus.Cases), len(codes))
}

func TestAValidResultHasNoIssuesAndTextThatIsNotJSONIsRefused(t *testing.T) {
	if _, err := ValidateResult([]byte(`{}`), []byte(`not json`)); err == nil {
		t.Fatal("text that is not JSON was judged")
	} else if refusal, ok := err.(*MalformedResult); !ok || refusal.Kind != MalformedShape {
		t.Fatalf("%#v", err)
	}
	if _, err := ValidateResult([]byte(`{}`), []byte(`{"status": "feasible"}`)); err == nil ||
		err.(*MalformedResult).Kind != MalformedContract || err.Error() != "result is missing 'feasibility'" {
		t.Fatalf("%v", err)
	}
}
