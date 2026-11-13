package serialization

import (
	"fmt"
	"sort"
	"strings"
)

// UnsupportedFields names public request fields an engine has not implemented, by the scope
// they appear in. The decoder reads the keys it knows and ignores the rest, so without this
// guard an unimplemented field would get an answer computed as if it had never been sent.
type UnsupportedFields struct {
	Request       []string
	Configuration []string
	Item          []string
	Container     []string
	ShapeTypes    []string
}

// CurrentlyUnsupported is what this engine still refuses. container.pallet_overhang_limit is
// reserved by the 1.1.0 contract and refused by every engine until one implements it: a field
// a caller can set and the solver ignores is worse than a refusal.
var CurrentlyUnsupported = UnsupportedFields{Container: []string{"pallet_overhang_limit"}}

// Empty reports whether the engine refuses nothing.
func (u UnsupportedFields) Empty() bool {
	return len(u.Request)+len(u.Configuration)+len(u.Item)+len(u.Container)+len(u.ShapeTypes) == 0
}

// UnsupportedFeatureError refuses a request that uses an unimplemented public field.
type UnsupportedFeatureError struct{ message string }

func (e *UnsupportedFeatureError) Error() string { return e.message }

// RejectUnsupported refuses request if it uses any field in unsupported, naming each one
// once, sorted. O(fields x entries).
func RejectUnsupported(request map[string]interface{}, unsupported UnsupportedFields) error {
	found := map[string]bool{}
	for _, key := range unsupported.Request {
		if _, ok := request[key]; ok {
			found[key] = true
		}
	}
	if configuration, ok := request["configuration"].(map[string]interface{}); ok {
		for _, key := range unsupported.Configuration {
			if _, ok := configuration[key]; ok {
				found["configuration."+key] = true
			}
		}
	}
	collect(request["items"], "item", unsupported.Item, found)
	collect(request["containers"], "container", unsupported.Container, found)
	if items, ok := request["items"].([]interface{}); ok {
		for _, entry := range items {
			item, _ := entry.(map[string]interface{})
			if shape, ok := item["shape_type"].(string); ok && containsString(unsupported.ShapeTypes, shape) {
				found["item.shape_type="+shape] = true
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return &UnsupportedFeatureError{fmt.Sprintf("unsupported_feature: the Go engine does not yet implement %s; "+
		"the request was rejected instead of silently ignoring public fields", strings.Join(names, ", "))}
}

func collect(entries interface{}, scope string, keys []string, found map[string]bool) {
	list, _ := entries.([]interface{})
	for _, entry := range list {
		object, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		for _, key := range keys {
			if _, ok := object[key]; ok {
				found[scope+"."+key] = true
			}
		}
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
