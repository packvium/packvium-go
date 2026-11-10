// The smallest useful call: some items, some boxes, one answer.
//
// Run it from packvium-go/:
//
//	go run ./examples/basic
//
// Three things are worth noticing.
//
// domain.DimensionsOf in "mm" and in "in" are both exact -- "4 in" is not converted to a
// rounded number of millimetres, it is stored as an exact tick count, so an imperial spec
// sheet and a metric container agree without a tolerance to tune (see examples/units).
//
// KeepUpright is a rule, not a hint. The mug will never be laid on its side, and if that
// makes it not fit you are told which item failed and why, rather than getting a
// plausible-looking arrangement that spills coffee.
//
// CostMinor is what the box costs you, in minor currency units. The default objective
// ignores it -- it opens as few containers as possible and packs them tightly. Ranking by
// packaging cost, by carrier-billed weight or by actual money is one setting away; that is
// what examples/objectives is for.
package main

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/packer"
	"github.com/packvium/packvium-go/serialization"
	"github.com/packvium/packvium-go/units"
)

func main() {
	book := domain.NewItemSpec("book", dimensions("210", "140", "30", "mm"))
	book.Weight, book.Quantity = weight("450 g"), 4
	mug := domain.NewItemSpec("mug", dimensions("4", "4", "5", "in"))
	mug.Weight, mug.Quantity, mug.KeepUpright = weight("12 oz"), 2, true

	medium, large := weight("20 kg"), weight("30 kg")
	containers := []*domain.Container{
		container(domain.ContainerSpec{ID: "box-m", InnerDimensions: dimensions("400", "300", "250", "mm"), MaxPayload: &medium, CostMinor: 180}),
		container(domain.ContainerSpec{ID: "box-l", InnerDimensions: dimensions("500", "400", "350", "mm"), MaxPayload: &large, CostMinor: 250}),
	}

	balanced, err := config.New(config.Default(config.Balanced))
	check(err)
	result, err := packer.New(balanced, algorithm.NoExtensions()).Pack([]*domain.Item{item(book), item(mug)}, containers)
	check(err)

	var opened, unpacked []string
	placed := 0
	for _, packed := range result.Containers {
		opened = append(opened, "'"+packed.Container().ID()+"'")
		placed += len(packed.Placements())
	}
	for _, left := range result.Unpacked {
		unpacked = append(unpacked, fmt.Sprintf("('%s', '%s')", left.Instance.Item().ID(), left.Reason))
	}
	score := make([]string, len(result.Score))
	for index, component := range result.Score {
		score[index] = fmt.Sprint(component)
	}
	fmt.Println("status    ", result.Status)
	fmt.Println("containers", "["+strings.Join(opened, ", ")+"]")
	fmt.Println("packed    ", placed, "of 6")
	fmt.Println("unpacked  ", "["+strings.Join(unpacked, ", ")+"]")
	fmt.Println("score     ", "("+strings.Join(score, ", ")+")", " <- lexicographic, exact integers, lower is better")
	fmt.Println()
	fmt.Println("the full result as a plain map is what examples/serialization explores:")
	encoded := serialization.EncodeResult(result, "mm", "g")
	keys := make([]string, 0, len(encoded))
	for key := range encoded {
		keys = append(keys, "'"+key+"'")
	}
	sort.Strings(keys)
	fmt.Println("[" + strings.Join(keys, ", ") + "]")
}

func dimensions(length, width, height, unit string) domain.Dimensions {
	value, err := domain.DimensionsOf(length, width, height, unit)
	check(err)
	return value
}

func weight(text string) units.Weight {
	value, err := units.ParseWeight(units.TextQuantity(text), "g", units.Nearest)
	check(err)
	return value
}

func item(spec domain.ItemSpec) *domain.Item {
	value, err := domain.NewItem(spec)
	check(err)
	return value
}

func container(spec domain.ContainerSpec) *domain.Container {
	value, err := domain.NewContainer(spec)
	check(err)
	return value
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
