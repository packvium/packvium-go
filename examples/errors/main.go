// Turn a refused request into a useful answer, the way a web handler would.
//
// Run it from packvium-go/:
//
//	go run ./examples/errors
//
// A packing service takes JSON from a caller it does not control. Most failures are the
// caller's: a quantity of zero, a negative width, a unit nobody has heard of. Those are
// returned as a *domain.InvalidRequestError before anything is solved, with three fields a
// program can use:
//
//   - Reason: one of a closed set (missing_field, wrong_type, below_minimum, ...), the thing
//     to branch on;
//   - Field: a JSON Pointer to the bad value in the request the caller sent, such as
//     /items/0/quantity;
//   - Error(): "invalid_request: <field>: <detail>", the same text in every Packvium engine,
//     fit for a log line.
//
// A refusal of items already in place that cannot be where the request says is the same type
// with Code "invalid_fixed_placement" (IsFixedPlacementError). And a request that is valid
// but does not fit completely is not an error at all: it is a result with unpacked_items.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/serialization"
)

type object = map[string]interface{}

const orderJSON = `{
  "units": {"length": "mm"},
  "configuration": {"time_limit_ms": 60000, "effort_budget": {"max_restarts": 4}},
  "items": [
    {"id": "mug", "quantity": 2, "weight": "380 g",
     "dimensions": {"length": "100", "width": "100", "height": "120"}},
    {"id": "teapot", "quantity": 1, "weight": "1.2 kg",
     "dimensions": {"length": "220", "width": "160", "height": "180"}}
  ],
  "containers": [{"id": "box", "inner_dimensions": {"length": "300", "width": "250", "height": "200"}}]
}`

// order is a fresh decoded copy, so each variant below edits its own document.
func order() object {
	value, err := jsonvalue.DecodeBytes([]byte(orderJSON))
	check(err)
	return value.(object)
}

// resolve is every value along a JSON Pointer, root first, so a message can name the item by
// its id.
func resolve(document interface{}, pointer string) []interface{} {
	values := []interface{}{document}
	for _, token := range strings.Split(pointer, "/")[1:] {
		key := strings.NewReplacer("~1", "/", "~0", "~").Replace(token)
		switch current := values[len(values)-1].(type) {
		case []interface{}:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(current) {
				return values
			}
			values = append(values, current[index])
		case object:
			value, ok := current[key]
			if !ok {
				return values
			}
			values = append(values, value)
		default:
			return values
		}
	}
	return values
}

// where turns /items/1/dimensions/width into `item "teapot", dimensions/width`. The pointer
// is exact but written for programs; people know their items by id, so the nearest object
// that has one names the place, and the rest of the pointer says which value in it.
func where(request object, pointer string) string {
	tokens := strings.Split(pointer, "/")[1:]
	values := resolve(request, pointer)
	for depth := len(values) - 1; depth > 0; depth-- {
		value, ok := values[depth].(object)
		if !ok {
			continue
		}
		if id, named := value["id"]; named {
			kind := strings.TrimRight(tokens[depth-2], "s")
			place := fmt.Sprintf(`%s "%v"`, kind, id)
			if rest := strings.Join(tokens[depth:], "/"); rest != "" {
				place += ", " + rest
			}
			return place
		}
	}
	if pointer == "" {
		return "the request"
	}
	return pointer
}

// field is one member of a JSON object, so a response keeps the order it was written in.
type field struct {
	name  string
	value interface{}
}

// handle is what a POST /pack endpoint would do: a status code and a JSON body.
func handle(request object) (int, []field) {
	result, err := serialization.Pack(request)
	var refusal *domain.InvalidRequestError
	if errors.As(err, &refusal) {
		body := []field{{"code", refusal.Code}, {"reason", refusal.Reason}, {"field", refusal.Field}}
		// The fixed-placement detail names the physical problem (a collision, too much
		// weight), which is what a dock operator needs to hear.
		if refusal.IsFixedPlacementError() {
			return 422, append(body, field{"message", "The items already loaded cannot stay as recorded: " + refusal.Detail + "."})
		}
		return 422, append(body, field{"message", "Please check " + where(request, refusal.Field) + ": " + refusal.Detail + "."})
	}
	check(err)
	// Not an error: the caller gets the plan, and a clear list of what is not in it.
	leftOut := []string{}
	if unpacked, ok := result["unpacked_items"].([]interface{}); ok {
		for _, entry := range unpacked {
			leftOut = append(leftOut, entry.(object)["item_id"].(string))
		}
	}
	return 200, []field{{"status", result["status"]}, {"boxes", len(result["containers"].([]interface{}))},
		{"left_out", leftOut}}
}

// encode spells a body as Python's json.dumps does: ", " and ": " between members.
func encode(body []field) string {
	members := make([]string, len(body))
	for index, member := range body {
		members[index] = jsonText(member.name) + ": " + jsonText(member.value)
	}
	return "{" + strings.Join(members, ", ") + "}"
}

