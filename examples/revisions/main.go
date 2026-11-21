// Replan a half-loaded job without losing what was already done, or the record of why.
//
// Run it from packvium-go/:
//
//	go run ./examples/revisions
//
// A plan is approved, and the dock starts loading. Then something changes: a slab is missing
// from the shelf, and a cube that is already in the tote must stay exactly where it is. The
// next plan has to keep the cube in place and pack around it, and anyone auditing the job
// later has to see what changed, in what order, and which approved plan each change was
// recorded against.
//
// A plan revision is that record. It is append-only and hash-chained: each revision names its
// parent and the artifact it replaced by SHA-256, and carries the request the events produce.
// The replan is an ordinary solve of that request, and every Packvium engine computes the same
// revision bytes from the same inputs.
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/packvium/packvium-go/artifacts"
	"github.com/packvium/packvium-go/revisions"
	"github.com/packvium/packvium-go/serialization"
)

type object = map[string]interface{}

// Counted work decides where the search stops, not a clock: a plan the clock stopped could
// not be replayed, and its revision would say `replay: not_guaranteed`. The time limit is only
// a fuse, far above what this needs even on a slow or emulated host.
const requestJSON = `{
  "units": {"length": "mm"},
  "configuration": {"effort_budget": {"max_search_nodes": 20000}, "time_limit_ms": 60000},
  "items": [
    {"id": "cube", "quantity": 4, "weight": "1 kg", "dimensions": {"length": "100", "width": "100", "height": "100"}},
    {"id": "slab", "quantity": 2, "weight": "2 kg", "dimensions": {"length": "200", "width": "100", "height": "50"}}
  ],
  "containers": [{"id": "tote", "quantity": 2, "inner_dimensions": {"length": "200", "width": "200", "height": "150"}}]
}`

func main() {
	request := decode(requestJSON)

	section("1. The approved plan, and the revision that records it")
	root, err := revisions.RootRevision(request)
	check(err)
	approved := approve(root["request"])
	firstStep := approved["plan"].(object)["containers"].([]interface{})[0].(object)["steps"].([]interface{})[0].(object)["placement"].(object)
	fmt.Printf("  revision %v, parent %s\n", root["revision"], spellParent(root["parent"]))
	fmt.Printf("  first step: a %v at x=%v in container 0\n", firstStep["item_type"],
		firstStep["position_ticks"].(object)["x"])
	fmt.Println(`
  The root records nothing against the request. It exists so the first change has a
  parent to name.`)

	section("2. What happened on the dock, recorded against that plan")
	loaded := solve(root["request"])["containers"].([]interface{})[0].(object)["placements"].([]interface{})[0].(object)
	position := object{}
	for _, axis := range []string{"x", "y", "z"} {
		position[axis] = loaded["position"].(object)[axis].(object)["value"]
	}
	events := []interface{}{
		object{"sequence": int64(1), "type": "placement_locked", "placement": object{
			"item_type": "cube", "container_type": "tote", "container_instance": int64(1),
			"position": position, "orientation": loaded["orientation"]}},
		object{"sequence": int64(2), "type": "item_missing", "item_type": "slab", "quantity": int64(1)},
	}
	revision, err := revisions.DeriveRevision(root, approved, events)
	check(err)
	derived := revision["request"].(object)
	fmt.Printf("  revision %v, parent %s...\n", revision["revision"], revision["parent"].(string)[:23])
	record := revision["approved"].(object)
	fmt.Printf("  approved artifact %s..., replay %v\n", record["artifact"].(string)[:23],
		record["replay"].(object)["level"])
	fmt.Printf("  slabs still to pack: %v\n", derived["items"].([]interface{})[1].(object)["quantity"])
	fmt.Printf("  fixed placements:    %d\n", len(derived["fixed_placements"].([]interface{})))
	fmt.Println(`
  The events are applied to the request, not to the result: one slab fewer, and the
  cube becomes a fixed placement. The revision names its parent and the artifact it
  replaces by SHA-256 over their RFC 8785 bytes, so every engine computes the same
  digest.`)

	section("3. The replan keeps the cube where it is")
	replanned := solve(derived)
	var fixed []string
	placed := 0
	for _, raw := range replanned["containers"].([]interface{}) {
		container := raw.(object)
		placements := container["placements"].([]interface{})
		placed += len(placements)
		for _, entry := range placements {
			if placement := entry.(object); placement["fixed"] == true {
				fixed = append(fixed, fmt.Sprintf("%v in %v", placement["item_id"], container["id"]))
			}
		}
	}
	fmt.Printf("  fixed in the answer: %s\n", strings.Join(fixed, ", "))
	fmt.Printf("  items placed:        %d\n", placed)
	fmt.Println(`
  An ordinary solve of the derived request, with nothing remembered from the first
  one. The fixed cube is marked ` + "`fixed: true`" + `, and the validator refuses any answer
  that moves it.`)

	section("4. An audit that notices tampering, and a refusal instead of a guess")
	chain := []interface{}{root, revision}
	fmt.Printf("  intact chain: %s\n", codes(revisions.VerifyRevisionChain(chain, []interface{}{nil, approved})))
	edited := decode(canonical(chain)).([]interface{})
	edited[1].(object)["request"].(object)["items"].([]interface{})[1].(object)["quantity"] = int64(2)
	fmt.Printf("  edited chain: %s\n", codes(revisions.VerifyRevisionChain(edited, nil)))
	_, err = revisions.DeriveRevision(revision, approve(derived), []interface{}{
		object{"sequence": int64(3), "type": "item_missing", "item_type": "pallet", "quantity": int64(1)}})
	if refusal, refused := err.(*revisions.Error); refused {
		fmt.Printf("  a pallet that was never requested: refused with %s\n", refusal.Code)
	}
	fmt.Println(`
  An edited request no longer equals what its parent's request and its own events
  produce, and any later revision would stop naming it as a parent. An event that
  contradicts the request is refused by name rather than applied as a best guess.`)
}

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", 78))
}

// solve packs a copy of the request, as a JSON caller would send it.
func solve(request interface{}) object {
	result, err := serialization.Pack(decode(canonical(request)))
	check(err)
	return result
}

// approve solves a request and wraps the answer as the artifact the dock works from.
func approve(request interface{}) object {
	artifact, err := artifacts.Build(request, solve(request), nil)
	check(err)
	return artifact
}

func codes(issues []revisions.Issue, err error) string {
	check(err)
	if len(issues) == 0 {
		return "no issues"
	}
	names := make([]string, len(issues))
	for index, issue := range issues {
		names[index] = issue.Code
	}
	return strings.Join(names, ", ")
}

func spellParent(parent interface{}) string {
	if parent == nil {
		return "nil"
	}
	return parent.(string)
}

func canonical(value interface{}) string {
	text, err := revisions.CanonicalJSON(value)
	check(err)
	return text
}

func decode(text string) interface{} {
	value, err := artifacts.Parse([]byte(text))
	check(err)
	return value
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
