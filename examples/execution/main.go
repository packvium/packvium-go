// Turn a packing result into instructions someone can follow on a dock.
//
// Run it from packvium-go/:
//
//	go run ./examples/execution
//
// serialization.Pack answers where every box goes. That answer is not yet a work order: it
// does not say what to lift first, and it does not separate what the solver decided from what
// a screen should say.
//
// The execution plan is that second document. It is derived from an already validated
// result -- it calls no solver and no validator.
// Anything it could decide on its own would be a decision made twice.
//
// It also has no way, on its own, for the operator standing there to say the printer must go
// in the corner. Sections 5 and 6 add that: an operator lock, which re-solves through the
// ordinary engine and comes back as a second plan beside the approved one.
//
// Every Packvium engine builds this example's load and emits the plan in the same
// byte-identical canonical form.
package main

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/packvium/packvium-go/execution"
	"github.com/packvium/packvium-go/execution/locks"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/serialization"
)

type object = map[string]interface{}

// One crate, a printer that must stay upright, four toner cartridges, and a pallet jack that
// was never going to fit. The last one is deliberate: an execution plan has to say what is
// not going on the truck as clearly as what is. time_limit_ms is a safety fuse, not a
// target -- nothing in this scene comes close to it.
const requestJSON = `{
  "units": {"length": "mm"},
  "configuration": {"objective": "default", "solver_profile": "balanced", "seed": 42, "time_limit_ms": 60000},
  "items": [
    {"id": "printer", "quantity": 1, "weight": "9 kg", "keep_upright": true,
     "dimensions": {"length": "420", "width": "340", "height": "260"}},
    {"id": "toner", "quantity": 4, "weight": "900 g",
     "dimensions": {"length": "180", "width": "120", "height": "100"}},
    {"id": "pallet-jack", "quantity": 1, "weight": "80 kg",
     "dimensions": {"length": "1200", "width": "550", "height": "1200"}}
  ],
  "containers": [
    {"id": "crate", "quantity": 1, "max_payload": "30 kg",
     "inner_dimensions": {"length": "600", "width": "400", "height": "400"}}
  ]
}`

var rule = strings.Repeat("=", 78)

func main() {
	decoded, err := jsonvalue.DecodeBytes([]byte(requestJSON))
	check(err)
	request := decoded.(object)
	result, err := serialization.Pack(request)
	check(err)
	plan, err := execution.BuildPlan(request, result, nil)
	check(err)
	container := plan["containers"].([]interface{})[0].(object)

	facts(plan, container)
	order(request, result, container)
	citations(plan)
	canonical := canonicalForm(plan)
	lock := pin(request, result, plan, canonical)
	failures(request, lock)
}

func heading(title string) {
	fmt.Println(rule)
	fmt.Println(title)
	fmt.Println(rule)
	fmt.Println()
}

func paragraph(lines ...string) {
	for _, line := range lines {
		fmt.Println(line)
	}
}

func facts(plan, container object) {
	heading("1. What the solver decided, kept apart from what a screen says")
	planFacts := plan["facts"].(object)
	score := planFacts["score"].([]interface{})
	components := make([]string, len(score))
	for index, component := range score {
		components[index] = fmt.Sprint(component)
	}
	fmt.Printf("  format:          %s\n", plan["format"])
	fmt.Printf("  status:          %s\n", planFacts["status"])
	fmt.Printf("  containers used: %d\n", planFacts["container_count"])
	fmt.Printf("  score:           [%s]\n", strings.Join(components, ", "))
	fmt.Printf("  utilization:     %s\n", container["facts"].(object)["volume_utilization"])
	fmt.Println()
	paragraph(
		"  Everything above is under `facts`. It is the solver's own answer, copied and",
		"  not re-derived, so a downstream system that reads only `facts` loses nothing it",
		"  is entitled to rely on. The score stays a vector: collapsing five axes into one",
		"  number is a judgement about your priorities that this document does not make.",
		"")
}

func order(request, result, container object) {
	heading("2. The step order is injected, or it is honestly absent")
	steps := container["steps"].([]interface{})
	fmt.Printf("  order: %s\n", container["order"])
	for _, step := range steps {
		reference := step.(object)["placement"].(object)
		ticks := reference["position_ticks"].(object)
		fmt.Printf("    %-9s %s  at (%d, %d, %d)\n", reference["item_type"], reference["orientation"],
			ticks["x"], ticks["y"], ticks["z"])
	}
	fmt.Println()
	paragraph(
		"  Every placement is listed and not one is numbered. The engines compute a safe",
		"  loading order from geometry the plan builder never sees, so without one it says",
		"  `unavailable` rather than guessing.",
		"",
		"  There is no third behaviour on purpose. Falling back to the order placements",
		"  happen to appear in would present an artifact of how the solver walked its",
		"  candidate points as an order that is safe to lift boxes in. It is not.",
		"")

	reversed := make([]int, len(steps))
	for index := range reversed {
		reversed[index] = len(steps) - 1 - index
	}
	ordered, err := execution.BuildPlan(request, result, map[int][]int{0: reversed})
	check(err)
	first := ordered["containers"].([]interface{})[0].(object)
	fmt.Printf("  order: %s\n", first["order"])
	for _, step := range first["steps"].([]interface{}) {
		numbered := step.(object)
		fmt.Printf("    %d. %s\n", numbered["sequence"], numbered["placement"].(object)["item_type"])
	}
	fmt.Println()
	paragraph(
		"  Hand it an order and each step is numbered. The order above is reversed on",
		"  purpose, to show that the sequence is the one you supplied and not one the",
		"  plan builder re-derived behind your back.",
		"")
}

