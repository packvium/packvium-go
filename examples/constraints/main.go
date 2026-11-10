// Constraints: how to say "this may not go there" and get told why.
//
// Run it from packvium-go/:
//
//	go run ./examples/constraints
//
// The solver's job is not only to fit boxes. Most real packing rules are refusals -- this
// side up, nothing on top of that, keep the chemicals away from the food -- and the useful
// part of the answer is often the item that did not fit and the reason it did not.
//
// Every constraint here is a field on an ItemSpec or ContainerSpec. None of them needs a
// custom type, and none of them changes how you call Pack.
package main

import (
	"fmt"
	"log"
	"strconv"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/explain"
	"github.com/packvium/packvium-go/packer"
	"github.com/packvium/packvium-go/result"
	"github.com/packvium/packvium-go/units"
)

func main() {
	// KeepUpright forbids every rotation that would tip the item over: an open tub of paint.
	paint := spec("paint", dims("200", "200", "250"), "5 kg", 2)
	paint.KeepUpright = true
	// MustBeOnFloor keeps the item on the floor, and MaxTopLoad caps what may rest directly
	// on it. Note "directly": this is not a whole-stack limit.
	glass := spec("glass-panel", dims("400", "300", "40"), "8 kg", 1)
	topLoad := weight("2 kg")
	glass.MustBeOnFloor, glass.MaxTopLoad = true, &topLoad
	// Stackable false means nothing may be placed on this item at all.
	cake := spec("cake", dims("250", "250", "150"), "1 kg", 1)
	cake.Stackable = false
	// Tags are how two items refuse each other; IncompatibleTags is checked both ways, so
	// tagging one side is enough.
	bleach := spec("bleach", dims("120", "120", "300"), "2 kg", 2)
	bleach.Tags, bleach.IncompatibleTags = []string{"hazmat"}, []string{"food"}
	flour := spec("flour", dims("200", "150", "100"), "1500 g", 3)
	flour.Tags = []string{"food"}
	// Longer than the crate's longest inner edge in every orientation, so no solver can
	// place it. It is here to show what a refusal looks like.
	ladder := spec("ladder", dims("1800", "300", "100"), "6 kg", 1)

	// MaxPayload is the weight the container may carry, excluding its own tare.
	crate := container(domain.ContainerSpec{ID: "crate", InnerDimensions: dims("600", "500", "500"),
		TareWeight: weight("3 kg"), MaxPayload: payload("25 kg"), CostMinor: 400})

	outcome := pack(items(paint, glass, cake, bleach, flour, ladder), []*domain.Container{crate})
	fmt.Printf("status: %s\n", outcome.Status)
	fmt.Printf("containers opened: %d\n", len(outcome.Containers))
	for index, packed := range outcome.Containers {
		fmt.Printf("\ncrate #%d (%s): %d placement(s)\n", index+1, packed.Container().ID(), len(packed.Placements()))
		for _, placement := range packed.Placements() {
			position := placement.Position()
			fmt.Printf("  %-12s at (%s, %s, %s) mm\n", placement.Instance().Item().ID(),
				millimetres(position.X()), millimetres(position.Y()), millimetres(position.Z()))
		}
	}
	if len(outcome.Unpacked) > 0 {
		fmt.Println("\nnot packed:")
		for _, unpacked := range outcome.Unpacked {
			fmt.Printf("  %-12s %s\n", unpacked.Instance.Item().ID(), explanation(unpacked))
		}
	} else {
		fmt.Println("\neverything fitted -- widen the crate or add items to see a refusal explained")
	}

	shelf := []*domain.Container{container(domain.ContainerSpec{ID: "shelf", InnerDimensions: dims("800", "400", "500"), MaxPayload: payload("40 kg")})}

	// AllowedRotations: a pole that only fits lying down, forbidden from lying down.
	pole := dims("90", "90", "700")
	upright := spec("pole", pole, "1 kg", 1)
	upright.AllowedRotations = domain.UprightRotations()
	compare("allowed_rotations -- a pole that only fits lying down, forbidden from lying down",
		items(spec("pole", pole, "1 kg", 1)), items(upright), shelf)

	// MaxStackedItems caps how many units may sit above one item -- a pallet-pattern rule
	// ("three high, no more"), not a weight limit. The column is one tin wide, so height is
	// the only way to fit more, and the second container is the price of the cap.
	column := []*domain.Container{container(domain.ContainerSpec{ID: "column", InnerDimensions: dims("160", "160", "600"), MaxPayload: payload("40 kg")})}
	tin := dims("150", "150", "120")
	capped := spec("tin", tin, "800 g", 5)
	three := int64(3)
	capped.MaxStackedItems = &three
	compare("max_stacked_items -- five tins fit in one column; three-high needs two columns",
		items(spec("tin", tin, "800 g", 5)), items(capped), column)

	// MinimumSupportRatio is how much of an item's base must rest on something solid. The
	// only place the slab fits is perched on the plinth, on a quarter of its base; at 0.9
	// that is refused and a second ledge opens.
	ledge := []*domain.Container{container(domain.ContainerSpec{ID: "ledge", InnerDimensions: dims("400", "400", "350"), MaxPayload: payload("40 kg")})}
	plinth := spec("plinth", dims("200", "200", "300"), "5 kg", 1)
	plinth.MustBeOnFloor = true
	slab := dims("400", "400", "60")
	supported := spec("slab", slab, "9 kg", 1)
	supported.MinimumSupportRatio = 0.9
	compare("minimum_support_ratio -- a slab perched on a quarter of its base",
		items(plinth, spec("slab", slab, "9 kg", 1)), items(plinth, supported), ledge)

	// Group is atomic: every member ships in one container or none of them does. The third
	// part is too long for the shelf, so it takes the other two down with it.
	parts := []domain.Dimensions{dims("200", "200", "100"), dims("200", "200", "100"), dims("900", "100", "100")}
	var loose, grouped []domain.ItemSpec
	assembly := "assembly"
	for index, part := range parts {
		member := spec(fmt.Sprintf("kit-%d", index+1), part, "2 kg", 1)
		loose = append(loose, member)
		member.Group = &assembly
		grouped = append(grouped, member)
	}
	compare("group -- one member cannot be placed, so none of them is", items(loose...), items(grouped...), shelf)
}

