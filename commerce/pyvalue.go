package commerce

import (
	"fmt"
	"math"
	"math/big"
	"strings"
)

// Policy predicates compare raw JSON values, and the contract defines that comparison
// as Python's: true == 1, 1 == 1.0 exactly, lists element-wise, `in` over a string is a
// substring test, and an operand pair Python refuses raises its TypeError.

// pythonEqual is Python's == over the decoded value model.
func pythonEqual(left, right interface{}) bool {
	if leftNumber, ok := numeric(left); ok {
		rightNumber, isNumber := numeric(right)
		return isNumber && numbersEqual(leftNumber, rightNumber)
	}
	switch typed := left.(type) {
	case nil:
		return right == nil
	case string:
		other, ok := right.(string)
		return ok && typed == other
	case []interface{}:
		other, ok := right.([]interface{})
		return ok && listsEqual(typed, other)
	case *Object:
		other, ok := right.(*Object)
		return ok && objectsEqual(typed, other)
	}
	return false
}

// numeric widens a bool to the int it is in Python; a float stays a float.
func numeric(value interface{}) (interface{}, bool) {
	switch typed := value.(type) {
	case bool:
		if typed {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	case *big.Int, float64:
		return typed, true
	}
	return nil, false
}

// numbersEqual compares an int and a float by exact value, as Python does, rather than
// by rounding the int to a double.
func numbersEqual(left, right interface{}) bool {
	leftInt, leftIsInt := left.(*big.Int)
	rightInt, rightIsInt := right.(*big.Int)
	switch {
	case leftIsInt && rightIsInt:
		return leftInt.Cmp(rightInt) == 0
	case leftIsInt:
		return intEqualsFloat(leftInt, right.(float64))
	case rightIsInt:
		return intEqualsFloat(rightInt, left.(float64))
	}
	return left.(float64) == right.(float64)
}

func intEqualsFloat(whole *big.Int, float float64) bool {
	if math.IsInf(float, 0) || math.IsNaN(float) {
		return false
	}
	return new(big.Float).SetInt(whole).Cmp(big.NewFloat(float)) == 0
}

func listsEqual(left, right []interface{}) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !pythonEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func objectsEqual(left, right *Object) bool {
	if left.Len() != right.Len() {
		return false
	}
	for _, key := range left.keys {
		other, present := right.Get(key)
		if !present || !pythonEqual(left.values[key], other) {
			return false
		}
	}
	return true
}

// pythonContains is Python's `needle in container`.
func pythonContains(container, needle interface{}) (bool, error) {
	switch typed := container.(type) {
	case string:
		word, ok := needle.(string)
		if !ok {
			return false, fmt.Errorf("TypeError: 'in <string>' requires string as left operand, not %s",
				pythonTypeName(needle))
		}
		return strings.Contains(typed, word), nil
	case []interface{}:
		for _, entry := range typed {
			if pythonEqual(entry, needle) {
				return true, nil
			}
		}
		return false, nil
	case *Object:
		return objectHasKey(typed, needle)
	}
	return false, fmt.Errorf("TypeError: argument of type '%s' is not iterable", pythonTypeName(container))
}

// objectHasKey is dict membership: an unhashable needle is a TypeError, and a non-string
// needle can never equal a JSON object's string keys.
func objectHasKey(object *Object, needle interface{}) (bool, error) {
	switch typed := needle.(type) {
	case []interface{}, *Object:
		return false, fmt.Errorf("TypeError: unhashable type: '%s'", pythonTypeName(typed))
	case string:
		_, present := object.Get(typed)
		return present, nil
	}
	return false, nil
}

func pythonTypeName(value interface{}) string {
	switch value.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case *big.Int:
		return "int"
	case float64:
		return "float"
	case string:
		return "str"
	case []interface{}:
		return "list"
	case *Object:
		return "dict"
	}
	return fmt.Sprintf("%T", value)
}
