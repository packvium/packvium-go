package validation

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/internal/pyrepr"
)

// The reference builds every issue detail with an f-string, so a detail carries the
// reference's own spelling of whatever it names: None, True, 'quoted', (1, 2, 3), 1e-05.
// These render a JSON value that way, so one issue list compares across engines byte for
// byte.

// pyStr is the reference's str(value): a string as itself, anything else as its repr.
func pyStr(value interface{}) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return pyRepr(value)
}

func pyRepr(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case bool:
		return boolRepr(typed)
	case *big.Int:
		return typed.String()
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return floatRepr(typed)
	case string:
		return pyrepr.String(typed)
	case []interface{}:
		elements := make([]string, len(typed))
		for index, element := range typed {
			elements[index] = pyRepr(element)
		}
		return listOf(elements)
	case *jsonObject:
		pairs := make([]string, len(typed.keys))
		for index, key := range typed.keys {
			pairs[index] = pyrepr.String(key) + ": " + pyRepr(typed.values[key])
		}
		return "{" + strings.Join(pairs, ", ") + "}"
	}
	return "?"
}

func boolRepr(flag bool) string {
	if flag {
		return "True"
	}
	return "False"
}

// floatRepr is the reference's repr(float): the shortest digits that round-trip.
func floatRepr(value float64) string {
	switch {
	case math.IsNaN(value):
		return "nan"
	case math.IsInf(value, 1):
		return "inf"
	case math.IsInf(value, -1):
		return "-inf"
	}
	return jsonvalue.FloatRepr(value)
}

// fixed6 is f"{value:.6f}": both round the exact binary value half to even.
func fixed6(value float64) string { return strconv.FormatFloat(value, 'f', 6, 64) }

func listOf(elements []string) string { return "[" + strings.Join(elements, ", ") + "]" }

// tupleOf is a tuple's spelling; a one-element tuple keeps its trailing comma.
func tupleOf(elements []string) string {
	if len(elements) == 1 {
		return "(" + elements[0] + ",)"
	}
	return "(" + strings.Join(elements, ", ") + ")"
}

func intTuple(values []*big.Int) string {
	elements := make([]string, len(values))
	for index, value := range values {
		elements[index] = value.String()
	}
	return tupleOf(elements)
}

func intList(values []*big.Int) string {
	elements := make([]string, len(values))
	for index, value := range values {
		elements[index] = value.String()
	}
	return listOf(elements)
}

func valueTuple(values []interface{}) string {
	elements := make([]string, len(values))
	for index, value := range values {
		elements[index] = pyRepr(value)
	}
	return tupleOf(elements)
}

func reprList(values []interface{}) string {
	elements := make([]string, len(values))
	for index, value := range values {
		elements[index] = pyRepr(value)
	}
	return listOf(elements)
}
