package validation

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// The documents ValidateResult reads, held the way the reference's json.loads holds them:
// integers of any size (*big.Int), floats (float64), strings, booleans, nil, lists
// ([]interface{}) and objects that keep their key order (*jsonObject), because several checks
// walk an object's keys in the order they were written.

// jsonObject is a JSON object with its keys in document order. A repeated key keeps its first
// position and its last value, as the reference's dict does.
type jsonObject struct {
	keys   []string
	values map[string]interface{}
}

func newJSONObject() *jsonObject { return &jsonObject{values: map[string]interface{}{}} }

func (o *jsonObject) set(key string, value interface{}) {
	if _, present := o.values[key]; !present {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// clone is a deep copy, so an expanded result never writes into the caller's document.
func (o *jsonObject) clone() *jsonObject {
	copied := &jsonObject{keys: append([]string(nil), o.keys...), values: make(map[string]interface{}, len(o.values))}
	for key, value := range o.values {
		copied.values[key] = cloneValue(value)
	}
	return copied
}

func cloneValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case *jsonObject:
		return typed.clone()
	case []interface{}:
		copied := make([]interface{}, len(typed))
		for index, element := range typed {
			copied[index] = cloneValue(element)
		}
		return copied
	}
	return value
}

// decodeDocument reads one JSON document in order. Text that is not JSON is the reference
// failing to read it: a shape refusal.
func decodeDocument(text []byte) (value interface{}, err error) {
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.UseNumber()
	value, err = decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, shapeRefusal("extra data after the JSON document")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder) (interface{}, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, shapeRefusal("JSONDecodeError: " + err.Error())
	}
	switch typed := token.(type) {
	case json.Delim:
		if typed == '[' {
			return decodeList(decoder)
		}
		return decodeObject(decoder)
	case json.Number:
		return decodeNumber(string(typed))
	}
	return token, nil
}

func decodeList(decoder *json.Decoder) (interface{}, error) {
	list := []interface{}{}
	for decoder.More() {
		element, err := decodeValue(decoder)
		if err != nil {
			return nil, err
		}
		list = append(list, element)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, shapeRefusal("JSONDecodeError: " + err.Error())
	}
	return list, nil
}

func decodeObject(decoder *json.Decoder) (interface{}, error) {
	object := newJSONObject()
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, shapeRefusal("JSONDecodeError: " + err.Error())
		}
		key, _ := token.(string)
		value, err := decodeValue(decoder)
		if err != nil {
			return nil, err
		}
		object.set(key, value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, shapeRefusal("JSONDecodeError: " + err.Error())
	}
	return object, nil
}

