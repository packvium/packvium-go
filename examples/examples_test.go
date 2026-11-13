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

// The Python examples' output at v1.1.0, kept beside the programs so the test runs wherever
// the module does, with no checkout of the suite repository.
const referenceDirectory = "testdata"

// languageWording lists, per example, each Go line that differs from the reference only in
// naming its language, with the reference line it stands for. Everything else must match
// exactly; a listed line that is no longer printed fails the test as stale.
var languageWording = map[string]map[string]string{
	"basic": {
		"the full result as a plain map is what examples/serialization explores:": "the full result as a plain dict is what serialization.py explores:",
	},
	"serialization": {
		"  what one looks like, from an engine that is not: unsupported_feature: the Go engine does not yet implement item.shape_type=convex_hull; the request was rejecte": "  what one looks like, from an engine that is not: unsupported_feature: the Python engine does not yet implement item.shape_type=convex_hull; the request was rej",
		"fields this engine refuses by name: map[container:[pallet_overhang_limit]]":                                                                                        "fields this engine refuses by name: {'container': ('pallet_overhang_limit',)}",
		"the CLI takes exactly the document above:  echo '...' | go run ./cmd/packvium":                                                                                     "the CLI takes exactly the document above:  echo '...' | python3 -m packvium",
	},
	"extensions": {
		"rule everyone must obey belongs in the request as data, not in a type.": "rule everyone must obey belongs in the request as data, not in a class.",
	},
	"commerce": {
		"   nothing matched, so the shipment is allowed with no citation: nil": "   nothing matched, so the shipment is allowed with no citation: None",
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
