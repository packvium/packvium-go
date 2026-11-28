// Package pointshape holds the shape rules a fixed placement shares with a plan revision's
// placement events, so both refuse the same malformed point with the same words.
package pointshape

import (
	"sort"

	"github.com/packvium/packvium-go/internal/canonicaljson"
)

// Axes are a point's members, in order.
var Axes = []string{"x", "y", "z"}

// Orientations are the six rotation codes.
var Orientations = []string{"LWH", "LHW", "WLH", "WHL", "HLW", "HWL"}

// IsOrientation reports whether value is one of the six codes.
func IsOrientation(value interface{}) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	for _, code := range Orientations {
		if text == code {
			return true
		}
	}
	return false
}

// OrientationRule is the orientation rule as the request-error tables word a closed
// vocabulary.
func OrientationRule() string {
	spelled := make([]interface{}, len(Orientations))
	for index, code := range Orientations {
		spelled[index] = code
	}
	return "must be one of " + canonicaljson.Spelling(spelled)
}

// Refusal builds an error from its message and the pointer of the bad value.
type Refusal func(message, field string) error

// RequirePoint refuses a point that is not an object of x, y and z measures; units are the
// length parser's, so only the shape is checked here.
func RequirePoint(point interface{}, where, field string, refuse Refusal) error {
	members, ok := point.(map[string]interface{})
	if !ok {
		return refuse(where+" is a point object", field)
	}
	if unknown := UnknownKeys(members, Axes); len(unknown) > 0 {
		return refuse(where+" cannot carry "+canonicaljson.Spelling(unknown), field)
	}
	for _, axis := range Axes {
		value, present := members[axis]
		if !present {
			continue
		}
		switch value.(type) {
		case nil, bool, []interface{}:
			return refuse(where+"."+axis+" is a measure", field+"/"+axis)
		}
	}
	return nil
}

// UnknownKeys are the keys of record outside allowed, sorted, as a JSON list.
func UnknownKeys(record map[string]interface{}, allowed []string) []interface{} {
	known := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		known[key] = true
	}
	var names []string
	for key := range record {
		if !known[key] {
			names = append(names, key)
		}
	}
	sort.Strings(names)
	unknown := make([]interface{}, len(names))
	for index, name := range names {
		unknown[index] = name
	}
	return unknown
}
