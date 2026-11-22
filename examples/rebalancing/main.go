// Even out the weight across pallets after packing.
//
// Run it from packvium-go/:
//
//	go run ./examples/rebalancing
//
// The packer opens as few containers as it can and fills the first one tightly. For two
// pallets on a forklift, that is the wrong shape of answer: one pallet near its weight limit
// and one nearly empty, when two pallets of similar weight are safer to lift, wrap and stack.
//
// packer.RebalanceWeight is a separate, opt-in step you run on a finished packing. It moves
// one item at a time from the heaviest container to a lighter one, and keeps a move only when
// the item has a real place in its new container and the whole packing still passes the
// independent validator. It never adds a container, never drops an item, and stops when no
// single move narrows the gap (or after MaxMoves, 64 by default). It is greedy, so the result
// is better, not necessarily the most even split possible.
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/packer"
	"github.com/packvium/packvium-go/units"
	"github.com/packvium/packvium-go/validation"
)

func main() {
	paint := domain.NewItemSpec("paint-20l", dimensions("300", "300", "380"))
	paint.Weight, paint.Quantity, paint.KeepUpright = weight("24 kg"), 16, true
	brushes := domain.NewItemSpec("brush-case", dimensions("400", "300", "200"))
	brushes.Weight, brushes.Quantity = weight("3 kg"), 10
	items := []*domain.Item{item(paint), item(brushes)}
	tare, payload := weight("25 kg"), weight("300 kg")
	pallet, err := domain.NewContainer(domain.ContainerSpec{ID: "pallet", InnerDimensions: dimensions("1200", "800", "1000"),
		TareWeight: tare, MaxPayload: &payload})
	check(err)
	pallets := []*domain.Container{pallet}

	// Counted work, not a clock, decides the plan (see examples/reproducibility); the time
	// limits here and in RebalanceWeight below are fuses far above what either step needs.
	options := config.Default(config.Balanced)
	restarts := int64(8)
	options.TimeLimitMS, options.EffortBudget = 60_000, &config.EffortBudget{MaxRestarts: &restarts}
	cfg, err := config.New(options)
	check(err)
	rebalancing := packer.DefaultRebalanceOptions()
	rebalancing.TimeLimitMS = 60_000

	section("1. As packed: as few pallets as possible, the first one full")
	result, err := packer.New(cfg, nil).Pack(items, pallets)
	check(err)
	fmt.Printf("  %s\n", weights(result.Containers))

	section("2. Rebalanced")
	// The request is what the validator checks the new packing against: every item that was
	// asked for must still be accounted for, exactly once.
	request, err := domain.NewPackingRequest(items, pallets)
	check(err)
	rebalanced, err := packer.RebalanceWeight(request, result.Containers, result.Unpacked, cfg, rebalancing)
	check(err)
	for _, move := range rebalanced.Moves {
		fmt.Printf("  moved %s from %s to %s\n", move.ItemID, move.FromContainerID, move.ToContainerID)
	}
	fmt.Printf("  %s\n", weights(rebalanced.Containers))
	clearance := cfg.Clearance()
	report := validation.ValidateWithUnpacked(request, rebalanced.Containers, cfg.MinimumSupportRatio(), &clearance,
		result.Unpacked)
	fmt.Printf("  still a valid packing: %t\n", report.Valid())
	fmt.Println(`
  Heavy items move first, because each one narrows the gap most. The paint stays
  upright in its new place and nothing is left resting on a gap where a moved can
  used to be -- a move that would do that is rejected, not made.`)

	section("3. When there is nothing to improve")
	single, err := packer.New(cfg, nil).Pack(items[1:], pallets)
	check(err)
	singleRequest, err := domain.NewPackingRequest(items[1:], pallets)
	check(err)
	unchanged, err := packer.RebalanceWeight(singleRequest, single.Containers, single.Unpacked, cfg, rebalancing)
	check(err)
	fmt.Printf("  %s  improved: %t\n", weights(unchanged.Containers), unchanged.Improved())
	fmt.Println("\n  One pallet has nothing to balance against, and the result says so rather than\n" +
		"  inventing a second pallet. The same holds when every candidate move would break a\n" +
		"  rule: `Moves` is empty and the packing comes back as it was.")
}

func weights(containers []*domain.PackedContainer) string {
	parts := make([]string, len(containers))
	for index, packed := range containers {
		kilograms, err := packed.PayloadWeight().Decimal("kg")
		check(err)
		parts[index] = fmt.Sprintf("%s %s kg (%d items)", packed.ID(), kilograms, packed.PlacementCount())
	}
	return strings.Join(parts, ", ")
}

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", 78))
}

func dimensions(length, width, height string) domain.Dimensions {
	value, err := domain.DimensionsOf(length, width, height, "mm")
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

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
