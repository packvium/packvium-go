// Load a delivery van for a three-stop route, within its axle ratings.
//
// Run it from packvium-go/:
//
//	go run ./examples/trucking
//
// A van is not a big box. Its payload rides on two axles, and each axle has its own rating
// that the total payload limit does not protect: a van can be under its payload and still
// overload the front axle. Its cargo also comes out through one door, in the order the stops
// come up, so the goods for the first stop must not be walled in by the goods for the last
// one.
//
// Three fields carry that:
//
//   - Axles on the container: [front, rear], each a position along the length and an
//     optional maximum load. Checked for every placement against the gross load (payload
//     plus tare), so no plan puts either axle over its rating.
//   - AccessDirections on the container: the walls cargo can leave through. "+x" is the wall
//     at the far end of the length axis -- here, the rear doors.
//   - StopIndex on each item: which stop it is delivered at, 0 first.
//
// After the solve, the sequence functions answer the questions a driver asks: in what order
// do things come off, in what order must they go on, and what can I reach when I open the
// doors at the first stop?
package main

import (
	"errors"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strings"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/packer"
	"github.com/packvium/packvium-go/result"
	"github.com/packvium/packvium-go/sequence"
	"github.com/packvium/packvium-go/serialization"
	"github.com/packvium/packvium-go/units"
)

var rearDoors = []string{"+x"}

// Length runs from the cab bulkhead (x = 0) to the rear doors. The axle positions are
// measured along that same axis, which is what lets the engine take moments about them.
var (
	frontAxle = axle("600", "1650 kg")
	rearAxle  = axle("3500", "2200 kg")
)

func van(front domain.Axle) *domain.Container {
	// Tare matters here: axle ratings are gross limits, and the empty van already puts most
	// of the front rating to use before anything is loaded.
	payload := weight("1300 kg")
	axles := [2]domain.Axle{front, rearAxle}
	container, err := domain.NewContainer(domain.ContainerSpec{ID: "van", InnerDimensions: dimensions("4200", "1800", "1900"),
		TareWeight: weight("2400 kg"), MaxPayload: &payload, Axles: &axles, AccessDirections: rearDoors})
	check(err)
	return container
}

func manifest(withRoute bool) []*domain.Item {
	stop := func(spec *domain.ItemSpec, index int64) *domain.Item {
		if withRoute {
			spec.StopIndex = &index
		}
		value, err := domain.NewItem(*spec)
		check(err)
		return value
	}
	rack := itemSpec("bakery-rack", "800", "600", "1700", "90 kg", 2)
	rack.KeepUpright = true
	drinks := itemSpec("drinks-pallet", "1200", "800", "1100", "420 kg", 1)
	drinks.KeepUpright, drinks.MustBeOnFloor = true, true
	parcel := itemSpec("parcel", "600", "400", "400", "18 kg", 6)
	dryGoods := itemSpec("dry-goods-pallet", "1200", "800", "1400", "260 kg", 1)
	dryGoods.KeepUpright = true
	return []*domain.Item{stop(&rack, 0), stop(&drinks, 1), stop(&parcel, 2), stop(&dryGoods, 2)}
}

// configuration lets the effort budget decide where the search stops, so every run gives
// this same plan; the time limit is only a fuse far above what this needs (see
// examples/reproducibility). One van, so anything that does not fit is reported, not sent in
// a second vehicle.
func configuration() *config.PackingConfig {
	options := config.Default(config.Balanced)
	restarts, containers := int64(8), int64(1)
	options.TimeLimitMS, options.EffortBudget = 60_000, &config.EffortBudget{MaxRestarts: &restarts}
	options.MaxContainers = &containers
	cfg, err := config.New(options)
	check(err)
	return cfg
}

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", 78))
}

func millimetres(ticks int64) string {
	return units.RenderDecimal(big.NewInt(ticks), big.NewInt(units.TicksPerMM), 1)
}

func kilograms(ticks *big.Int) string {
	return units.RenderDecimal(ticks, big.NewInt(units.TicksPerKG), 1)
}

