// Nested packing: cartons into a pallet, in one call.
//
// Run it from packvium-go/:
//
//	go run ./examples/nested
//
// Real fulfilment is rarely one level. Units go into cartons, cartons go onto a pallet, and
// sometimes pallets go into a trailer. nested.Pack runs those levels in order and feeds
// each level's packed containers into the next level as items -- a carton that came out of
// level one arrives at level two as a box with its own outer dimensions and its total
// packed weight.
//
// The levels stay independent on purpose. Level two does not reach back and repack level
// one to get a better pallet, because that would make the carton contents depend on the
// pallet, and a carton you already taped shut cannot be repacked. If you want that
// trade-off explored, run the packer yourself with different carton sets and compare.
package main

import (
	"fmt"
	"log"

	"github.com/toxakara/packvium-go/config"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/nested"
	"github.com/toxakara/packvium-go/units"
)

func main() {
	items := []*domain.Item{
		item("mug", dims("120", "120", "100"), "400 g", 24),
		item("plate", dims("260", "260", "20"), "600 g", 16),
	}
	fast := config.Default(config.Fast)
	fast.TimeLimitMS = 60_000
	cartonConfig, err := config.New(fast)
	check(err)
	small, large := dims("310", "310", "310"), dims("412", "412", "412")
	levels := []nested.Level{
		{Name: "carton", Config: cartonConfig, Containers: []*domain.Container{
			container(domain.ContainerSpec{ID: "box-s", InnerDimensions: dims("300", "300", "300"), OuterDimensions: &small,
				TareWeight: weight("300 g"), MaxPayload: payload("15 kg"), CostMinor: 120}),
			container(domain.ContainerSpec{ID: "box-l", InnerDimensions: dims("400", "400", "400"), OuterDimensions: &large,
				TareWeight: weight("500 g"), MaxPayload: payload("25 kg"), CostMinor: 180}),
		}},
		{Name: "pallet", Containers: []*domain.Container{
			container(domain.ContainerSpec{ID: "euro", InnerDimensions: dims("1200", "800", "1400"),
				TareWeight: weight("25 kg"), MaxPayload: payload("700 kg"), CostMinor: 1500}),
		}},
	}

	outcome, err := nested.Pack(items, levels)
	check(err)
	for index, packedLevel := range outcome.Levels {
		fmt.Printf("== %s ==\n", levels[index].Name)
		fmt.Printf("   status: %s\n", packedLevel.Status)
		fmt.Printf("   containers used: %d\n", len(packedLevel.Containers))
		for _, packed := range packedLevel.Containers {
			fmt.Printf("     %-8s holding %d item(s)\n", packed.Container().ID(), len(packed.Placements()))
		}
		if len(packedLevel.Unpacked) > 0 {
			fmt.Printf("   left over: %d\n", len(packedLevel.Unpacked))
		}
		fmt.Println()
	}
	cartons := len(outcome.Levels[0].Containers)
	pallets := len(outcome.Levels[len(outcome.Levels)-1].Containers)
	fmt.Printf("%d carton(s) travelling on %d pallet(s)\n", cartons, pallets)
}

func item(id string, size domain.Dimensions, mass string, quantity int64) *domain.Item {
	spec := domain.NewItemSpec(id, size)
	spec.Weight, spec.Quantity = weight(mass), quantity
	created, err := domain.NewItem(spec)
	check(err)
	return created
}

func container(spec domain.ContainerSpec) *domain.Container {
	created, err := domain.NewContainer(spec)
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
