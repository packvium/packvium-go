// Command packvium packs one request: packing JSON in (a file argument, or stdin), result JSON
// out (stdout, or -o FILE). It shares the stdin/stdout contract of every other engine's
// command line, so the conformance runner drives it the same way.
//
// A refused request exits 1 with the refusal on stderr; a usage error exits 2.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"unicode/utf8"

	"github.com/toxakara/packvium-go/internal/jsonvalue"
	"github.com/toxakara/packvium-go/serialization"
)

const (
	refused    = 1
	usageError = 2
)

func main() {
	input, output, err := arguments(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage: packvium [-o OUTPUT] [input]")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(usageError)
	}
	if err := run(input, output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(refused)
	}
}

func arguments(args []string) (input, output string, err error) {
	for index := 0; index < len(args); index++ {
		switch argument := args[index]; argument {
		case "-o", "--output":
			if index+1 == len(args) {
				return "", "", fmt.Errorf("argument %s: expected one argument", argument)
			}
			index++
			output = args[index]
		default:
			if input != "" || (len(argument) > 1 && argument[0] == '-') {
				return "", "", fmt.Errorf("unrecognized arguments: %s", argument)
			}
			input = argument
		}
	}
	return input, output, nil
}

func run(input, output string) error {
	data, err := readInput(input)
	if err != nil {
		return err
	}
	// Malformed UTF-8 is refused rather than replaced, which would silently alter ids.
	if !utf8.Valid(data) {
		return errors.New("input is not valid UTF-8")
	}
	request, err := jsonvalue.DecodeBytes(data)
	if err != nil {
		return err
	}
	result, err := serialization.Pack(request)
	if err != nil {
		return err
	}
	encoded, err := jsonvalue.Indented(result)
	if err != nil {
		return err
	}
	if output == "" {
		_, err = io.WriteString(os.Stdout, encoded+"\n")
		return err
	}
	return ioutil.WriteFile(output, []byte(encoded+"\n"), 0o644)
}

func readInput(input string) ([]byte, error) {
	if input == "" {
		return ioutil.ReadAll(os.Stdin)
	}
	return ioutil.ReadFile(input)
}
