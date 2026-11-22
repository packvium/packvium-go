// Get the same answer on every run: counted work instead of a clock.
//
// Run it from packvium-go/:
//
//	go run ./examples/reproducibility
//
// The search tries several starts and keeps the best. Something has to decide when it stops,
// and there are two choices:
//
//   - TimeLimitMS, a wall clock. Its default is one second. When it runs out, the search
//     keeps what it has, and how far it got depends on the machine: its speed, what else is
//     running, the garbage collector. The same request can give a different answer on a
//     loaded server than on your laptop.
//   - EffortBudget, counted work: candidates evaluated, placements attempted, search nodes,
//     restarts. The count is the same on every machine, so the answer is too.
//
// Set an effort budget whenever the answer is stored, compared, audited or replayed, and
// keep the time limit as a generous fuse against a genuine hang. The result says which one
// stopped the search, in its termination.
//
// To make a slow machine repeatable here, this example uses the injected clock
// packer.NewWith accepts for testing: a clock that advances a fixed amount each time it is
// read stands in for a faster or slower host. A real deployment never passes one.
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/artifacts"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/packer"
	"github.com/packvium/packvium-go/result"
	"github.com/packvium/packvium-go/serialization"
	"github.com/packvium/packvium-go/units"
)

// fuseMS is a fuse, not a limit: nothing below comes close to it on a real machine.
const fuseMS = 60_000

// host is a monotonic clock that moves nanosecondsPerRead every time it is read: a larger
// step is a slower or busier machine doing the same work.
func host(nanosecondsPerRead int64) algorithm.Clock {
	next := int64(0)
	return func() int64 {
		reading := next
		next += nanosecondsPerRead
		return reading
	}
}

func describe(packed *result.PackingResult) string {
	document := serialization.EncodeResult(packed, "mm", "g")
	level, err := artifacts.ReplayLevel(document)
	check(err)
	boxes := make([]string, len(packed.Containers))
	for index, container := range packed.Containers {
		boxes[index] = container.Container().ID()
	}
	return fmt.Sprintf("%-6s left out %2d  termination %-12s replay %s", strings.Join(boxes, ","),
		len(packed.Unpacked), packed.Termination.Code(), level)
}

type simulated struct {
	label string
	step  int64
}

func solveOn(items []*domain.Item, boxes []*domain.Container, options config.Options, machine simulated) string {
	cfg, err := config.New(options)
	check(err)
	packed, err := packer.NewWith(cfg, nil, nil, host(machine.step)).Pack(items, boxes)
	check(err)
	return describe(packed)
}

func balanced(timeLimitMS int64, budget *config.EffortBudget) config.Options {
	options := config.Default(config.Balanced)
	options.TimeLimitMS, options.EffortBudget = timeLimitMS, budget
	return options
}

func limit(value int64) *int64 { return &value }

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", 78))
}

func main() {
	items := []*domain.Item{
		item("kettle", "310", "220", "140", "2 kg", 5),
		item("toaster", "250", "250", "250", "3 kg", 4),
		item("knife-block", "400", "150", "100", "1 kg", 6),
		item("scale", "180", "120", "90", "0.5 kg", 8),
	}
	boxes := []*domain.Container{
		box("small", "450", "350", "300", 100), box("medium", "600", "400", "400", 160),
		box("large", "800", "600", "500", 240),
	}

	section("1. A clock-limited search answers differently on different machines")
	// The same 100 ms limit on four simulated hosts.
	for _, machine := range []simulated{{"fast host", 10_000}, {"busy host", 50_000}, {"slower host", 200_000},
		{"overloaded", 1_000_000}} {
		fmt.Printf("  %-12s %s\n", machine.label, solveOn(items, boxes, balanced(100, nil), machine))
	}
	fmt.Println("\n  Same request, four answers. Each is a valid packing of what the search reached\n" +
		"  before the clock ran out, and the items it never reached are listed as left out,\n" +
		"  but none can be reproduced: run it again on a different day and you get whichever\n" +
		"  one the machine allows. `termination` says\n" +
		"  `time_limit`, and `ReplayLevel` refuses to promise a replay.")

	section("2. An effort budget answers the same everywhere")
	enough := &config.EffortBudget{MaxCandidatesEvaluated: limit(5_000)}
	for _, machine := range []simulated{{"fast host", 10_000}, {"slower host", 200_000}, {"overloaded", 1_000_000}} {
		fmt.Printf("  %-12s %s\n", machine.label, solveOn(items, boxes, balanced(fuseMS, enough), machine))
	}
	fmt.Println("\n  The budget is large enough that the search finishes, so `termination` is\n" +
		"  `complete`, and it would stop at the same count on any machine if it were not.")

	section("3. A budget that is too small is still reproducible, and says so")
	tooSmall := &config.EffortBudget{MaxCandidatesEvaluated: limit(500)}
	for _, machine := range []simulated{{"fast host", 10_000}, {"overloaded", 1_000_000}} {
		fmt.Printf("  %-12s %s\n", machine.label, solveOn(items, boxes, balanced(fuseMS, tooSmall), machine))
	}
	fmt.Println("\n  The search stopped early and left items out -- `termination` is `effort_limit`,\n" +
		"  not `complete` -- but it stopped at the same point on both hosts, so the answer\n" +
		"  can be replayed exactly. Raise the budget until the answer is good enough, then\n" +
		"  keep it fixed.")

	section("4. Reading `termination`")
	cfg, err := config.New(balanced(fuseMS, &config.EffortBudget{MaxRestarts: limit(4)}))
	check(err)
	packed, err := packer.New(cfg, nil).Pack(items, boxes)
	check(err)
	termination := serialization.EncodeResult(packed, "mm", "g")["termination"].(map[string]interface{})
	fmt.Printf("  code: %s\n", termination["code"])
	for _, entry := range termination["starts"].([]interface{}) {
		start := entry.(map[string]interface{})
		marker := ""
		if start["selected"] == true {
			marker = "  <- selected"
		}
		line := fmt.Sprintf("    %-28s completed=%-5s%s", start["id"], pythonBool(start["completed"] == true), marker)
		fmt.Println(strings.TrimRight(line, " "))
	}
	fmt.Println("\n  `max_restarts` caps how many portfolio starts run -- here four -- which bounds the\n" +
		"  work of a large request as well as fixing it. `code` describes the selected start:\n" +
		"  a losing start that was cut short leaves it `complete`, and shows up in its own\n" +
		"  record and in `all_required_starts_completed` instead. Over JSON, the same budget\n" +
		"  is `configuration.effort_budget`.")
}

// pythonBool keeps the column the reference prints, True or False.
func pythonBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

func item(id, length, width, height, mass string, quantity int64) *domain.Item {
	dimensions, err := domain.DimensionsOf(length, width, height, "mm")
	check(err)
	spec := domain.NewItemSpec(id, dimensions)
	spec.Weight, err = units.ParseWeight(units.TextQuantity(mass), "g", units.Nearest)
	check(err)
	spec.Quantity = quantity
	value, err := domain.NewItem(spec)
	check(err)
	return value
}

func box(id, length, width, height string, costMinor int64) *domain.Container {
	dimensions, err := domain.DimensionsOf(length, width, height, "mm")
	check(err)
	value, err := domain.NewContainer(domain.ContainerSpec{ID: id, InnerDimensions: dimensions, CostMinor: costMinor})
	check(err)
	return value
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
