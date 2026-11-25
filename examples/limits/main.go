// Fleet and vehicle limits: what may go where, and how much of it.
//
// Run it from packvium-go/:
//
//	go run ./examples/limits
//
// A same-day courier has one refrigerated van and a few cargo bikes. Every rule below is a
// request field, so it holds in every Packvium engine and travels with the request:
//
//   - obstacles on a container: space inside it that cargo may not use -- here the fridge
//     unit under the roof, and the two wheel arches as one obstacle made of two boxes
//     (additional_boxes), since an arch on each side is one thing to the person loading.
//   - max_items on a container: a cap on how many items it takes, whatever their size -- a
//     bike's rack holds four parcels however small they are.
//   - tags and tag_limits: at most one item tagged dangerous_goods in the van.
//   - eligible_container_tags on an item: frozen goods only go in a container tagged
//     refrigerated.
//   - max_containers in the configuration: how many vehicles the whole plan may use.
package main

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/serialization"
)

type object = map[string]interface{}

// Counted work decides the answer, not the clock (see examples/reproducibility); the time
// limit is a fuse far above what this solve needs.
const requestJSON = `{
  "units": {"length": "mm"},
  "configuration": {"time_limit_ms": 60000, "effort_budget": {"max_restarts": 8}},
  "items": [
    {"id": "frozen-tote", "quantity": 3, "weight": "15 kg",
     "dimensions": {"length": "600", "width": "400", "height": "300"},
     "eligible_container_tags": ["refrigerated"]},
    {"id": "lithium-battery", "quantity": 3, "weight": "12 kg",
     "dimensions": {"length": "400", "width": "300", "height": "250"},
     "tags": ["dangerous_goods"]},
    {"id": "parcel", "quantity": 24, "weight": "8 kg",
     "dimensions": {"length": "500", "width": "400", "height": "400"}}
  ],
  "containers": [
    {"id": "cargo-bike", "quantity": 3, "cost_minor": 900, "max_items": 4, "max_payload": "100 kg",
     "inner_dimensions": {"length": "1000", "width": "800", "height": "900"}},
    {"id": "reefer-van", "quantity": 1, "cost_minor": 4000,
     "inner_dimensions": {"length": "1800", "width": "1400", "height": "1200"},
     "tags": ["refrigerated"],
     "tag_limits": {"dangerous_goods": 1},
     "obstacles": [
       {"id": "fridge-unit", "origin": {"x": "0", "y": "0", "z": "800"},
        "dimensions": {"length": "400", "width": "1400", "height": "400"}},
       {"id": "wheel-arches", "origin": {"x": "700", "y": "0", "z": "0"},
        "dimensions": {"length": "800", "width": "200", "height": "350"},
        "additional_boxes": [{"origin": {"x": "700", "y": "1200", "z": "0"},
                              "dimensions": {"length": "800", "width": "200", "height": "350"}}]}
     ]}
  ]
}`

func solve(change func(object)) object {
	value, err := jsonvalue.DecodeBytes([]byte(requestJSON))
	check(err)
	request := value.(object)
	if change != nil {
		change(request)
	}
	result, err := serialization.Pack(request)
	check(err)
	return result
}

func containers(request object) []interface{} { return request["containers"].([]interface{}) }

func show(result object) {
	fmt.Printf("  status: %s\n", result["status"])
	for _, entry := range result["containers"].([]interface{}) {
		container := entry.(object)
		contents := map[string]int{}
		for _, placement := range container["placements"].([]interface{}) {
			contents[placement.(object)["item_type"].(string)]++
		}
		listed := make([]string, 0, len(contents))
		for _, name := range sortedKeys(contents) {
			listed = append(listed, fmt.Sprintf("%d %s", contents[name], name))
		}
		fmt.Printf("    %-14s %s\n", container["id"], strings.Join(listed, ", "))
	}
	// One line per kind of refusal: a reader needs "five parcels, no room", not five lines.
	refusals := map[string]int{}
	unpacked, _ := result["unpacked_items"].([]interface{})
	for _, entry := range unpacked {
		item := entry.(object)
		key := strings.Join([]string{item["item_type"].(string), item["reason"].(string),
			item["proof"].(object)["level"].(string)}, "\x00")
		refusals[key]++
	}
	for _, key := range sortedKeys(refusals) {
		parts := strings.Split(key, "\x00")
		fmt.Printf("    left out: %d %s, %s (%s)\n", refusals[key], parts[0], parts[1], parts[2])
	}
}

// sortedKeys orders keys as Python sorts tuples, the order the shared output uses: NUL joins the parts, and it sorts
// before every character an id or a reason contains.
func sortedKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func count(result object, containerID, itemType string) int {
	total := 0
	for _, entry := range result["containers"].([]interface{}) {
		container := entry.(object)
		if container["id"] != containerID {
			continue
		}
		for _, placement := range container["placements"].([]interface{}) {
			if placement.(object)["item_type"] == itemType {
				total++
			}
		}
	}
	return total
}

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", 78))
}

func main() {
	section("1. The plan")
	plan := solve(nil)
	show(plan)
	fmt.Println("\n  All three frozen totes are in the van, because nothing else is refrigerated. Only\n" +
		"  one battery rides in the van; the other two go by bike. The bike takes four items,\n" +
		"  its `max_items`, although there is room and payload for more.")

	section("2. What the obstacles cost")
	openVan := solve(func(r object) { delete(containers(r)[1].(object), "obstacles") })
	fmt.Printf("  parcels in the van with the fridge unit and arches: %d\n", count(plan, "reefer-van#1", "parcel"))
	fmt.Printf("  parcels in the van if it were an empty box:        %d\n", count(openVan, "reefer-van#1", "parcel"))
	fmt.Println(`
  An obstacle is exact space, not a hint. Model what is really there, or the plan
  will put a parcel where the wheel arch is.`)

	section("3. `max_containers`: the plan may use one vehicle")
	show(solve(func(r object) { r["configuration"].(object)["max_containers"] = int64(1) }))
	fmt.Println("\n  The cap is honoured, and what does not fit is listed rather than silently sent in\n" +
		"  a vehicle you did not allow. The reason is `observed`, not `proven`: the search\n" +
		"  found no room for these, which is not a claim that no arrangement has room.")

	section("4. `eligible_container_tags`: the van is off the road today")
	show(solve(func(r object) { r["containers"] = containers(r)[:1] }))
	fmt.Println("\n  The frozen totes cannot go anywhere, and their reason is `proven`: no container in\n" +
		"  the request carries the tag they need, which is a fact about the request rather\n" +
		"  than about how far the search got. The rest is `observed`: three bikes of four\n" +
		"  items each were all the request offered, and the search ran out of them.")
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