func jsonText(value interface{}) string {
	if list, ok := value.([]string); ok {
		parts := make([]string, len(list))
		for index, element := range list {
			parts[index] = jsonText(element)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	check(encoder.Encode(value))
	return strings.TrimSuffix(buffer.String(), "\n")
}

func show(label string, request object) {
	status, body := handle(request)
	fmt.Printf("  %s\n", label)
	fmt.Printf("    HTTP %d  %s\n", status, encode(body))
}

func broken(change func(object)) object {
	request := order()
	change(request)
	return request
}

func items(request object) []interface{} { return request["items"].([]interface{}) }

func itemAt(request object, index int) object { return items(request)[index].(object) }

func dimensionsOf(request object, index int) object {
	return itemAt(request, index)["dimensions"].(object)
}

func configuration(request object) object { return request["configuration"].(object) }

func firstContainer(request object) object { return request["containers"].([]interface{})[0].(object) }

// onBoard is a teapot turned (WLH: its width along the box's length) so the mugs still fit
// beside it.
func onBoard() object {
	return object{"item_type": "teapot", "container_type": "box", "orientation": "WLH",
		"position": object{"x": "0", "y": "0", "z": "0"}}
}

func fixedAs(entries ...object) func(object) {
	return func(request object) {
		list := make([]interface{}, len(entries))
		for index, entry := range entries {
			list[index] = entry
		}
		request["fixed_placements"] = list
	}
}

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", 78))
}

func main() {
	section("1. Refused before solving: one reason, one pointer, one message")
	show("a valid order", order())
	show("quantity 0", broken(func(r object) { itemAt(r, 0)["quantity"] = int64(0) }))
	show("a negative width", broken(func(r object) { dimensionsOf(r, 1)["width"] = "-160" }))
	show("a missing height", broken(func(r object) { delete(dimensionsOf(r, 1), "height") }))
	show("a float where a measure belongs", broken(func(r object) { dimensionsOf(r, 1)["width"] = 160.5 }))
	show("the same container id twice", broken(func(r object) {
		r["containers"] = append(r["containers"].([]interface{}), order()["containers"].([]interface{})[0])
	}))
	show("an unknown solver profile", broken(func(r object) { configuration(r)["solver_profile"] = "turbo" }))
	fmt.Println(`
  A float is refused as a measure on purpose: binary floating point cannot hold most
  decimal lengths exactly, and every length here is exact. Send "160.5" as a string.
  When several values are wrong, the first is reported, in a fixed order every
  engine shares: units, configuration, items, containers, fixed placements.`)

	section("2. Items already in place that cannot be there")
	show("a teapot already in the box, packed around", broken(fixedAs(onBoard())))
	hanging := onBoard()
	hanging["position"] = object{"x": "200"}
	show("a teapot recorded at x = 200 mm, hanging out of a 300 mm box", broken(fixedAs(hanging)))
	show("a mug and the teapot recorded in the same place", broken(fixedAs(onBoard(),
		object{"item_type": "mug", "container_type": "box", "orientation": "LWH"})))
	unknownOrientation := onBoard()
	unknownOrientation["orientation"] = "XYZ"
	show("an orientation code that does not exist", broken(fixedAs(unknownOrientation)))
	fmt.Println("\n  `cannot_hold` means the fixed set is well formed but is not a valid packing on its\n" +
		"  own, and `field` is the whole list. `malformed` points at the one bad value.")

	section("3. Not an error: a valid order that does not fit")
	show("a teapot too tall for any box", broken(func(r object) { dimensionsOf(r, 1)["height"] = "450" }))
	fmt.Println("\n  The caller asked a fair question and got an answer: the rest of the order is\n" +
		"  packed, and `unpacked_items` says what is not and why. Retrying it will not help,\n" +
		"  and neither will treating it as a 4xx.")

	section("4. Two refusals that do not carry a pointer")
	for _, refused := range []struct {
		label  string
		change func(object)
	}{
		{"an unknown objective", func(r object) { configuration(r)["objective"] = "cheapest" }},
		{"an unknown door", func(r object) { firstContainer(r)["access_directions"] = []interface{}{"rear"} }},
	} {
		_, err := serialization.Pack(broken(refused.change))
		var refusal *domain.InvalidRequestError
		switch {
		case errors.As(err, &refusal):
			fmt.Printf("  %s: InvalidRequestError\n", refused.label)
		case err != nil:
			fmt.Printf("  %s: %T: %s\n", refused.label, err, truncate(err.Error(), 70))
		}
	}
	fmt.Println("\n  Both are errors, so a handler that ends with a plain `err != nil` still turns\n" +
		"  them into a 422 -- but they have no Reason or Field to highlight, only a message.")
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
