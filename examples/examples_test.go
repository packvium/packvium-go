// Package examples holds runnable programs, one per directory, each the Go counterpart of a
// reference engine example. The test runs every one and compares its output with what the
// reference example printed at the same release.
package examples

import (
	"bytes"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The Python examples' output at v1.6.0, kept beside the programs so the test runs wherever
// the module does, with no checkout of the suite repository.
const referenceDirectory = "testdata"

// languageWording lists, per example, each Go line that differs from the reference only in
// naming its language, with the reference line it stands for. Everything else must match
// exactly; a listed line that is no longer printed fails the test as stale.
var languageWording = map[string]map[string]string{
	"artifacts": {
		"  contains a script or an external resource: false":                            "  contains a script or an external resource: False",
		"  The canonical form is RFC 8785, so every Packvium engine builds these exact": "  The canonical form is RFC 8785, so PHP, Rust and JavaScript build these exact",
	},
	"basic": {
		"the full result as a plain map is what examples/serialization explores:": "the full result as a plain dict is what serialization.py explores:",
	},
	"serialization": {
		"  sending one anyway: unsupported_feature: the Go engine does not yet implement container.pallet_overhang_limit; the reque": "  sending one anyway: unsupported_feature: the Python engine does not yet implement container.pallet_overhang_limit; the r",
		"fields this engine refuses by name: map[container:[pallet_overhang_limit]]":                                                 "fields this engine refuses by name: {'container': ('pallet_overhang_limit',)}",
		"the CLI takes exactly the document above:  echo '...' | go run ./cmd/packvium":                                              "the CLI takes exactly the document above:  echo '...' | python3 -m packvium",
	},
	"extensions": {
		"Neither rule above exists on the wire. Hand the same request to another Packvium": "Neither rule above exists on the wire. Hand the same request to the Rust or",
		"engine and you get the unrestricted answer -- which is exactly why a":             "JavaScript engine and you get the unrestricted answer -- which is exactly why a",
		"rule everyone must obey belongs in the request as data, not in a type.":           "rule everyone must obey belongs in the request as data, not in a class.",
	},
	"commerce": {
		"   nothing matched, so the shipment is allowed with no citation: nil": "   nothing matched, so the shipment is allowed with no citation: None",
	},
	"execution": {
		// The Python text of sections 2 and 4 speaks for four engines and names its
		// JavaScript example; this text speaks for every Packvium engine instead.
		"  loading order from geometry the plan builder never sees, so without one it says": "  loading order from geometry this adapter never sees, so without one it says",
		"  plan builder re-derived behind your back.":                                       "  adapter re-derived behind your back.",
		"4. One plan, every engine, the same bytes":                                         "4. One plan, four engines, the same bytes",
		"  Hand the same *result* to the plan builder of any Packvium engine and it emits":  "  Hand the same *result* to all four adapters and they emit the same bytes. That",
		"  the same bytes. That is stricter than the packing contract, and it can be: a":    "  is stricter than the packing contract, and it can be: a plan is derived from a",
		"  plan is derived from a result, so there is nothing left to differ about.":        "  result, so there is nothing left to differ about.",
		"  It does not follow that every engine packing the same *request* agrees. Python,": "  It does not follow that four engines packing the same *request* agree. Python",
		"  PHP, Java, .NET and Go return identical results at the same version; Rust and":   "  and PHP are held to identical placements and do produce this exact form; Rust",
		"  JavaScript return a valid answer at or above the objective floor, so their plan": "  and JavaScript are held to a valid answer at or above the objective floor, and",
		"  for this request can be a different length. Whose packing is better is a":        "  `examples/execution.mjs` prints 1280 bytes here rather than 1877 for that",
		"  question the plan never answers.":                                                "  reason. Whose packing is better is a question the plan never answers.",
		"  preserved: true":                                                                 "  preserved: True",
		"  the approved plan is untouched: true":                                            "  the approved plan is untouched: True",
		"  a lock the solve cannot honour -> preserved=false, 1 lock reported back":         "  a lock the solve cannot honour -> preserved=False, 1 lock reported back",
	},
	"errors": {
		"  Both are errors, so a handler that ends with a plain `err != nil` still turns":     "  Both are `ValueError`s, so a handler that ends with `except ValueError` still turns",
		"  them into a 422 -- but they have no Reason or Field to highlight, only a message.": "  them into a 422 -- but they have no `reason` or `field` to highlight, only a message.",
	},
	"rebalancing": {
		"  still a valid packing: true":                                  "  still a valid packing: True",
		"  pallet#1 30 kg (10 items)  improved: false":                   "  pallet#1 30 kg (10 items)  improved: False",
		"  rule: `Moves` is empty and the packing comes back as it was.": "  rule: `moves` is empty and the packing comes back as it was.",
	},
	"reproducibility": {
		"  `time_limit`, and `ReplayLevel` refuses to promise a replay.": "  `time_limit`, and `replay_level` refuses to promise a replay.",
	},
	"revisions": {
		"  revision 0, parent nil": "  revision 0, parent None",
	},
	"intelligence": {
		"  minimum_confidence=0.9 -> nil":                                                 "  minimum_confidence=0.9 -> None",
		"  met. `nil` is the whole answer: there is no low-confidence proposal to weigh.": "  met. `None` is the whole answer: there is no low-confidence proposal to weigh.",
	},
}

func TestEveryExamplePrintsItsRecordedAnswer(t *testing.T) {
	names := exampleNames(t)
	if len(names) == 0 {
		t.Fatal("no examples found")
	}
	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expected := referenceOutput(t, name)
			got := normalise(t, name, run(t, name))
			if got != expected {
				t.Fatalf("output differs from the reference example\n%s", firstDifference(got, expected))
			}
		})
	}
}

func exampleNames(t *testing.T) []string {
	entries, err := ioutil.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if _, err := os.Stat(filepath.Join(entry.Name(), "main.go")); entry.IsDir() && err == nil {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func referenceOutput(t *testing.T, name string) string {
	output, err := ioutil.ReadFile(filepath.Join(referenceDirectory, name+".out"))
	if err != nil {
		t.Fatalf("reference output of %s: %v", name, err)
	}
	return string(output)
}

func run(t *testing.T, name string) string {
	command := exec.Command("go", "run", "./"+name)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("go run ./%s: %v\n%s", name, err, stderr.String())
	}
	return stdout.String()
}

// timing is the reference runner's own mask: elapsed time is a fact about the host, not
// the packing.
var timing = regexp.MustCompile(`("(?:duration_ms|elapsed_ms)":\s*)\d+|(\bin )\d+( ms\b)`)

func normalise(t *testing.T, name, output string) string {
	wording := languageWording[name]
	lines := strings.Split(timing.ReplaceAllString(output, "${1}${2}<masked>${3}"), "\n")
	replaced := 0
	for index, line := range lines {
		if reference, ok := wording[line]; ok {
			lines[index] = reference
			replaced++
		}
	}
	if replaced != len(wording) {
		t.Fatalf("%s no longer prints every line listed in languageWording (%d of %d)", name, replaced, len(wording))
	}
	return strings.Join(lines, "\n")
}

func firstDifference(got, want string) string {
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for index := 0; index < len(gotLines) || index < len(wantLines); index++ {
		var g, w string
		if index < len(gotLines) {
			g = gotLines[index]
		}
		if index < len(wantLines) {
			w = wantLines[index]
		}
		if g != w {
			return "line " + strconv.Itoa(index+1) + "\n got: " + g + "\nwant: " + w
		}
	}
	return "outputs differ only in trailing content"
}
