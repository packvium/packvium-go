// Shapes: items that do not fill their box.
//
// Run it from packvium-go/:
//
//	go run ./examples/shapes
//
// By default an item is its box. Two opt-in shapes narrow that: a convex_hull item occupies
// only the solid its vertices span, and a compressible item gives up part of its height
// under the load resting on it. Both go through serialization.Pack, which answers in the
// same JSON shape every engine returns, so everything read here is the cross-language
// contract rather than a Go type.
package main

import (
	"fmt"
	"log"

	"github.com/packvium/packvium-go/serialization"
)

type object = map[string]interface{}

// summarise runs one request and prints only what the shape changed: containers and
// refusals.
func summarise(label string, request object) {
	result := pack(request)
	containers := result["containers"].([]interface{})
	placed := 0
	for _, container := range containers {
		placed += len(container.(object)["placements"].([]interface{}))
	}
	fmt.Printf("  %-22s %-10s %d container(s), %d placed, %d refused\n", label, result["status"],
		len(containers), placed, len(result["unpacked_items"].([]interface{})))
}

func pack(request object) object {
	request["units"] = object{"length": "mm"}
	result, err := serialization.Pack(request)
	if err != nil {
		log.Fatal(err)
	}
	return result
}

func crate(length, width, height string) []interface{} {
	return []interface{}{object{"id": "crate", "inner_dimensions": object{"length": length, "width": width, "height": height}}}
}

func vertices(points ...[3]string) []interface{} {
	list := make([]interface{}, len(points))
	for index, point := range points {
		list[index] = object{"x": point[0], "y": point[1], "z": point[2]}
	}
	return list
}

// Two triangular prisms, each cut from the same 100 mm cube along the diagonal. Their
// bounding boxes are identical and fill the crate on their own, so as cuboids the second
// one has nowhere to go. As hulls they are complementary halves and share the crate exactly:
// the collision test is an exact integer separating-axis test on the vertices.
//
// The hull is given in the item's own coordinates, in the request's length unit, and must
// fit inside the declared dimensions: the box still bounds the item, the hull only says how
// much of it is solid.
var (
	lowerWedge = vertices([3]string{"0", "0", "0"}, [3]string{"100", "0", "0"}, [3]string{"0", "100", "0"},
		[3]string{"0", "0", "100"}, [3]string{"100", "0", "100"}, [3]string{"0", "100", "100"})
	upperWedge = vertices([3]string{"100", "100", "0"}, [3]string{"100", "0", "0"}, [3]string{"0", "100", "0"},
		[3]string{"100", "100", "100"}, [3]string{"100", "0", "100"}, [3]string{"0", "100", "100"})
)

func wedge(id string, hull []interface{}) object {
	item := object{"id": id, "quantity": int64(1),
		"dimensions": object{"length": "100", "width": "100", "height": "100"},
		"weight":     object{"value": "1", "unit": "kg"}}
	if hull != nil {
		item["shape_type"] = "convex_hull"
		item["hull_vertices"] = hull
	}
	return item
}

// compression_ratio is the fraction of its own height an item may lose under load -- 0.25
// gives up at most a quarter -- and the mass above decides how much it actually gives.
// Past max_compression_pressure_kpa the item is crushed and the load is refused instead.
// must_be_on_floor keeps the cushion underneath, or nothing would bear on it.
func cushion(crushKPA int64) object {
	return object{"id": "cushion", "quantity": int64(1),
		"dimensions":       object{"length": "100", "width": "100", "height": "100"},
		"weight":           object{"value": "2", "unit": "kg"},
		"must_be_on_floor": true, "shape_type": "compressible", "compression_ratio": 0.25,
		"max_compression_pressure_kpa": crushKPA}
}

func brick(kilograms int) object {
	return object{"id": "brick", "quantity": int64(1),
		"dimensions": object{"length": "100", "width": "100", "height": "100"},
		"weight":     object{"value": fmt.Sprint(kilograms), "unit": "kg"}}
}

// load is one crate, one cushion, one brick; only the brick's mass changes.
func load(label string, kilograms int) {
	result := pack(object{"items": []interface{}{cushion(100), brick(kilograms)}, "containers": crate("100", "100", "200")})
	unused := result["score"].([]interface{})[3]
	fmt.Printf("  %-22s %d container(s), unused volume %v ppm\n", label, len(result["containers"].([]interface{})), unused)
}

func main() {
	fmt.Println("convex_hull -- two complementary wedges cut from one cube")
	summarise("as cuboids", object{"items": []interface{}{wedge("wedge-lower", nil), wedge("wedge-upper", nil)},
		"containers": crate("100", "100", "100")})
	summarise("as hulls", object{"items": []interface{}{wedge("wedge-lower", lowerWedge), wedge("wedge-upper", upperWedge)},
		"containers": crate("100", "100", "100")})

	// The crate is 100 x 100 x 200 and the items are 100 mm cubes, so rigidly they fill it
	// exactly. Under 101 kg the cushion gives up part of its quarter, the pair still ships as
	// one stack, and the volume it stopped occupying shows up as unused. One more kilogram
	// crosses 100 kPa over the cushion's 0.01 m^2 face: the stack is refused, the brick opens
	// a second crate, and half of each crate is empty.
	fmt.Println("\ncompressible -- a cushion that yields to the load above it")
	load("brick 101 kg", 101)
	load("brick 102 kg", 102)
}
