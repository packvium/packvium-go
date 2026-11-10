// Serialization: the JSON contract every engine shares, and what it does with mistakes.
//
// Run it from packvium-go/:
//
//	go run ./examples/serialization
//
// serialization.Pack takes a decoded JSON request and returns the result in the shape all
// the engines return. The same document can be posted to any of them, or piped to the
// command line, and the answer agrees byte for byte with every other engine's.
//
// The parser is strict about values and lenient about keys: a malformed number or an
// unknown objective is refused, while an unknown key is read past. That is why the
// misspelled field below changes nothing -- and why a field this engine does not implement
// is refused by name instead of being ignored like a typo.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/serialization"
)

type object = map[string]interface{}

const requestJSON = `{
  "units": {"length": "mm"},
  "configuration": {
    "objective": "lowest_landed_cost",
    "dimensional_weight_divisor": 5000,
    "dimensional_weight_length_unit": "cm",
    "dimensional_weight_weight_unit": "kg",
    "profile": "balanced",
    "seed": 42,
    "top_k": 2
  },
  "items": [
    {"id": "book", "quantity": 6, "dimensions": {"length": "210", "width": "140", "height": "30"}, "weight": "450 g"},
    {"id": "mug", "quantity": 2, "dimensions": {"length": "100", "width": "100", "height": "120"}, "weight": "380 g",
     "keep_upright": true, "max_top_load": "1 kg"}
  ],
  "containers": [
    {"id": "box-m", "inner_dimensions": {"length": "400", "width": "300", "height": "250"}, "max_payload": "20 kg",
     "cost_minor": 180,
     "rate_table": {"weight_brackets_g": [5000, 10000, 30000], "prices_minor": [890, 1240, 2050],
                    "minimum_charge_minor": 650, "fuel_surcharge_permille": 78}}
  ]
}`

// request returns a fresh decoded copy, so each variant below edits its own document.
func request() object {
	value, err := jsonvalue.DecodeBytes([]byte(requestJSON))
	check(err)
	return value.(object)
}

func main() {
	result, err := serialization.Pack(request())
	check(err)
	containers := result["containers"].([]interface{})
	algorithm := result["algorithm"].(object)
	fmt.Println("status     ", result["status"])
	fmt.Println("score      ", repr(result["score"]), "  <- lexicographic, exact integers, cheapest first")
	fmt.Println("solver     ", algorithm["solver"], "in", algorithm["duration_ms"], "ms")
	types := make([]interface{}, len(containers))
	placed := 0
	for index, container := range containers {
		types[index] = container.(object)["container_type"]
		placed += len(container.(object)["placements"].([]interface{}))
	}
	fmt.Println("containers ", repr(types))
	fmt.Println("placed     ", placed)
	var unplaced []string
	for _, entry := range result["unpacked_items"].([]interface{}) {
		item := entry.(object)
		unplaced = append(unplaced, fmt.Sprintf("(%s, %s)", repr(item["item_id"]), repr(item["reason"])))
	}
	fmt.Println("unplaced   ", "["+strings.Join(unplaced, ", ")+"]")

	first := containers[0].(object)["placements"].([]interface{})[0]
	fmt.Println()
	fmt.Println("one placement, in full:")
	fmt.Println(truncate(indentInWireOrder(first, ""), 400), "...")

	alternatives := result["alternatives"].([]interface{})
	fmt.Println()
	fmt.Println("alternatives:", len(alternatives))
	for _, entry := range alternatives {
		alternative := entry.(object)
		var positions []string
		for _, container := range alternative["containers"].([]interface{}) {
			for _, raw := range container.(object)["placements"].([]interface{}) {
				placement := raw.(object)
				x := placement["position"].(object)["x"].(object)["value"]
				positions = append(positions, fmt.Sprintf("(%s, %s)", repr(placement["item_id"]), repr(x)))
			}
		}
		if len(positions) > 2 {
			positions = positions[:2]
		}
		fmt.Println("   score", repr(alternative["score"]), "first two placements", "["+strings.Join(positions, ", ")+"]")
	}

	fmt.Println()
	typo := request()
	typo["items"].([]interface{})[1].(object)["keep_uprght"] = true
	misspelled, err := serialization.Pack(typo)
	check(err)
	fmt.Println("misspelled field:", misspelled["status"], "-- accepted; the misspelling is invisible to the parser,")
	fmt.Println("                  so `keep_upright` was never applied to the mug")

	badObjective := request()
	badObjective["configuration"].(object)["objective"] = "cheapest"
	if _, err := serialization.Pack(badObjective); err != nil {
		fmt.Println("unknown objective:", truncate(err.Error(), 100))
	}

	refused := "none"
	if !serialization.CurrentlyUnsupported.Empty() {
		refused = fmt.Sprintf("%+v", serialization.CurrentlyUnsupported)
	}
	fmt.Println("fields this engine refuses by name:", refused)

	behind := request()
	behind["items"].([]interface{})[0].(object)["shape_type"] = "convex_hull"
	if err := serialization.RejectUnsupported(behind, serialization.UnsupportedFields{ShapeTypes: []string{"convex_hull"}}); err != nil {
		fmt.Println("  what one looks like, from an engine that is not:", truncate(err.Error(), 110))
	}

	fmt.Println()
	fmt.Println("the CLI takes exactly the document above:  echo '...' | go run ./cmd/packvium")
}

// wireOrder is the field order of a placement as the result contract tabulates it; JSON
// objects have no order of their own, so a human-facing rendering picks one.
var wireOrder = []string{"item_id", "item_type", "position", "dimensions", "orientation", "support_ratio", "top_load",
	"x", "y", "z", "length", "width", "height", "ticks", "value", "unit"}

func rank(key string) int {
	for index, known := range wireOrder {
		if known == key {
			return index
		}
	}
	return len(wireOrder)
}

// indentInWireOrder renders value with two-space indentation, objects in wire order.
func indentInWireOrder(value interface{}, indent string) string {
	inner := indent + "  "
	switch typed := value.(type) {
	case object:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.SliceStable(keys, func(i, j int) bool {
			return rank(keys[i]) < rank(keys[j]) || (rank(keys[i]) == rank(keys[j]) && keys[i] < keys[j])
		})
		members := make([]string, len(keys))
		for index, key := range keys {
			encodedKey, _ := json.Marshal(key)
			members[index] = inner + string(encodedKey) + ": " + indentInWireOrder(typed[key], inner)
		}
		return "{\n" + strings.Join(members, ",\n") + "\n" + indent + "}"
	case string:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	}
	return fmt.Sprint(value)
}

// repr prints a decoded JSON value the way the reference prints its own values.
func repr(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return "'" + typed + "'"
	case []interface{}:
		parts := make([]string, len(typed))
		for index, element := range typed {
			parts[index] = repr(element)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprint(value)
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