// compare packs the same scene without and with one rule.
func compare(rule string, without, withRule []*domain.Item, containers []*domain.Container) {
	fmt.Printf("\n%s\n", rule)
	for _, variant := range []struct {
		label string
		items []*domain.Item
	}{{"without the rule", without}, {"with the rule   ", withRule}} {
		outcome := pack(variant.items, containers)
		placed := 0
		for _, packed := range outcome.Containers {
			placed += len(packed.Placements())
		}
		fmt.Printf("  %s: %d container(s), %d placed, %d refused\n", variant.label, len(outcome.Containers), placed, len(outcome.Unpacked))
		for _, unpacked := range outcome.Unpacked {
			fmt.Printf("      %s\n", explanation(unpacked))
		}
	}
}

// millimetres renders an exact tick position for a human, as the reference's "%g" does.
func millimetres(ticks int64) string {
	return strconv.FormatFloat(float64(ticks)/float64(units.TicksPerMM), 'g', 6, 64)
}

func explanation(unpacked domain.UnpackedItem) string {
	text, err := explain.UnpackedItem(unpacked)
	check(err)
	return text
}

func pack(items []*domain.Item, containers []*domain.Container) *result.PackingResult {
	balanced, err := config.New(config.Default(config.Balanced))
	check(err)
	outcome, err := packer.New(balanced, algorithm.NoExtensions()).Pack(items, containers)
	check(err)
	return outcome
}

func spec(id string, size domain.Dimensions, mass string, quantity int64) domain.ItemSpec {
	value := domain.NewItemSpec(id, size)
	value.Weight, value.Quantity = weight(mass), quantity
	return value
}

func items(specs ...domain.ItemSpec) []*domain.Item {
	created := make([]*domain.Item, len(specs))
	for index, value := range specs {
		item, err := domain.NewItem(value)
		check(err)
		created[index] = item
	}
	return created
}

func container(value domain.ContainerSpec) *domain.Container {
	created, err := domain.NewContainer(value)
	check(err)
	return created
}

func dims(length, width, height string) domain.Dimensions {
	value, err := domain.DimensionsOf(length, width, height, "mm")
	check(err)
	return value
}

func weight(text string) units.Weight {
	value, err := units.ParseWeight(units.TextQuantity(text), "g", units.Nearest)
	check(err)
	return value
}

func payload(text string) *units.Weight {
	value := weight(text)
	return &value
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
