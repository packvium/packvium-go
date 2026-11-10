// Command packvium-commerce drives the exported commerce API from stdin to stdout: one
// case object ({"operation", "document", "request"}) in, one canonical result document
// out. It calls only the public commerce functions, so what the conformance harness
// compares is the exported API.
//
// Exit codes follow the other ports' adapters: 1 with "input error: ..." for a malformed
// request, 2 for an unknown operation, and 255 for any other failure. A rejection is an
// answer, so it exits 0.
package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/toxakara/packvium-go/commerce"
)

const (
	inputErrorStatus       = 1
	unknownOperationStatus = 2
	fatalStatus            = 255
)

type operation func(document, request interface{}) (commerce.Result, error)

var operations = map[string]operation{
	"quote":                commerce.Quote,
	"evaluate_policy":      commerce.EvaluatePolicy,
	"catalog_version_info": commerce.CatalogVersionInfo,
}

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(stdin io.Reader, stdout, stderr io.Writer) int {
	input, err := ioutil.ReadAll(stdin)
	if err != nil {
		return fatal(stderr, err)
	}
	decoded, err := commerce.Decode(input)
	if err != nil {
		return fatal(stderr, err)
	}
	testCase, isObject := decoded.(*commerce.Object)
	if !isObject {
		return fatal(stderr, fmt.Errorf("the case is not a JSON object"))
	}
	name, _ := lookup(testCase, "operation").(string)
	call, known := operations[name]
	if !known {
		fmt.Fprintf(stderr, "unknown operation '%s'\n", name)
		return unknownOperationStatus
	}
	document, hasDocument := testCase.Get("document")
	request, hasRequest := testCase.Get("request")
	if !hasDocument || !hasRequest {
		return fatal(stderr, fmt.Errorf("the case needs both a document and a request"))
	}
	result, err := call(document, request)
	if err != nil {
		if _, isInput := err.(*commerce.InputError); isInput {
			fmt.Fprintf(stderr, "input error: %v\n", err)
			return inputErrorStatus
		}
		return fatal(stderr, err)
	}
	canonical, err := commerce.CanonicalJSON(result)
	if err != nil {
		return fatal(stderr, err)
	}
	if _, err := io.WriteString(stdout, canonical); err != nil {
		return fatal(stderr, err)
	}
	return 0
}

func lookup(object *commerce.Object, key string) interface{} {
	value, _ := object.Get(key)
	return value
}

func fatal(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "fatal error: %v\n", err)
	return fatalStatus
}
