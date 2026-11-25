// A test harness for cross-engine conformance; not a supported interface.
//
// Command packvium-revision-probe answers plan-revision and request-admission cases in batch.
//
// stdin is a JSON list of cases; stdout is one JSON line per case, in order:
// {"error": code-or-null, "message": text-or-null, "json": text-or-null}. An "admit" case packs
// a request through the ordinary JSON entry point and answers "admitted", or the refusal with
// its "reason" and "field" as well. A refusal is an outcome, not a failure,
// so the probe exits 0 unless it cannot run at all, and then 255.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/revisions"
	"github.com/packvium/packvium-go/serialization"
)

const fatalStatus = 255

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(stdin io.Reader, stdout, stderr io.Writer) int {
	decoded, err := jsonvalue.Decode(stdin)
	if err != nil {
		return fatal(stderr, err)
	}
	cases, ok := decoded.([]interface{})
	if !ok {
		return fatal(stderr, fmt.Errorf("stdin is not a JSON list of cases"))
	}
	encoder := json.NewEncoder(stdout)
	for _, raw := range cases {
		testCase, ok := raw.(map[string]interface{})
		if !ok {
			return fatal(stderr, fmt.Errorf("a case is not a JSON object"))
		}
		outcome, err := answer(testCase)
		if err != nil {
			return fatal(stderr, err)
		}
		// Encode writes the line's LF; U+2028 and U+2029 are escaped, so a reader splitting
		// lines on them cannot break one.
		if err := encoder.Encode(outcome); err != nil {
			return fatal(stderr, err)
		}
	}
	return 0
}

// answer is one case's outcome, or an error when the case itself cannot be answered.
func answer(testCase map[string]interface{}) (map[string]interface{}, error) {
	if testCase["op"] == "admit" {
		return admit(testCase["request"]), nil
	}
	text, err := revisionAnswer(testCase)
	if refusal, refused := err.(*revisions.Error); refused {
		return map[string]interface{}{"error": refusal.Code, "message": refusal.Message, "json": nil}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"error": nil, "message": nil, "json": text}, nil
}

func revisionAnswer(testCase map[string]interface{}) (string, error) {
	switch testCase["op"] {
	case "root":
		return canonical(revisions.RootRevision(testCase["request"]))
	case "derive":
		return canonical(revisions.DeriveRevision(testCase["parent"], testCase["artifact"], testCase["events"]))
	case "apply":
		return canonical(revisions.ApplyEvents(testCase["request"], testCase["events"]))
	case "verify":
		issues, err := revisions.VerifyRevisionChain(testCase["revisions"], testCase["artifacts"])
		if err != nil {
			return "", err
		}
		listed := make([]interface{}, len(issues))
		for index, issue := range issues {
			listed[index] = map[string]interface{}{"code": issue.Code, "revision": issue.Revision, "detail": issue.Detail}
		}
		return revisions.CanonicalJSON(listed)
	case "digest":
		return revisions.DocumentDigest(testCase["document"])
	}
	return "", fmt.Errorf("unknown op %v", testCase["op"])
}

func canonical(document map[string]interface{}, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return revisions.CanonicalJSON(document)
}

func admit(request interface{}) map[string]interface{} {
	_, err := serialization.Pack(request)
	if err == nil {
		return map[string]interface{}{"error": nil, "message": nil, "json": "admitted"}
	}
	if refusal, refused := err.(*domain.InvalidRequestError); refused {
		return map[string]interface{}{"error": refusal.Code, "message": refusal.Error(), "json": nil,
			"reason": refusal.Reason, "field": refusal.Field}
	}
	return map[string]interface{}{"error": "other", "message": err.Error(), "json": nil}
}

func fatal(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "packvium-revision-probe: %v\n", err)
	return fatalStatus
}
