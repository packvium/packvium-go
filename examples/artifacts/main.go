// Hand a packing result to a warehouse system that has no engine.
//
// Run it from packvium-go/:
//
//	go run ./examples/artifacts
//
// An execution plan says what to lift first. A warehouse or transport system needs more than
// that before it can act on its own: how big each box is, what the load weighs, a sheet to
// print for the dock, and a record of which request and solver produced all of it. Without
// those, it has to call the engine again.
//
// The operational artifact is that one document. It wraps the plan unchanged and adds
// geometry, display values and provenance. It exports to JSON, CSV and a printable HTML work
// order, and none of that calls a solver, a renderer or a clock. Every engine builds the same
// bytes from it.
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/packvium/packvium-go/artifacts"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/sequence"
	"github.com/packvium/packvium-go/serialization"
	"github.com/packvium/packvium-go/units"
)

type object = map[string]interface{}

// time_limit_ms is a fuse far above what this solve needs, so the answer never depends on
// machine load.
const requestJSON = `{
  "units": {"length": "mm"},
  "configuration": {"time_limit_ms": 60000},
  "items": [
    {"id": "printer", "dimensions": {"length": 420, "width": 300, "height": 250}, "weight": "12 kg",
     "metadata": {"sales_order": "SO-1042"}},
    {"id": "toner", "quantity": 3, "dimensions": {"length": 300, "width": 100, "height": 100},
     "weight": "1.5 kg", "minimum_support_ratio": 0.5}
  ],
  "containers": [{"id": "crate", "inner_dimensions": {"length": 800, "width": 400, "height": 400}}]
}`

var (
	dimensionAxes = []string{"length", "width", "height"}
	positionAxes  = []string{"x", "y", "z"}
)

func main() {
	request, err := artifacts.Parse([]byte(requestJSON))
	check(err)
	result, err := serialization.Pack(request)
	check(err)

	artifact := oneDocument(request, result)
	importable(artifact)
	printable(artifact)
	sameBytes(artifact)
}

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", 78))
}

func paragraph(lines ...string) {
	for _, line := range lines {
		fmt.Println(line)
	}
}

func oneDocument(request interface{}, result object) object {
	section("1. One document that carries everything a consumer needs")
	crate := result["containers"].([]interface{})[0].(object)
	var boxes []domain.Box
	for _, placement := range crate["placements"].([]interface{}) {
		boxes = append(boxes, box(placement.(object)))
	}
	// The order boxes can go in without lifting one over another comes from the engine's own
	// sequence API. The artifact is handed that order; it never invents one.
	order, err := sequence.SafeLoadingOrder(boxes, dimensions(crate["inner_dimensions"].(object)), sequence.AllDirections())
	check(err)
	artifact, err := artifacts.Build(request, result, map[int][]int{0: order})
	check(err)
	provenance := artifact["provenance"].(object)
	plan := artifact["plan"].(object)
	geometry := artifact["geometry"].(object)["containers"].([]interface{})[0].(object)
	item := provenance["request"].(object)["items"].([]interface{})[0].(object)
	fmt.Printf("  format:          %s\n", artifact["format"])
	fmt.Printf("  plan steps:      %d\n", len(plan["containers"].([]interface{})[0].(object)["steps"].([]interface{})))
	fmt.Printf("  crate inside:    %s ticks long\n", geometry["inner_dimensions"].(object)["length"])
	fmt.Printf("  replay:          %s\n", provenance["replay"].(object)["level"])
	fmt.Printf("  sales order:     %s\n", item["metadata"].(object)["sales_order"])
	paragraph("",
		"  The request is inside the artifact, not a hash of it: a digest names a request,",
		"  and only the request itself lets someone replay the artifact without a lookup.",
		"  `replay` is `exact` because the search finished inside its fuse; a search the",
		"  clock stopped would say `not_guaranteed` and name the field that decided it.")
	return artifact
}

func importable(artifact object) {
	section("2. A CSV a warehouse system can import")
	text, err := artifacts.ExportCSV(artifact)
	check(err)
	for _, row := range strings.Split(text, "\r\n")[:3] {
		fmt.Printf("  %s\n", row)
	}
	paragraph("",
		"  One row per step, in the plan's order, then one row per item that was not",
		"  packed. The tick columns are the identifiers a system matches on; the rendered",
		"  columns are for people. Nothing is re-rendered: every value is the result's own.")
}

func printable(artifact object) {
	section("3. A work order to print")
	html, err := artifacts.ExportWorkOrderHTML(artifact)
	check(err)
	fmt.Printf("  %d bytes of HTML, one section per container, one checkbox per step\n", len(html))
	fmt.Printf("  contains a script or an external resource: %t\n", strings.Contains(html, "<script") || strings.Contains(html, "http"))
	paragraph("",
		"  Self-contained on purpose: a work order that loads a stylesheet from the network",
		"  is a work order that prints blank on a dock with no signal.")
}

func sameBytes(artifact object) {
	section("4. The same bytes in every engine, and a refusal instead of a guess")
	canonical, err := artifacts.ExportJSON(artifact)
	check(err)
	fmt.Printf("  canonical JSON: %d bytes, starting %s...\n", len(canonical), string([]rune(canonical)[:40]))
	foreign := object{}
	for key, value := range artifact {
		foreign[key] = value
	}
	foreign["format"] = "packvium-operational-artifact/v2"
	if _, err := artifacts.ExportCSV(foreign); err != nil {
		fmt.Printf("  a v2 document: refused with %s\n", err.(*artifacts.Error).Code)
	}
	paragraph("",
		"  The canonical form is RFC 8785, so PHP, Rust and JavaScript build these exact",
		"  bytes from the same request and result. A reader that meets a format it does not",
		"  know refuses it by name rather than drawing half of it.")
}

// box is a placement as the sequence API's geometry, read from its exact ticks.
func box(placement object) domain.Box {
	position, size := placement["position"].(object), placement["dimensions"].(object)
	var origin [3]int64
	for index, axis := range positionAxes {
		origin[index] = ticks(position[axis])
	}
	point, err := domain.NewPoint(origin[0], origin[1], origin[2])
	check(err)
	return domain.NewBox(point, dimensions(size))
}

func dimensions(size object) domain.Dimensions {
	var lengths [3]units.Length
	for index, axis := range dimensionAxes {
		length, err := units.NewLength(ticks(size[axis]))
		check(err)
		lengths[index] = length
	}
	result, err := domain.NewDimensions(lengths[0], lengths[1], lengths[2])
	check(err)
	return result
}

func ticks(scalar interface{}) int64 {
	return scalar.(object)["ticks"].(int64)
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