// decodeNumber is an exact integer for an integer literal and the nearest double otherwise,
// infinite past the double range, as the reference's float() reads it.
func decodeNumber(text string) (interface{}, error) {
	if !strings.ContainsAny(text, ".eE") {
		integer, ok := new(big.Int).SetString(text, 10)
		if !ok {
			return nil, shapeRefusal("JSONDecodeError: bad number " + text)
		}
		return integer, nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil && !math.IsInf(value, 0) {
		return nil, shapeRefusal("JSONDecodeError: bad number " + text)
	}
	return value, nil
}

// ------------------------------------------------------------------ reading values

func isDict(value interface{}) bool { _, ok := value.(*jsonObject); return ok }

func isList(value interface{}) bool { _, ok := value.([]interface{}); return ok }

// has is the reference's `key in value` for an object; anything else does not hold it.
func has(value interface{}, key string) bool {
	object, ok := value.(*jsonObject)
	if !ok {
		return false
	}
	_, present := object.values[key]
	return present
}

// get is the reference's value.get(key, fallback): the fallback only when the key is absent,
// so a key present as null still reads as null. A non-object has no get.
func get(value interface{}, key string, fallback interface{}) interface{} {
	object, ok := value.(*jsonObject)
	if !ok {
		panic(shapeRefusal("expected an object holding " + strconv.Quote(key) + ", got " + kind(value)))
	}
	if element, present := object.values[key]; present {
		return element
	}
	return fallback
}

// member is the reference's value[key].
func member(value interface{}, key string) interface{} {
	object, ok := value.(*jsonObject)
	if !ok {
		panic(shapeRefusal("missing " + strconv.Quote(key)))
	}
	element, present := object.values[key]
	if !present {
		panic(shapeRefusal("missing " + strconv.Quote(key)))
	}
	return element
}

// items is something the reference iterates as a list; only a list is admitted.
func items(value interface{}, what string) []interface{} {
	list, ok := value.([]interface{})
	if !ok {
		panic(shapeRefusal(what + " must be a list, got " + kind(value)))
	}
	return list
}

func text(value interface{}, what string) string {
	typed, ok := value.(string)
	if !ok {
		panic(shapeRefusal(what + " must be a string, got " + kind(value)))
	}
	return typed
}

// integer is an integer the reference does arithmetic on. A boolean is the reference's int;
// a float would carry it into float arithmetic this validator does not follow, so it is refused.
func integer(value interface{}, what string) *big.Int {
	if number := intInstance(value); number != nil {
		return number
	}
	panic(shapeRefusal(what + " must be an integer, got " + kind(value)))
}

func optionalInteger(value interface{}, what string) *big.Int {
	if value == nil {
		return nil
	}
	return integer(value, what)
}

// intInstance is the value when the reference's isinstance(value, int) holds, which a
// boolean satisfies; nil otherwise.
func intInstance(value interface{}) *big.Int {
	switch typed := value.(type) {
	case *big.Int:
		return typed
	case bool:
		if typed {
			return big.NewInt(1)
		}
		return big.NewInt(0)
	}
	return nil
}

// strictInt is the value when it is an integer and not a boolean.
func strictInt(value interface{}) *big.Int {
	typed, _ := value.(*big.Int)
	return typed
}

// pyInt is the reference's int(value): a number truncates, a string parses as a base-10
// integer with optional underscores between digits.
func pyInt(value interface{}, what string) *big.Int {
	if number := intInstance(value); number != nil {
		return number
	}
	switch typed := value.(type) {
	case float64:
		if !math.IsInf(typed, 0) && !math.IsNaN(typed) {
			truncated, _ := big.NewFloat(math.Trunc(typed)).Int(nil)
			return truncated
		}
	case string:
		if parsed := parseIntLiteral(stripSpace(typed)); parsed != nil {
			return parsed
		}
	}
	panic(shapeRefusal(what + ": int() cannot convert " + kind(value)))
}

func parseIntLiteral(literal string) *big.Int {
	sign := ""
	if strings.HasPrefix(literal, "+") || strings.HasPrefix(literal, "-") {
		sign, literal = literal[:1], literal[1:]
	}
	if literal == "" || strings.HasPrefix(literal, "_") || strings.HasSuffix(literal, "_") ||
		strings.Contains(literal, "__") {
		return nil
	}
	digits := strings.Replace(literal, "_", "", -1)
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return nil
		}
	}
	parsed, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil
	}
	if sign == "-" {
		parsed.Neg(parsed)
	}
	return parsed
}

// truthy is the reference's truth value of a JSON value.
func truthy(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case *big.Int:
		return typed.Sign() != 0
	case float64:
		return typed != 0
	case string:
		return typed != ""
	case []interface{}:
		return len(typed) > 0
	case *jsonObject:
		return len(typed.keys) > 0
	}
	return true
}

func isText(value interface{}, expected string) bool {
	typed, ok := value.(string)
	return ok && typed == expected
}

// numberOf is a JSON number, or a boolean, as an exact rational; ok is false for anything
// else. NaN and the infinities have no rational: nan reports so, and inf is their sign.
func numberOf(value interface{}) (rational *big.Rat, inf int, nan, ok bool) {
	switch typed := value.(type) {
	case bool, *big.Int:
		return new(big.Rat).SetInt(intInstance(typed)), 0, false, true
	case float64:
		switch {
		case math.IsNaN(typed):
			return nil, 0, true, true
		case math.IsInf(typed, 1):
			return nil, 1, false, true
		case math.IsInf(typed, -1):
			return nil, -1, false, true
		}
		return new(big.Rat).SetFloat64(typed), 0, false, true
	}
	return nil, 0, false, false
}

// compareNumbers orders two numbers exactly, as the reference compares an int with a float;
// ordered is false when either is NaN.
func compareNumbers(left, right interface{}) (order int, ordered bool) {
	leftValue, leftInf, leftNaN, _ := numberOf(left)
	rightValue, rightInf, rightNaN, _ := numberOf(right)
	if leftNaN || rightNaN {
		return 0, false
	}
	if leftInf != 0 || rightInf != 0 {
		return compareSigns(leftInf, rightInf), true
	}
	return leftValue.Cmp(rightValue), true
}

func compareSigns(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	}
	return 0
}

