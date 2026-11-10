// Units: why there are no floats anywhere, and what you can type.
//
// Run it from packvium-go/:
//
//	go run ./examples/units
//
// Packing is arithmetic about physical space, and floating point is the wrong tool for it:
// 0.1 + 0.2 is famously not 0.3, and a box that "almost" fits either fits or does not. So
// every length and weight in this library is an exact integer count of ticks -- 1/16000 mm
// for length, 1/8 microgram for weight -- and nothing in the request, the search or the
// result is ever a float.
//
// That choice is invisible until it saves you. This example shows where it does.
package main

import (
	"fmt"
	"log"
	"math/big"

	"github.com/toxakara/packvium-go/algorithm"
	"github.com/toxakara/packvium-go/config"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/packer"
	"github.com/toxakara/packvium-go/units"
)

func length(text string) units.Length {
	value, err := units.ParseLength(units.TextQuantity(text), "mm", units.Nearest)
	check(err)
	return value
}

func weight(text string) units.Weight {
	value, err := units.ParseWeight(units.TextQuantity(text), "g", units.Nearest)
	check(err)
	return value
}

func decimal(value interface{ Decimal(string) (string, error) }, unit string) string {
	text, err := value.Decimal(unit)
	check(err)
	return text
}

func dimensions(unit, l, w, h string) domain.Dimensions {
	value, err := domain.DimensionsOf(l, w, h, unit)
	check(err)
	return value
}

func main() {
	// What you can type: integers, decimals, fractions and mixed fractions, in mm, cm, m,
	// in and ft -- because a spec sheet says "12 3/8 in", and retyping that as 12.375 is a
	// transcription step where mistakes live.
	for _, text := range []string{"30", "30.5 mm", "3/16 in", "12 3/8 in", "2 ft", "1.5 m"} {
		value := length(text)
		fmt.Printf("%12s  ->  %12d ticks  =  %s mm\n", text, value.Ticks(), decimal(value, "mm"))
	}
	fmt.Println()
	for _, text := range []string{"450 g", "12 oz", "1.5 kg", "2 3/4 lb"} {
		value := weight(text)
		fmt.Printf("%12s  ->  %14d ticks  =  %s g\n", text, value.Ticks(), decimal(value, "g"))
	}

	// Common fractional inches are exact, not rounded: 1/16000 mm was chosen so that every
	// binary fraction of an inch down to 1/128 lands on a whole number of ticks.
	fmt.Println()
	fmt.Println("1 inch is", units.TicksPerInch, "ticks, so 1/128 in is", units.TicksPerInch/128, "ticks exactly")
	sixteenth := length("1/16 in")
	asFraction := big.NewRat(sixteenth.Ticks(), units.TicksPerMM)
	fmt.Println("1/16 in as a fraction of a mm:", asFraction.RatString(), "==", big.NewRat(sixteenth.Ticks(), units.TicksPerMM).RatString())
	fmt.Println("128 x (1/128 in) == 1 in ?", pythonBool(length("1/128 in").Ticks()*128 == length("1 in").Ticks()))

	// The honest other half: a third of an inch is not a binary fraction, so it does not land
	// on a whole tick. The library rounds it, deterministically and visibly.
	third := length("1/3 in")
	fmt.Println("  3 x (1/3 in) == 1 in ?", pythonBool(third.Ticks()*3 == length("1 in").Ticks()),
		fmt.Sprintf("-- 1/3 in is %d/3 ticks, which is not an integer", units.TicksPerInch))

	// Where floats would actually bite: the classic case, and the same three tenths as
	// lengths instead.
	fmt.Println()
	tenth, fifth := 0.1, 0.2
	fmt.Println("0.1 + 0.2 == 0.3 in floats:", pythonBool(tenth+fifth == 0.3))
	fmt.Println("the same three tenths as lengths:",
		pythonBool(length("0.1 mm").Ticks()+length("0.2 mm").Ticks() == length("0.3 mm").Ticks()))

	// The same exactness decides whether something fits: one tick over is a refusal, with no
	// tolerance to tune.
	fmt.Println()
	opening := dimensions("mm", "100", "100", "100")
	fmt.Println("exactly 100mm fits:      ", pythonBool(dimensions("mm", "100", "100", "100").FitsInside(opening)))
	oneTickOver, err := domain.NewDimensions(units.MustLength(opening.Length().Ticks()+1), opening.Width(), opening.Height())
	check(err)
	fmt.Println("one tick over does not:  ", pythonBool(oneTickOver.FitsInside(opening)))

	// Dimensional weight, the number carriers bill on: volume over a divisor, never through
	// a float. The shipping_cost and lowest_landed_cost objectives use the same helper.
	fmt.Println()
	billed, err := domain.DimensionalWeight(dimensions("mm", "400", "400", "400"), 5000, "cm", "kg")
	check(err)
	fmt.Println("400mm cube = 64,000 cm^3 / 5,000 =", decimal(billed, "kg"), "kg dimensional weight")

	// It survives a round trip through JSON, because the wire carries the decimal string
	// rather than a float.
	fmt.Println()
	fmt.Println("on the wire:", wire(length("12 3/8 in").ToMap("mm")), wire(weight("2 3/4 lb").ToMap("g")))

	shelf := domain.NewItemSpec("shelf", dimensions("in", "12 3/8", "9 1/2", "3/4"))
	shelf.Weight, shelf.Quantity = weight("2 3/4 lb"), 3
	item, err := domain.NewItem(shelf)
	check(err)
	payload := weight("20 lb")
	carton, err := domain.NewContainer(domain.ContainerSpec{ID: "carton", InnerDimensions: dimensions("in", "13", "10", "4"), MaxPayload: &payload})
	check(err)
	balanced, err := config.New(config.Default(config.Balanced))
	check(err)
	result, err := packer.New(balanced, algorithm.NoExtensions()).Pack([]*domain.Item{item}, []*domain.Container{carton})
	check(err)
	placed := 0
	for _, container := range result.Containers {
		placed += len(container.Placements())
	}
	fmt.Println("packed", placed, "shelves,", len(result.Unpacked), "left over")
}

// wire prints a {ticks, value, unit} map the way the reference prints a dict.
func wire(fields map[string]interface{}) string {
	return fmt.Sprintf("{'ticks': %d, 'value': '%s', 'unit': '%s'}", fields["ticks"], fields["value"], fields["unit"])
}

// pythonBool spells a boolean the way the reference example prints it.
func pythonBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