// axleLoadsKG are the exact axle reactions from the result document, in kilograms. They
// arrive as a numerator over a shared denominator so no engine has to round them; rounding
// happens here, for display only, to the nearest tick with ties to even.
func axleLoadsKG(packed *result.PackingResult) (string, string) {
	document := serialization.EncodeResult(packed, "mm", "g")
	reactions := document["containers"].([]interface{})[0].(map[string]interface{})["axle_reactions"].(map[string]interface{})
	denominator := bigInteger(reactions["denominator"].(string))
	inKilograms := func(numerator string) string {
		return kilograms(roundHalfEven(bigInteger(numerator), denominator))
	}
	return inKilograms(reactions["front_numerator"].(string)), inKilograms(reactions["rear_numerator"].(string))
}

func roundHalfEven(numerator, denominator *big.Int) *big.Int {
	quotient, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	twice := new(big.Int).Mul(new(big.Int).Abs(remainder), big.NewInt(2))
	switch twice.Cmp(denominator) {
	case 1:
		return quotient.Add(quotient, big.NewInt(int64(numerator.Sign())))
	case 0:
		if quotient.Bit(0) == 1 {
			return quotient.Add(quotient, big.NewInt(int64(numerator.Sign())))
		}
	}
	return quotient
}

func stopOf(placement domain.Placement) int64 { return *placement.Instance().Item().StopIndex() }

