// Command packvium-artifact-probe is the batch probe the suite's artifact conformance
// (conformance/artifacts/run.py) drives, answering the protocol of its probe.php.
//
// stdin is a JSON list of cases; stdout is one JSON line per case, in order:
// {"error": code-or-null, "json": ..., "csv": ..., "html": ...}. A "build" case names a
// request file, a result file and loading orders keyed by container index; an "export" case
// names a stored artifact. A refusal is an outcome, not a failure, so the probe exits 0 unless
// it cannot run at all, and then 255.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"

	"github.com/packvium/packvium-go/artifacts"
)

const fatalStatus = 255

type probeCase struct {
	Kind          string           `json:"kind"`
	RequestPath   string           `json:"request_path"`
	ResultPath    string           `json:"result_path"`
	ArtifactPath  string           `json:"artifact_path"`
	LoadingOrders map[string][]int `json:"loading_orders"`
}

type outcome struct {
	Error *string `json:"error"`
	JSON  *string `json:"json"`
	CSV   *string `json:"csv"`
	HTML  *string `json:"html"`
}

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(stdin io.Reader, stdout, stderr io.Writer) int {
	var cases []probeCase
	if err := json.NewDecoder(stdin).Decode(&cases); err != nil {
		return fatal(stderr, err)
	}
	encoder := json.NewEncoder(stdout)
	for _, testCase := range cases {
		answer, err := answer(testCase)
		if err != nil {
			return fatal(stderr, err)
		}
		// Encode writes the line's LF; U+2028 and U+2029 are escaped, so a reader splitting
		// lines on them cannot break one.
		if err := encoder.Encode(answer); err != nil {
			return fatal(stderr, err)
		}
	}
	return 0
}

// answer is one case's outcome, or an error when the case itself cannot be read.
func answer(testCase probeCase) (outcome, error) {
	artifact, err := artifactOf(testCase)
	if refusal, refused := err.(*artifacts.Error); refused {
		return refusedWith(refusal), nil
	}
	if err != nil {
		return outcome{}, err
	}
	exports := [3]func(interface{}) (string, error){artifacts.ExportJSON, artifacts.ExportCSV, artifacts.ExportWorkOrderHTML}
	var texts [3]string
	for index, export := range exports {
		texts[index], err = export(artifact)
		if refusal, refused := err.(*artifacts.Error); refused {
			return refusedWith(refusal), nil
		}
		if err != nil {
			return outcome{}, err
		}
	}
	return outcome{JSON: &texts[0], CSV: &texts[1], HTML: &texts[2]}, nil
}

func artifactOf(testCase probeCase) (interface{}, error) {
	if testCase.Kind == "export" {
		text, err := ioutil.ReadFile(testCase.ArtifactPath)
		if err != nil {
			return nil, err
		}
		return artifacts.Parse(text)
	}
	request, err := ioutil.ReadFile(testCase.RequestPath)
	if err != nil {
		return nil, err
	}
	result, err := ioutil.ReadFile(testCase.ResultPath)
	if err != nil {
		return nil, err
	}
	orders, err := loadingOrders(testCase.LoadingOrders)
	if err != nil {
		return nil, err
	}
	return artifacts.FromJSON(request, result, orders)
}

func loadingOrders(byName map[string][]int) (map[int][]int, error) {
	orders := make(map[int][]int, len(byName))
	for name, order := range byName {
		index, err := strconv.Atoi(name)
		if err != nil {
			return nil, fmt.Errorf("loading order key %q is not a container index", name)
		}
		orders[index] = order
	}
	return orders, nil
}

func refusedWith(refusal *artifacts.Error) outcome {
	code := refusal.Code
	return outcome{Error: &code}
}

func fatal(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "packvium-artifact-probe: %v\n", err)
	return fatalStatus
}
