// Pack a trailer that is already partly loaded.
//
// Run it from packvium-go/:
//
//	go run ./examples/fixed_placements
//
// A trailer arrives at the depot with four pallets of returns already on board. They are
// staying on for the next leg, and nobody is going to unload them to make the plan tidier.
// The depot has fourteen store pallets to add.
//
// fixed_placements describes what is already there: which item, in which container, at
// which position and orientation -- exactly the way a result reports a placement, so a
// placement from an earlier plan can be fixed by quoting it. Fixed items are real cargo:
// they carry weight, count against payload and can support what is stacked on them. The
// engine packs around them, never moves them, and marks them "fixed": true in the result.
//
// A fixed item is also an instance of its item type: quantity counts the whole order, and
// the fixed entries take its first instances.
package main

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/serialization"
	"github.com/packvium/packvium-go/units"
)

type object = map[string]interface{}

// Counted work decides the answer, not the clock (see examples/reproducibility); the time
// limit is a fuse far above what this solve needs. The fixed placements are three across the
// nose, and one turned behind them.
const requestJSON = `{
  "units": {"length": "mm"},
  "configuration": {"time_limit_ms": 60000, "effort_budget": {"max_restarts": 8}},
  "items": [
    {"id": "returns-pallet", "quantity": 4, "weight": "350 kg", "keep_upright": true, "stackable": false,
     "dimensions": {"length": "1200", "width": "800", "height": "1500"}},
    {"id": "store-pallet", "quantity": 14, "weight": "500 kg", "keep_upright": true,
     "dimensions": {"length": "1200", "width": "800", "height": "1100"}}
  ],
  "containers": [{"id": "trailer", "max_payload": "9000 kg",
                  "inner_dimensions": {"length": "7200", "width": "2400", "height": "2400"}}],
  "fixed_placements": [
    {"item_type": "returns-pallet", "container_type": "trailer", "orientation": "LWH", "position": {"x": "0", "y": "0", "z": "0"}},
    {"item_type": "returns-pallet", "container_type": "trailer", "orientation": "LWH", "position": {"x": "0", "y": "800", "z": "0"}},
    {"item_type": "returns-pallet", "container_type": "trailer", "orientation": "LWH", "position": {"x": "0", "y": "1600", "z": "0"}},
    {"item_type": "returns-pallet", "container_type": "trailer", "orientation": "WLH", "position": {"x": "1200", "y": "0", "z": "0"}}
  ]
}`

func solve(change func(object)) (object, error) {
	value, err := jsonvalue.DecodeBytes([]byte(requestJSON))
	check(err)
	request := value.(object)
	if change != nil {
		change(request)
	}
	return serialization.Pack(request)
}

func fourth(request object) object { return request["fixed_placements"].([]interface{})[3].(object) }

func show(result object) {
	trailer := result["containers"].([]interface{})[0].(object)
	var fixed []string
	var added []object
	for _, entry := range trailer["placements"].([]interface{}) {
		placement := entry.(object)
		if placement["fixed"] == true {
			fixed = append(fixed, placement["item_id"].(string))
		} else {
			added = append(added, placement)
		}
	}
	payload, err := units.MustWeight(trailer["payload_weight"].(object)["ticks"].(int64)).Decimal("kg")
	check(err)
	unpacked, _ := result["unpacked_items"].([]interface{})
	fmt.Printf("  status %s, payload %s kg, left out %d\n", result["status"], payload, len(unpacked))
	fmt.Printf("  already on board, unmoved: %s\n", strings.Join(fixed, ", "))
	fmt.Printf("  added: %d store pallets\n", len(added))
	for _, placement := range added {
		if z := placement["position"].(object)["z"].(object)["value"]; z != "0" {
			fmt.Printf("    %s is stacked, at z = %s mm\n", placement["item_id"], z)
		}
	}
}

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", 78))
}

func main() {
	section("1. Packed around the pallets already on board")
	result, err := solve(nil)
	check(err)
	show(result)
	fmt.Println("\n  The returns pallets keep their ids, positions and orientations. They took the\n" +
		"  first four instances of `returns-pallet`, so none are left for the engine to\n" +
		"  place; ask for five and it would place one more.")

	section("2. One pallet was left in the middle of the floor")
	result, err = solve(func(r object) { fourth(r)["position"] = object{"x": "1200", "y": "400", "z": "0"} })
	check(err)
	show(result)
	fmt.Println(`
  The fourth returns pallet stands 400 mm off the side wall, and no pallet fits in
  the strip it leaves. The plan still takes all fourteen store pallets, but it has
  to put one on top of another to do it. Whether that is acceptable is
  your call; the plan shows you the price of not moving that pallet.`)

	section("3. What is on board cannot be where the record says")
	for _, impossible := range []struct {
		label  string
		change func(object)
	}{
		{"two pallets recorded in the same place",
			func(r object) { fourth(r)["position"] = object{"x": "600", "y": "0", "z": "0"} }},
		{"a 1000 kg trailer limit, already exceeded by the returns",
			func(r object) { r["containers"].([]interface{})[0].(object)["max_payload"] = "1000 kg" }},
	} {
		_, err := solve(impossible.change)
		var refusal *domain.InvalidRequestError
		if errors.As(err, &refusal) && refusal.IsFixedPlacementError() {
			fmt.Printf("  %s:\n", impossible.label)
			fmt.Printf("    %s, %s: %s\n", refusal.Reason, refusal.Field, refusal)
		}
	}
	fmt.Println("\n  Nothing is solved when the starting point is impossible: a plan built on a\n" +
		"  collision or an overweight trailer would be wrong before the first pallet moved.\n" +
		"  `cannot_hold` names the fixed set as a whole; the detail says what is wrong.")
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
