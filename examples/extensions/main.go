// Extension points: rules the schema does not have a field for.
//
// Run it from packvium-go/:
//
//	go run ./examples/extensions
//
// Most packing rules are already fields on ItemSpec and ContainerSpec -- see
// examples/constraints. This example is about the rules that are not, and it is
// deliberately paired with an honest warning about what you are giving up by using one.
//
// An extension point is an in-process, one-language interface. A custom constraint has no
// representation on the wire, so an engine driven over JSON cannot see it, and the
// cross-language conformance harness cannot check that the engines agree about it. Use
// these to specialise one application in one language. A rule that must hold for every
// caller of every binding belongs in the request as data instead -- as a policy rule, a
// tag, or a field.
package main

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/packer"
	"github.com/packvium/packvium-go/result"
	"github.com/packvium/packvium-go/units"
)

// FragileHeightLimit refuses to place a tagged item with its base above a limit.
//
// MaxTopLoad caps what may rest on an item, and MustBeOnFloor pins one to the bottom -- but
// neither says "nothing fragile above waist height, because that is where it gets knocked
// off a trolley". That is a real warehouse rule with no field, so it is a constraint.
//
// A constraint answers about one candidate position. It never searches; it is asked many
// thousands of times per solve, so keep it O(1) in the number of placements wherever you
// can. This one is: it looks at the candidate's z coordinate and nothing else.
type FragileHeightLimit struct {
	Limit units.Length
	Tag   string
}

// Evaluate allows the candidate unless a tagged item's base would sit above the limit.
func (f FragileHeightLimit) Evaluate(context constraint.Context) constraint.Result {
	if !context.Item().Item().Tags().Has(f.Tag) {
		return constraint.Allow()
	}
	// Point carries raw tick counts, not Length values -- it is built once per candidate and
	// this is the hot path.
	if context.Point().Z() <= f.Limit.Ticks() {
		return constraint.Allow()
	}
	// The code is yours. It travels into the unpacked reason, so make it something a human
	// reading a failed order will understand.
	return constraint.Reject("fragile_too_high", fmt.Sprintf("base at %smm is above the %smm limit for '%s' items",
		millimetres(context.Point().Z()), millimetres(f.Limit.Ticks()), f.Tag))
}

// EvenlyLoadedContainers prefers solutions whose containers weigh about the same.
//
// The six built-in objectives rank by space, packaging cost, billed weight, landed money,
// stack height or value. None of them cares whether the weight is spread evenly across the
// containers -- which is what a two-person lift or a van's axle balance actually depends
// on, and is nowhere in the request.
//
// Return exact integers, lower is better, compared key by key. Keep the unpacked count
// first unless you genuinely mean "leave items behind to score better", and fall back to
// the canonical vector for everything your rule does not care about -- otherwise two
// equally balanced solutions are ranked by luck.
type EvenlyLoadedContainers struct{}

// Score is the unpacked count, the load spread, then the default vector's remaining keys.
func (EvenlyLoadedContainers) Score(solution algorithm.RawSolution) algorithm.ObjectiveScore {
	spread := int64(0)
	if len(solution.Containers) > 0 {
		lightest, heaviest := load(solution.Containers[0]), load(solution.Containers[0])
		for _, packed := range solution.Containers[1:] {
			current := load(packed)
			if current < lightest {
				lightest = current
			}
			if current > heaviest {
				heaviest = current
			}
		}
		spread = heaviest - lightest
	}
	canonical := algorithm.DefaultSolutionScorer{}.Score(solution)
	return append(algorithm.ObjectiveScore{int64(len(solution.Unpacked)), spread}, canonical[1:]...)
}

func load(packed *domain.PackedContainer) int64 {
	total := int64(0)
	for _, placement := range packed.Placements() {
		total += placement.Instance().Item().Weight().Ticks()
	}
	return total
}