func citations(plan object) {
	heading("3. Every sentence names the fields it was built from")
	for _, entry := range plan["unplaced"].([]interface{}) {
		unplaced := entry.(object)
		entryFacts, presentation := unplaced["facts"].(object), unplaced["presentation"].(object)
		cites := presentation["cites"].([]interface{})
		names := make([]string, len(cites))
		for index, cite := range cites {
			names[index] = cite.(string)
		}
		fmt.Printf("  facts:        item_type='%s'\n", entryFacts["item_type"])
		fmt.Printf("                reason='%s' proof_level='%s'\n", entryFacts["reason"], entryFacts["proof_level"])
		fmt.Printf("  presentation: %s\n", presentation["summary"])
		fmt.Printf("  cites:        %s\n", strings.Join(names, ", "))
	}
	fmt.Println()
	paragraph(
		"  `proven` is a claim about a search, not a summary of one: no orientation of the",
		"  pallet jack fits any offered crate, so nothing was tried and nothing needed to",
		"  be. A reason with no citation would be a sentence nobody can check, which is",
		"  why `cites` is part of the format rather than a convention.",
		"")
}

func canonicalForm(plan object) string {
	heading("4. One plan, every engine, the same bytes")
	canonical, err := execution.CanonicalPlanJSON(plan)
	check(err)
	fmt.Printf("  canonical form: %d bytes, first 68 of them\n", len(canonical))
	fmt.Printf("    %s...\n", canonical[:68])
	fmt.Println()
	paragraph(
		"  Hand the same *result* to the plan builder of any Packvium engine and it emits",
		"  the same bytes. That is stricter than the packing contract, and it can be: a",
		"  plan is derived from a result, so there is nothing left to differ about.",
		"",
		"  It does not follow that every engine packing the same *request* agrees. Python,",
		"  PHP, Java, .NET and Go return identical results at the same version; Rust and",
		"  JavaScript return a valid answer at or above the objective floor, so their plan",
		"  for this request can be a different length. Whose packing is better is a",
		"  question the plan never answers.",
		"")
	return canonical
}

func pin(request, result, plan object, canonical string) locks.PlacementLock {
	heading("5. The operator pins a box, and gets a second plan — never an edited one")
	pinned, err := locks.LocksFromPlan(plan, 0, []string{"printer"})
	check(err)
	lock := pinned[0]
	fmt.Printf("  locked: %s %s at %s\n", lock.ItemType(), lock.Orientation(), position(lock))
	paragraph(
		"  The lock is read out of the plan's own placement reference. That is the only",
		"  reason the two can be trusted to mean the same box: there is no second address",
		"  format to drift.",
		"")

	resolved, err := locks.ResolveWithLocks(request, pinned, nil)
	check(err)
	again, err := execution.BuildPlan(request, result, nil)
	check(err)
	approved, err := execution.CanonicalPlanJSON(again)
	check(err)
	fmt.Printf("  preserved: %v\n", resolved.Preserved())
	fmt.Printf("  the approved plan is untouched: %v\n", approved == canonical)
	fmt.Println()
	paragraph(
		"  A lock becomes an ordinary placement constraint, so the re-solve is the same",
		"  portfolio under the same independent validator. Nothing here can produce a",
		"  placement the engine would otherwise refuse — the strongest thing a lock can do",
		"  is reserve its own slot and make the search work around it.",
		"")
	return lock
}

func failures(request object, lock locks.PlacementLock) {
	heading("6. Two ways a lock can fail, and they are not the same kind of thing")
	// Far outside the crate: a single lock that is individually impossible. The solve still
	// runs and still answers; it simply cannot honour this one.
	unreachable, err := locks.NewPlacementLock(0, "printer", "LWH", [3]int64{99000000, 0, 0})
	check(err)
	outcome, err := locks.ResolveWithLocks(request, []locks.PlacementLock{unreachable}, nil)
	check(err)
	missing := outcome.Missing()
	fmt.Printf("  a lock the solve cannot honour -> preserved=%v, %d lock reported back\n", outcome.Preserved(), len(missing))
	fmt.Printf("    %s at %s\n", missing[0].ItemType(), position(missing[0]))
	paragraph(
		"  Not an exception. The operator asked for something the geometry does not allow,",
		"  and what they need back is a valid plan plus the news that their pin was not",
		"  honoured — not a stack trace and no plan at all.",
		"")

	// An item the request never contained: the lock set is wrong about the world, and no
	// solve could make it right.
	scanner, err := locks.NewPlacementLock(0, "scanner", "LWH", [3]int64{0, 0, 0})
	check(err)
	_, err = locks.ResolveWithLocks(request, []locks.PlacementLock{lock, scanner}, nil)
	var refusal *locks.LockSetError
	if !errors.As(err, &refusal) {
		log.Fatalf("expected a lock-set refusal, got %v", err)
	}
	fmt.Printf("  a lock set that contradicts itself -> %s\n", refusal)
	fmt.Println()
	paragraph(
		"  Refused before any solve runs, because no result could satisfy it. The same goes",
		"  for two locks whose boxes overlap. The distinction is worth keeping: the first",
		"  case is an answer the operator may not like, the second is a question that has",
		"  no answer, and reporting them the same way would hide which one happened.")
}

// position prints a lock's ticks in Python's tuple notation, the shared notation.
func position(lock locks.PlacementLock) string {
	ticks := lock.PositionTicks()
	return fmt.Sprintf("(%d, %d, %d)", ticks[0], ticks[1], ticks[2])
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
