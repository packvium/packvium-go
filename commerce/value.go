package commerce

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// Object is a decoded JSON object that remembers the order its keys were first written
// in, the way a Python dict does. Go maps iterate in random order, and validation walks
// object members in document order, so a plain map would make the first reported error
// depend on the run.
type Object struct {
	keys   []string
	values map[string]interface{}
}

// NewObject returns an empty Object ready for Set.
func NewObject() *Object {
	return &Object{values: map[string]interface{}{}}
}

// Set stores a member. A repeated key keeps its first position and takes the new value,
// matching json.loads.
func (object *Object) Set(key string, value interface{}) {
	if _, present := object.values[key]; !present {
		object.keys = append(object.keys, key)
	}
	object.values[key] = value
}

// with is Set that returns the object, so a payload reads in its member order.
func (object *Object) with(key string, value interface{}) *Object {
	object.Set(key, value)
	return object
}

// Get returns a member and whether the key is present at all.
func (object *Object) Get(key string) (interface{}, bool) {
	value, present := object.values[key]
	return value, present
}

// Keys returns the member names in document order.
func (object *Object) Keys() []string {
	return append([]string(nil), object.keys...)
}

// Len is the number of members.
func (object *Object) Len() int {
	return len(object.keys)
}

// lookup treats an absent key and an explicit null alike, as the reference's
// `fields.get(key)` does.
func (object *Object) lookup(key string) interface{} {
	return object.values[key]
}

// Decode parses one JSON document into the value model every function in this package
// accepts: nil, bool, *big.Int for an integer literal, float64 for a literal with a
// fraction or exponent, string, []interface{} and *Object. Integers stay exact at any
// size, which is what keeps a quote over huge weights exact.
func Decode(data []byte) (interface{}, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("invalid JSON: extra data after the document")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder) (interface{}, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	switch typed := token.(type) {
	case json.Delim:
		if typed == '{' {
			return decodeObject(decoder)
		}
		if typed == '[' {
			return decodeArray(decoder)
		}
		return nil, fmt.Errorf("invalid JSON: unexpected %q", rune(typed))
	case json.Number:
		return decodeNumber(string(typed))
	default:
		return typed, nil
	}
}

func decodeObject(decoder *json.Decoder) (interface{}, error) {
	object := NewObject()
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid JSON: %v", err)
		}
		key, isText := token.(string)
		if !isText {
			return nil, fmt.Errorf("invalid JSON: object key is not a string")
		}
		value, err := decodeValue(decoder)
		if err != nil {
			return nil, err
		}
		object.Set(key, value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	return object, nil
}

func decodeArray(decoder *json.Decoder) (interface{}, error) {
	values := []interface{}{}
	for decoder.More() {
		value, err := decodeValue(decoder)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	return values, nil
}

// decodeNumber follows json.loads: a literal without '.', 'e' or 'E' is an exact
// integer, anything else is a binary double (overflowing to an infinity, as float() does).
func decodeNumber(text string) (interface{}, error) {
	if strings.ContainsAny(text, ".eE") {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil && !isRangeError(err) {
			return nil, fmt.Errorf("invalid JSON number %q", text)
		}
		return value, nil
	}
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, fmt.Errorf("invalid JSON number %q", text)
	}
	return value, nil
}

func isRangeError(err error) bool {
	numeric, ok := err.(*strconv.NumError)
	return ok && numeric.Err == strconv.ErrRange
}

// normalize converts the Go values a caller is likely to hold -- maps, native integers,
// json.Number -- into the value model, so the API can be called without Decode. A map is
// read in sorted key order because it has no other stable order.
func normalize(value interface{}) (interface{}, error) {
	switch typed := value.(type) {
	case nil, bool, string, float64, *big.Int:
		return typed, nil
	case *Object:
		return normalizeObject(typed)
	case map[string]interface{}:
		return normalizeMap(typed)
	case []interface{}:
		return normalizeList(typed)
	case []string:
		list := make([]interface{}, len(typed))
		for index, text := range typed {
			list[index] = text
		}
		return list, nil
	case json.Number:
		return decodeNumber(string(typed))
	case float32:
		return float64(typed), nil
	case int:
		return big.NewInt(int64(typed)), nil
	case int32:
		return big.NewInt(int64(typed)), nil
	case int64:
		return big.NewInt(typed), nil
	case uint32:
		return new(big.Int).SetUint64(uint64(typed)), nil
	case uint64:
		return new(big.Int).SetUint64(typed), nil
	case big.Int:
		return new(big.Int).Set(&typed), nil
	}
	return nil, &InputError{Message: fmt.Sprintf("unsupported Go value of type %T", value)}
}

func normalizeObject(object *Object) (interface{}, error) {
	normalized := NewObject()
	for _, key := range object.keys {
		member, err := normalize(object.values[key])
		if err != nil {
			return nil, err
		}
		normalized.Set(key, member)
	}
	return normalized, nil
}

func normalizeMap(members map[string]interface{}) (interface{}, error) {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	normalized := NewObject()
	for _, key := range keys {
		member, err := normalize(members[key])
		if err != nil {
			return nil, err
		}
		normalized.Set(key, member)
	}
	return normalized, nil
}

func normalizeList(values []interface{}) (interface{}, error) {
	normalized := make([]interface{}, len(values))
	for index, entry := range values {
		member, err := normalize(entry)
		if err != nil {
			return nil, err
		}
		normalized[index] = member
	}
	return normalized, nil
}