func main() {
	balanced, err := config.New(config.Default(config.Balanced))
	check(err)

	// The footprint is deliberately only as wide as one crate, so the column has to grow
	// upwards and the rule has something to refuse. A rule that never fires teaches nothing.
	crate := spec("crate", dims("400", "400", "300"), "12 kg", 3)
	vase := spec("vase", dims("400", "400", "200"), "2 kg", 2)
	vase.Tags = []string{"fragile"}
	scene := items(crate, vase)
	column := []*domain.Container{container(domain.ContainerSpec{ID: "column", InnerDimensions: dims("400", "400", "1300"),
		MaxPayload: payload("200 kg"), Quantity: count(1)})}

	unrestricted := pack(packer.New(balanced, algorithm.NoExtensions()), scene, column)
	highest := int64(0)
	for _, z := range vaseHeights(unrestricted) {
		if z > highest {
			highest = z
		}
	}
	fmt.Println("without the rule, the highest vase sits at", millimetres(highest), "mm")

	limit, err := units.ParseLength(units.TextQuantity("400 mm"), "mm", units.Nearest)
	check(err)
	rule := FragileHeightLimit{Limit: limit, Tag: "fragile"}
	restricted := pack(packer.New(balanced, algorithm.ExtensionsWithConstraints([]constraint.PlacementConstraint{rule})), scene, column)
	var heights []string
	for _, z := range vaseHeights(restricted) {
		heights = append(heights, millimetres(z))
	}
	sort.Strings(heights)
	fmt.Println("with a 400mm limit, vases sit at", pythonList(heights), "and", len(restricted.Unpacked), "were left behind")
	for _, unpacked := range restricted.Unpacked {
		fmt.Println("   left behind:", unpacked.Instance.Item().ID(), "->", unpacked.Reason, pythonList(unpacked.Details))
	}

	// Two heavy items and two light ones, two boxes, two slots each. Every arrangement uses
	// the same volume, so the built-in objectives are indifferent -- and land on both anvils
	// in one box. That is a 20kg box and a 2kg box.
	lopsided := items(spec("anvil", dims("200", "200", "200"), "10 kg", 2), spec("pillow", dims("200", "200", "200"), "1 kg", 2))
	twoBoxes := []*domain.Container{container(domain.ContainerSpec{ID: "box", InnerDimensions: dims("400", "200", "200"),
		MaxPayload: payload("50 kg"), Quantity: count(2)})}

	fmt.Println()
	for _, variant := range []struct {
		label  string
		scorer algorithm.SolutionScorer
	}{{"default", nil}, {"evenly loaded", EvenlyLoadedContainers{}}} {
		outcome := pack(packer.NewWith(balanced, algorithm.NoExtensions(), variant.scorer, nil), lopsided, twoBoxes)
		var contents, weights []string
		for _, packed := range outcome.Containers {
			var ids []string
			for _, placement := range packed.Placements() {
				ids = append(ids, placement.Instance().Item().ID())
			}
			sort.Strings(ids)
			contents = append(contents, pythonList(ids))
			kilograms, err := packed.PayloadWeight().Decimal("kg")
			check(err)
			weights = append(weights, kilograms+" kg")
		}
		fmt.Printf("%15s: [%s]  ->  %s\n", variant.label, strings.Join(contents, ", "), pythonList(weights))
	}

	fmt.Println()
	fmt.Println("Neither rule above exists on the wire. Hand the same request to the Rust or")
	fmt.Println("JavaScript engine and you get the unrestricted answer -- which is exactly why a")
	fmt.Println("rule everyone must obey belongs in the request as data, not in a type.")
}

func vaseHeights(outcome *result.PackingResult) []int64 {
	var heights []int64
	for _, packed := range outcome.Containers {
		for _, placement := range packed.Placements() {
			if placement.Instance().Item().ID() == "vase" {
				heights = append(heights, placement.Position().Z())
			}
		}
	}
	return heights
}

// pythonList renders strings the way the reference prints a list of them, so the output
// matches line for line.
func pythonList(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = "'" + value + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func millimetres(ticks int64) string {
	length, err := units.NewLength(ticks)
	check(err)
	text, err := length.Decimal("mm")
	check(err)
	return text
}

func pack(engine *packer.Packer, items []*domain.Item, containers []*domain.Container) *result.PackingResult {
	outcome, err := engine.Pack(items, containers)
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

func count(value int64) *int64 { return &value }

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
