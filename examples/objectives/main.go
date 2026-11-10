// Objectives: six ways to be "best", and the scenes where they disagree.
//
// Run it from packvium-go/:
//
//	go run ./examples/objectives
//
// Every solve returns the arrangement that scores best -- but "best" is a choice, and it is
// the one setting most likely to make the library look wrong when it is merely answering a
// different question than you meant to ask. This example builds scenes where two objectives
// genuinely pick different containers, so the difference is visible rather than asserted.
//
// The score is always a lexicographic vector of exact integers, never a float, and its
// first key is always unpacked_count: no objective will ever leave an item behind to save
// money. Ratios are parts per million.
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
	"github.com/packvium/packvium-go/result"
	"github.com/packvium/packvium-go/units"
)

var widgets = []*domain.Item{item("widget", "500 g", 8, nil)}

func main() {
	snug := container("snug", "300", 500, nil, nil)
	roomy := container("roomy", "400", 150, nil, nil)
	both := []*domain.Container{snug, roomy}

	fmt.Println("default          ", describe(solve(options(""), widgets, both)))
	fmt.Println("lowest_cost      ", describe(solve(options("lowest_cost"), widgets, both)))
	fmt.Println("shipping_cost    ", describe(solve(byWeight("shipping_cost"), widgets, both)))

	dearPerGram := container("snug", "300", 0, rateTable([]int64{6_000, 20_000}, []int64{2_400, 3_100}), nil)
	cheapPerGram := container("roomy", "400", 0, rateTable([]int64{6_000, 20_000}, []int64{900, 1_500}), nil)
	fmt.Println("landed_cost      ", describe(solve(byWeight("lowest_landed_cost"), widgets, []*domain.Container{dearPerGram, cheapPerGram})))

	tooNarrow := container("roomy", "400", 0, rateTable([]int64{2_000}, []int64{900}), nil)
	if _, err := pack(byWeight("lowest_landed_cost"), widgets, []*domain.Container{tooNarrow}); err != nil {
		fmt.Println("landed_cost*     ", "refused:", err)
	}

	fmt.Println("open_dimension   ", describe(solve(options("open_dimension_height"), widgets, both)))

	one := int64(1)
	tiny := []*domain.Container{container("tiny", "", 0, nil, &one)}
	gold, gravel := int64(90_000), int64(10)
	mixed := []*domain.Item{item("gold", "500 g", 2, &gold), item("gravel", "500 g", 2, &gravel)}
	outcome := solve(options("maximum_value"), mixed, tiny)
	var kept, left []string
	for _, packed := range outcome.Containers {
		for _, placement := range packed.Placements() {
			kept = append(kept, placement.Instance().Item().ID())
		}
	}
	for _, unpacked := range outcome.Unpacked {
		left = append(left, unpacked.Instance.Item().ID())
	}
	fmt.Println("maximum_value    ", "packed:", list(kept), "left behind:", list(left))
}

func options(objective string) config.Options {
	options := config.Default(config.Balanced)
	if objective != "" {
		options.Objective = objective
	}
	return options
}

// byWeight prices billed weight at 5000 cm^3 per kg, the divisor both cost objectives need.
func byWeight(objective string) config.Options {
	options := options(objective)
	divisor := int64(5000)
	options.DimensionalWeightDivisor = &divisor
	options.DimensionalWeightLengthUnit, options.DimensionalWeightWeightUnit = "cm", "kg"
	return options
}

func pack(options config.Options, items []*domain.Item, containers []*domain.Container) (*result.PackingResult, error) {
	cfg, err := config.New(options)
	check(err)
	return packer.New(cfg, algorithm.NoExtensions()).Pack(items, containers)
}

func solve(options config.Options, items []*domain.Item, containers []*domain.Container) *result.PackingResult {
	outcome, err := pack(options, items, containers)
	check(err)
	return outcome
}

// describe prints the chosen container and the score as the reference prints a tuple.
func describe(outcome *result.PackingResult) string {
	chosen := "none"
	if len(outcome.Containers) > 0 {
		chosen = outcome.Containers[0].Container().ID()
	}
	score := make([]string, len(outcome.Score))
	for index, component := range outcome.Score {
		score[index] = fmt.Sprint(component)
	}
	return fmt.Sprintf("('%s', (%s))", chosen, strings.Join(score, ", "))
}

func list(values []string) string {
	sort.Strings(values)
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = "'" + value + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func item(id, weight string, quantity int64, value *int64) *domain.Item {
	size, err := domain.DimensionsOf("100", "100", "100", "mm")
	check(err)
	spec := domain.NewItemSpec(id, size)
	spec.Weight, spec.Quantity, spec.Value = parseWeight(weight), quantity, value
	created, err := domain.NewItem(spec)
	check(err)
	return created
}

// container is a cube of side edge mm, or the 200 x 100 x 100 "tiny" box when edge is empty.
func container(id, edge string, cost int64, rates *domain.RateTable, quantity *int64) *domain.Container {
	length, width, height := edge, edge, edge
	if edge == "" {
		length, width, height = "200", "100", "100"
	}
	size, err := domain.DimensionsOf(length, width, height, "mm")
	check(err)
	payload := parseWeight("20 kg")
	created, err := domain.NewContainer(domain.ContainerSpec{
		ID: id, InnerDimensions: size, MaxPayload: &payload, CostMinor: cost, RateTable: rates, Quantity: quantity,
	})
	check(err)
	return created
}

func rateTable(brackets, prices []int64) *domain.RateTable {
	table, err := domain.NewRateTable(brackets, prices, 0, 0)
	check(err)
	return table
}

func parseWeight(text string) units.Weight {
	value, err := units.ParseWeight(units.TextQuantity(text), "g", units.Nearest)
	check(err)
	return value
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
