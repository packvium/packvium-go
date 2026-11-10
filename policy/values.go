package policy

import (
	"math/big"
	"sort"
)

// The policy block arrives as a decoded JSON tree: nil, bool, string, int64, *big.Int,
// float64, []interface{} and map[string]interface{}. These helpers read that tree with the
// reference's value semantics.

func object(value interface{}) (map[string]interface{}, bool) {
	raw, ok := value.(map[string]interface{})
	return raw, ok
}

// sortedUnknown is the keys of raw outside known, in code point order.
func sortedUnknown(raw map[string]interface{}, known []string) []string {
	var unknown []string
	for key := range raw {
		if !contains(known, key) {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// nonEmptyString is a value that is a string and not empty.
func nonEmptyString(value interface{}) (string, bool) {
	text, ok := value.(string)
	return text, ok && text != ""
}

// falsy is the reference's truth test: null, false, zero, the empty string and an empty
// array or object are all false.
func falsy(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case bool:
		return !typed
	case int64:
		return typed == 0
	case int:
		return typed == 0
	case *big.Int:
		return typed.Sign() == 0
	case float64:
		return typed == 0
	case string:
		return typed == ""
	case []interface{}:
		return len(typed) == 0
	case map[string]interface{}:
		return len(typed) == 0
	}
	return false
}

// integer is an integer of at least minimum. A boolean is refused, never read as 0 or 1,
// and so is a float, even one with an integral value. An integer past int64 is refused
// with the same message: no rule field can carry it.
func integer(value interface{}, where string, minimum int64) (int64, error) {
	var number int64
	switch typed := value.(type) {
	case int64:
		number = typed
	case int:
		number = int64(typed)
	default:
		return 0, refuse("%s must be an integer >= %d", where, minimum)
	}
	if number < minimum {
		return 0, refuse("%s must be an integer >= %d", where, minimum)
	}
	return number, nil
}