func main() {
	cfg := configuration()
	inside := van(frontAxle).InnerDimensions()

	section("1. The plan: nothing for a later stop stands in an earlier stop's way out")
	planned, err := packer.New(cfg, nil).Pack(manifest(true), []*domain.Container{van(frontAxle)})
	check(err)
	packed := planned.Containers[0]
	placements := packed.Placements()
	fmt.Printf("  status: %s, left behind: %d\n", planned.Status, len(planned.Unpacked))
	byPosition := append([]domain.Placement(nil), placements...)
	sort.SliceStable(byPosition, func(i, j int) bool {
		a, b := byPosition[i].Position(), byPosition[j].Position()
		if a.X() != b.X() {
			return a.X() < b.X()
		}
		if a.Y() != b.Y() {
			return a.Y() < b.Y()
		}
		return a.Z() < b.Z()
	})
	for _, placement := range byPosition {
		fmt.Printf("    stop %d  %-20s x=%6s mm  y=%6s mm\n", stopOf(placement), placement.Instance().ID(),
			millimetres(placement.Position().X()), millimetres(placement.Position().Y()))
	}
	fmt.Println("\n  x is the distance from the bulkhead, so the rear doors are at x = 4200 mm. The\n" +
		"  bakery racks sit at the front, but the aisle beside them runs clear to the doors:\n" +
		"  the rule is that nothing due later blocks every way out, not that stop 0 is last in.")

	section("2. Axle loads against their ratings")
	front, rear := axleLoadsKG(planned)
	fmt.Printf("  payload: %s kg of 1300 kg allowed\n", kilograms(big.NewInt(packed.PayloadWeight().Ticks())))
	fmt.Printf("  front axle: %s kg of %s kg\n", front, kilograms(big.NewInt(frontAxle.MaxLoad().Ticks())))
	fmt.Printf("  rear axle:  %s kg of %s kg\n", rear, kilograms(big.NewInt(rearAxle.MaxLoad().Ticks())))
	tight, err := packer.New(cfg, nil).Pack(manifest(true), []*domain.Container{van(axle("600", "1450 kg"))})
	check(err)
	fmt.Println()
	fmt.Println("  the same load with a 1450 kg front axle rating:")
	for _, unpacked := range tight.Unpacked {
		fmt.Printf("    left behind: %s, reason %s (%s)\n", unpacked.Instance.ID(), unpacked.Reason, unpacked.Proof.Level)
	}
	fmt.Println("\n  The axle rule refuses a placement rather than reporting an overload afterwards.\n" +
		"  Note the reason: an item the axles cannot carry is reported as the generic\n" +
		"  `no_feasible_placement`, not as an axle problem, so when a load comes up short on\n" +
		"  a vehicle with axle ratings, check the reactions before shopping for a bigger van.")

	section("3. Unloading order along the route, and the loading order it implies")
	boxes := make([]domain.Box, len(placements))
	stops := make([]*int64, len(placements))
	for index, placement := range placements {
		boxes[index], stops[index] = placement.EnvelopeBox(), placement.Instance().Item().StopIndex()
	}
	unloading, err := sequence.SafeRouteRemovalOrder(boxes, stops, inside, rearDoors)
	check(err)
	for step, index := range unloading {
		fmt.Printf("    off %2d: stop %d  %s\n", step+1, *stops[index], placements[index].Instance().ID())
	}
	// Loading is unloading played backwards: whatever comes off last goes on first.
	// ReplayLoadingOrder checks that independently -- every item supported by what is
	// already in, and each one able to travel in through the doors -- and refuses if not.
	loading := make([]int, len(unloading))
	for index, step := range unloading {
		loading[len(unloading)-1-index] = step
	}
	check(sequence.ReplayLoadingOrder(boxes, inside, loading, rearDoors))
	names := make([]string, len(loading))
	for index, step := range loading {
		names[index] = placements[step].Instance().ID()
	}
	fmt.Println()
	fmt.Println("  load in this order:", strings.Join(names, ", "))

	section("4. What the driver can reach on opening the doors at stop 0")
	reachability, err := sequence.PlacementReachability(boxes, inside, stops, rearDoors)
	check(err)
	for _, reach := range reachability {
		if *stops[reach.Index()] != 0 {
			continue
		}
		var blockers []string
		for _, index := range union(reach.BlockedByNeighbors(), reach.BlockedBySupport()) {
			blockers = append(blockers, placements[index].Instance().ID())
		}
		sort.Strings(blockers)
		line := fmt.Sprintf("    %s: reachable=%s", placements[reach.Index()].Instance().ID(), pythonBool(reach.Reachable()))
		if len(blockers) > 0 {
			line += ", behind " + strings.Join(blockers, ", ")
		}
		fmt.Println(line)
	}
	fmt.Println("\n  Reachability is a snapshot, not a plan: one rack is behind the other, which is\n" +
		"  fine because both are for this stop and the first one comes off first.")

	section("5. The same goods without `stop_index`")
	unrouted, err := packer.New(cfg, nil).Pack(manifest(false), []*domain.Container{van(frontAxle)})
	check(err)
	unroutedPlacements := unrouted.Containers[0].Placements()
	due := map[string]int64{"bakery-rack": 0, "drinks-pallet": 1, "parcel": 2, "dry-goods-pallet": 2}
	unroutedBoxes := make([]domain.Box, len(unroutedPlacements))
	unroutedStops := make([]*int64, len(unroutedPlacements))
	for index, placement := range unroutedPlacements {
		stop := due[placement.Instance().Item().ID()]
		unroutedBoxes[index], unroutedStops[index] = placement.EnvelopeBox(), &stop
	}
	_, err = sequence.SafeRouteRemovalOrder(unroutedBoxes, unroutedStops, inside, rearDoors)
	var stuck *sequence.RouteSequenceError
	if errors.As(err, &stuck) {
		var stranded []string
		for _, index := range stuck.Stuck() {
			stranded = append(stranded, unroutedPlacements[index].Instance().ID())
		}
		sort.Strings(stranded)
		fmt.Printf("  status %s, every item packed -- and at stop %d the driver cannot get these out: %s\n",
			unrouted.Status, stuck.Stop(), strings.Join(stranded, ", "))
	}
	fmt.Println("\n  Without the route the plan is still a valid packing, and still within the axle\n" +
		"  ratings; it just cannot be delivered in order. The route is only enforced when\n" +
		"  you state it.")
}

// union is the indices in either ascending list, once each.
func union(left, right []int) []int {
	seen := map[int]bool{}
	var all []int
	for _, index := range append(append([]int(nil), left...), right...) {
		if !seen[index] {
			seen[index] = true
			all = append(all, index)
		}
	}
	return all
}

// pythonBool keeps the reference's spelling of the flag, True or False.
func pythonBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

func bigInteger(text string) *big.Int {
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		log.Fatalf("not an integer: %q", text)
	}
	return value
}

func axle(positionMM, maxLoad string) domain.Axle {
	position, err := units.ParseLength(units.TextQuantity(positionMM), "mm", units.Nearest)
	check(err)
	load := weight(maxLoad)
	return domain.NewAxle(position, &load)
}

func itemSpec(id, length, width, height, mass string, quantity int64) domain.ItemSpec {
	spec := domain.NewItemSpec(id, dimensions(length, width, height))
	spec.Weight, spec.Quantity = weight(mass), quantity
	return spec
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

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