// equals is the reference's ==: a boolean equals its integer, an integer equals the float of
// the same value, lists compare element-wise and objects regardless of key order.
func equals(left, right interface{}) bool {
	_, _, _, leftNumber := numberOf(left)
	_, _, _, rightNumber := numberOf(right)
	if leftNumber || rightNumber {
		if !leftNumber || !rightNumber {
			return false
		}
		order, ordered := compareNumbers(left, right)
		return ordered && order == 0
	}
	switch typed := left.(type) {
	case nil:
		return right == nil
	case string:
		other, ok := right.(string)
		return ok && typed == other
	case []interface{}:
		other, ok := right.([]interface{})
		if !ok || len(other) != len(typed) {
			return false
		}
		for index := range typed {
			if !equals(typed[index], other[index]) {
				return false
			}
		}
		return true
	case *jsonObject:
		other, ok := right.(*jsonObject)
		if !ok || len(other.keys) != len(typed.keys) {
			return false
		}
		for _, key := range typed.keys {
			element, present := other.values[key]
			if !present || !equals(typed.values[key], element) {
				return false
			}
		}
		return true
	}
	return false
}

func equalsInt(value interface{}, number *big.Int) bool { return equals(value, number) }

// compare is the reference's ordering of what it sorts or compares: numbers with numbers,
// strings with strings by code point, lists element-wise. Anything else is the reference's
// TypeError, a shape refusal.
func compare(left, right interface{}) int {
	_, _, _, leftNumber := numberOf(left)
	_, _, _, rightNumber := numberOf(right)
	if leftNumber && rightNumber {
		order, ordered := compareNumbers(left, right)
		if !ordered {
			return 0
		}
		return order
	}
	if leftText, ok := left.(string); ok {
		if rightText, ok := right.(string); ok {
			return strings.Compare(leftText, rightText)
		}
	}
	leftList, leftOK := left.([]interface{})
	rightList, rightOK := right.([]interface{})
	if leftOK && rightOK {
		for index, element := range leftList {
			if index >= len(rightList) {
				return 1
			}
			if !equals(element, rightList[index]) {
				return compare(element, rightList[index])
			}
		}
		return compareSigns(len(leftList), len(rightList))
	}
	panic(shapeRefusal("cannot order " + kind(left) + " against " + kind(right)))
}

// sortValues sorts by the reference's ordering; the sort is stable, as the reference's is.
func sortValues(values []interface{}) {
	sort.SliceStable(values, func(left, right int) bool { return compare(values[left], values[right]) < 0 })
}

// hashKey is the identity the reference hashes a set member or dict key by, so 1, 1.0 and
// True share one. A list or an object is unhashable.
func hashKey(value interface{}) string {
	if rational, inf, nan, ok := numberOf(value); ok {
		switch {
		case nan:
			return "nan"
		case inf != 0:
			return "inf" + strconv.Itoa(inf)
		case rational.IsInt():
			return "n" + rational.Num().String()
		}
		return "r" + rational.String()
	}
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return "s" + typed
	}
	panic(shapeRefusal(kind(value) + " is unhashable"))
}

func kind(value interface{}) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case *big.Int:
		return "an integer"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []interface{}:
		return "a list"
	}
	return "an object"
}

// ------------------------------------------------------------------ an ordered dict

// orderedMap is insertion-ordered and keyed the way the reference's dict is, by hashKey, so 1
// and True are one key and setting an existing key replaces its value in place. Keys keep the
// JSON value they came from, for printing.
type orderedMap struct {
	keys   []interface{}
	values []interface{}
	index  map[string]int
}

func newOrderedMap() *orderedMap { return &orderedMap{index: map[string]int{}} }

func (m *orderedMap) set(key, value interface{}) {
	hashed := hashKey(key)
	if position, present := m.index[hashed]; present {
		m.values[position] = value
		return
	}
	m.index[hashed] = len(m.keys)
	m.keys = append(m.keys, key)
	m.values = append(m.values, value)
}

func (m *orderedMap) get(key interface{}) (interface{}, bool) {
	position, present := m.index[hashKey(key)]
	if !present {
		return nil, false
	}
	return m.values[position], true
}

func (m *orderedMap) position(key interface{}) (int, bool) {
	position, present := m.index[hashKey(key)]
	return position, present
}

func (m *orderedMap) has(key interface{}) bool { _, present := m.index[hashKey(key)]; return present }

func (m *orderedMap) count() int { return len(m.keys) }

// countOf is the value as a count, zero when absent.
func (m *orderedMap) countOf(key interface{}) int64 {
	value, present := m.get(key)
	if !present {
		return 0
	}
	return value.(int64)
}
