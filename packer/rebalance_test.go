package packer

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/units"
	"github.com/packvium/packvium-go/validation"
)

// The tests follow the reference's test_rebalance.py one to one. They are built around the
// failure mode of a naive post-pass redistributor: an item lost or duplicated on the way, a
// move that strands another item's support, or a spread that does not actually narrow.

const sharedRebalanceFixture = "../../conformance/scene/rebalance-fixtures.json"

type rebalanceItem struct {
	id                  string
	sideMM              int64
	grams               int64
	quantity            int64
	minimumSupportRatio float64
}

func rebalanceTestItem(t *testing.T, spec rebalanceItem) *domain.Item {
	t.Helper()
	side := spec.sideMM * units.TicksPerMM
	itemSpec := domain.NewItemSpec(spec.id, domain.MustDimensions(side, side, side))
	itemSpec.Weight = units.MustWeight(spec.grams * units.TicksPerG)
	itemSpec.Quantity = 1
	if spec.quantity > 0 {
		itemSpec.Quantity = spec.quantity
	}
	itemSpec.MinimumSupportRatio = spec.minimumSupportRatio
	item, err := domain.NewItem(itemSpec)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func boxOf(t *testing.T, id string, lengthMM, widthMM, heightMM int64, edit func(*domain.ContainerSpec)) *domain.Container {
	t.Helper()
	spec := domain.ContainerSpec{ID: id, InnerDimensions: domain.MustDimensions(lengthMM*units.TicksPerMM,
		widthMM*units.TicksPerMM, heightMM*units.TicksPerMM)}
	if edit != nil {
		edit(&spec)
	}
	container, err := domain.NewContainer(spec)
	if err != nil {
		t.Fatal(err)
	}
	return container
}

func requestOf(t *testing.T, items []*domain.Item, containers ...*domain.Container) *domain.PackingRequest {
	t.Helper()
	request, err := domain.NewPackingRequest(items, containers)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

// floorPlacement is an item resting flat on the container floor, so support is trivially met.
func floorPlacement(item *domain.Item, sequence int, xMM, yMM int64) domain.Placement {
	origin := domain.MustPoint(xMM*units.TicksPerMM, yMM*units.TicksPerMM, 0)
	return domain.NewPlacement(item.Instances()[sequence-1], origin, domain.LWH, item.Dimensions(), origin,
		item.Dimensions(), 1.0)
}

// stackedPlacement is an item resting exactly on top of below, fully supported by it alone.
func stackedPlacement(item *domain.Item, sequence int, below domain.Placement) domain.Placement {
	origin := domain.MustPoint(below.EnvelopeOrigin().X(), below.EnvelopeOrigin().Y(), below.EnvelopeBox().Z2())
	return domain.NewPlacement(item.Instances()[sequence-1], origin, domain.LWH, item.Dimensions(), origin,
		item.Dimensions(), 1.0)
}

func packedOf(container *domain.Container, sequence int64, placements ...domain.Placement) *domain.PackedContainer {
	return domain.NewPackedContainer(container, sequence, placements)
}

func payloads(containers []*domain.PackedContainer) []int64 {
	weights := make([]int64, len(containers))
	for index, packed := range containers {
		weights[index] = packed.PayloadWeight().Ticks()
	}
	return weights
}

func payloadSpread(containers []*domain.PackedContainer) int64 {
	if len(containers) == 0 {
		return 0
	}
	return spreadOf(payloads(containers))
}

func movedItems(moves []WeightMove) []string {
	ids := make([]string, len(moves))
	for index, move := range moves {
		ids[index] = move.ItemID
	}
	return ids
}

func itemIDs(containers []*domain.PackedContainer) [][]string {
	ids := make([][]string, len(containers))
	for index, packed := range containers {
		ids[index] = []string{}
		for _, placement := range packed.Placements() {
			ids[index] = append(ids[index], placement.Instance().ID())
		}
	}
	return ids
}

func defaultConfig(t *testing.T) *config.PackingConfig {
	t.Helper()
	return configOf(t, config.Default(config.Balanced))
}

func rebalance(t *testing.T, request *domain.PackingRequest, containers []*domain.PackedContainer,
	unpacked []domain.UnpackedItem, cfg *config.PackingConfig, options RebalanceOptions) RebalanceResult {
	t.Helper()
	outcome, err := RebalanceWeight(request, containers, unpacked, cfg, options)
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

// requireAccountingHolds is the strongest bookkeeping property there is: every requested
// instance comes back exactly once, placed or explained.
func requireAccountingHolds(t *testing.T, request *domain.PackingRequest, containers []*domain.PackedContainer,
	unpacked []domain.UnpackedItem) {
	t.Helper()
	seen := map[string]bool{}
	for _, packed := range containers {
		for _, placement := range packed.Placements() {
			if seen[placement.Instance().ID()] {
				t.Fatalf("%s was packed twice", placement.Instance().ID())
			}
			seen[placement.Instance().ID()] = true
		}
	}
	for _, item := range unpacked {
		seen[item.Instance.ID()] = true
	}
	instances := request.Instances()
	if len(seen) != len(instances) {
		t.Fatalf("items were lost or fabricated: %d accounted for, %d requested", len(seen), len(instances))
	}
	for _, instance := range instances {
		if !seen[instance.ID()] {
			t.Fatalf("%s was lost", instance.ID())
		}
	}
}

func requireValid(t *testing.T, request *domain.PackingRequest, containers []*domain.PackedContainer,
	unpacked []domain.UnpackedItem, cfg *config.PackingConfig) {
	t.Helper()
	clearance := cfg.Clearance()
	if report := validation.ValidateWithUnpacked(request, containers, cfg.MinimumSupportRatio(), &clearance, unpacked); !report.Valid() {
		t.Fatalf("invalid packing: %v", report.Issues())
	}
}

// ------------------------------------------------------------- narrowing the spread

type sharedRebalanceCase struct {
	ExpectedMove struct {
		ItemID          string `json:"item_id"`
		FromContainerID string `json:"from_container_id"`
		ToContainerID   string `json:"to_container_id"`
	} `json:"expected_move"`
	ExpectedContainerItemIDs [][]string `json:"expected_container_item_ids"`
}

func TestAMoveStrictlyReducesThePayloadSpreadBetweenTwoContainers(t *testing.T) {
	heavy := rebalanceTestItem(t, rebalanceItem{id: "heavy", sideMM: 40, grams: 5000})
	light := rebalanceTestItem(t, rebalanceItem{id: "light", sideMM: 40, grams: 1000})
	alone := rebalanceTestItem(t, rebalanceItem{id: "alone", sideMM: 40, grams: 1000})
	boxType := boxOf(t, "box", 200, 200, 200, nil)
	request := requestOf(t, []*domain.Item{heavy, light, alone}, boxType)
	packed := []*domain.PackedContainer{
		packedOf(boxType, 1, floorPlacement(heavy, 1, 0, 0), floorPlacement(light, 1, 50, 0)),
		packedOf(boxType, 2, floorPlacement(alone, 1, 0, 0)),
	}
	if payloadSpread(packed) != 5000*units.TicksPerG {
		t.Fatalf("spread before %d", payloadSpread(packed))
	}
	cfg := defaultConfig(t)
	outcome := rebalance(t, request, packed, nil, cfg, DefaultRebalanceOptions())

	if after := payloads(outcome.Containers); !reflect.DeepEqual(after, []int64{5000 * units.TicksPerG, 2000 * units.TicksPerG}) {
		t.Fatalf("payloads after %v", after)
	}
	if !reflect.DeepEqual(outcome.Moves, []WeightMove{{"light#1", "box#1", "box#2"}}) {
		t.Fatalf("moves %v", outcome.Moves)
	}
	if !reflect.DeepEqual(itemIDs(outcome.Containers), [][]string{{"heavy#1"}, {"alone#1", "light#1"}}) {
		t.Fatalf("containers hold %v", itemIDs(outcome.Containers))
	}
	if !outcome.Improved() {
		t.Fatal("not reported as improved")
	}
	requireAccountingHolds(t, request, outcome.Containers, nil)
	requireValid(t, request, outcome.Containers, nil, cfg)

	// The same scene is a cross-language fixture beside this module; a copy of the module
	// alone does not carry it.
	content, err := ioutil.ReadFile(filepath.FromSlash(sharedRebalanceFixture))
	if os.IsNotExist(err) {
		t.Skip("the shared cross-language scene fixture is not part of this module")
	}
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Cases []sharedRebalanceCase }
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	shared := fixture.Cases[0]
	expected := WeightMove{shared.ExpectedMove.ItemID, shared.ExpectedMove.FromContainerID, shared.ExpectedMove.ToContainerID}
	if outcome.Moves[0] != expected || !reflect.DeepEqual(itemIDs(outcome.Containers), shared.ExpectedContainerItemIDs) {
		t.Fatalf("the shared fixture expects %v and %v", expected, shared.ExpectedContainerItemIDs)
	}
}

func TestAMoveThatWouldStrandASupportedItemIsDeclined(t *testing.T) {
	base := rebalanceTestItem(t, rebalanceItem{id: "base", sideMM: 40, grams: 5000})
	top := rebalanceTestItem(t, rebalanceItem{id: "top", sideMM: 40, grams: 1000, minimumSupportRatio: 1.0})
	boxType := boxOf(t, "box", 200, 200, 200, nil)
	request := requestOf(t, []*domain.Item{base, top}, boxType)
	basePlacement := floorPlacement(base, 1, 0, 0)
	packed := []*domain.PackedContainer{
		packedOf(boxType, 1, basePlacement, stackedPlacement(top, 1, basePlacement)),
		packedOf(boxType, 2),
	}
	cfg := defaultConfig(t)
	outcome := rebalance(t, request, packed, nil, cfg, DefaultRebalanceOptions())

	if ids := itemIDs(outcome.Containers); !reflect.DeepEqual(ids[0], []string{"base#1"}) {
		t.Fatalf("box#1 holds %v", ids[0])
	}
	if after := payloads(outcome.Containers); !reflect.DeepEqual(after, []int64{5000 * units.TicksPerG, 1000 * units.TicksPerG}) {
		t.Fatalf("payloads after %v", after)
	}
	if !reflect.DeepEqual(movedItems(outcome.Moves), []string{"top#1"}) {
		t.Fatalf("moves %v", outcome.Moves)
	}
	requireAccountingHolds(t, request, outcome.Containers, nil)
	requireValid(t, request, outcome.Containers, nil, cfg)
}

func TestASingleContainerHasNothingToRebalance(t *testing.T) {
	solo := rebalanceTestItem(t, rebalanceItem{id: "solo", sideMM: 40, grams: 500})
	boxType := boxOf(t, "box", 200, 200, 200, nil)
	packed := []*domain.PackedContainer{packedOf(boxType, 1, floorPlacement(solo, 1, 0, 0))}
	outcome := rebalance(t, requestOf(t, []*domain.Item{solo}, boxType), packed, nil, defaultConfig(t),
		DefaultRebalanceOptions())
	if outcome.Improved() || len(outcome.Moves) != 0 || !reflect.DeepEqual(outcome.Containers, packed) {
		t.Fatalf("moves %v", outcome.Moves)
	}
}

func TestAnAlreadyBalancedPairIsLeftUntouched(t *testing.T) {
	a := rebalanceTestItem(t, rebalanceItem{id: "a", sideMM: 40, grams: 1000})
	b := rebalanceTestItem(t, rebalanceItem{id: "b", sideMM: 40, grams: 1000})
	boxType := boxOf(t, "box", 200, 200, 200, nil)
	packed := []*domain.PackedContainer{
		packedOf(boxType, 1, floorPlacement(a, 1, 0, 0)), packedOf(boxType, 2, floorPlacement(b, 1, 0, 0)),
	}
	outcome := rebalance(t, requestOf(t, []*domain.Item{a, b}, boxType), packed, nil, nil, DefaultRebalanceOptions())
	if len(outcome.Moves) != 0 || !reflect.DeepEqual(outcome.Containers, packed) {
		t.Fatalf("moves %v", outcome.Moves)
	}
}

// --------------------------------------------------------------- randomised property

const rebalanceTrials = 20

// generatedOrder is a plausible multi-item order over one container type with a payload
// ceiling low enough that a real solve must split it across several instances.
func generatedOrder(t *testing.T, seed int64) ([]*domain.Item, []*domain.Container) {
	t.Helper()
	random := rand.New(rand.NewSource(seed))
	var items []*domain.Item
	for index := 0; index < 2+random.Intn(5); index++ {
		side := func() int64 { return (10 + 5*int64(random.Intn(6))) * units.TicksPerMM }
		spec := domain.NewItemSpec(fmt.Sprintf("i%d", index), domain.MustDimensions(side(), side(), side()))
		spec.Quantity = 1 + int64(random.Intn(3))
		spec.Weight = units.MustWeight((100 + int64(random.Intn(800))) * units.TicksPerG)
		item, err := domain.NewItem(spec)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	quantity := 2 + int64(random.Intn(3))
	payload := units.MustWeight((1 + int64(random.Intn(2))) * 1000 * units.TicksPerG)
	container := boxOf(t, "c0", 150, 150, 150, func(spec *domain.ContainerSpec) {
		spec.Quantity, spec.MaxPayload = &quantity, &payload
	})
	return items, []*domain.Container{container}
}

func balancedWithin(t *testing.T, milliseconds int64) *config.PackingConfig {
	t.Helper()
	options := config.Default(config.Balanced)
	options.TimeLimitMS = milliseconds
	return configOf(t, options)
}

func TestRebalanceNeverLosesOrDuplicatesAnItemAndNeverWidensTheSpread(t *testing.T) {
	for seed := int64(0); seed < rebalanceTrials; seed++ {
		items, containers := generatedOrder(t, seed)
		request := requestOf(t, items, containers...)
		packed, err := New(balancedWithin(t, 250), nil).Pack(items, containers)
		if err != nil {
			t.Fatal(err)
		}
		cfg := balancedWithin(t, 200)
		outcome := rebalance(t, request, packed.Containers, packed.Unpacked, cfg, DefaultRebalanceOptions())
		requireAccountingHolds(t, request, outcome.Containers, packed.Unpacked)
		requireValid(t, request, outcome.Containers, packed.Unpacked, cfg)
		if payloadSpread(outcome.Containers) > payloadSpread(packed.Containers) {
			t.Fatalf("seed %d widened the spread", seed)
		}
		if outcome.Improved() && reflect.DeepEqual(payloads(outcome.Containers), payloads(packed.Containers)) {
			t.Fatalf("seed %d claims moves that changed nothing", seed)
		}
	}
}

// --------------------------------------------------------------------- tariffs

func landedCostConfig(t *testing.T, divisor int64) *config.PackingConfig {
	t.Helper()
	options := config.Default(config.Balanced)
	options.Objective, options.DimensionalWeightDivisor = algorithm.LandedCostObjective, &divisor
	options.DimensionalWeightLengthUnit, options.DimensionalWeightWeightUnit = "cm", "kg"
	return configOf(t, options)
}

func rated(t *testing.T, id string, lengthMM, bracketG, priceMinor int64) *domain.Container {
	t.Helper()
	table, err := domain.NewRateTable([]int64{bracketG}, []int64{priceMinor}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return boxOf(t, id, lengthMM, 100, 100, func(spec *domain.ContainerSpec) { spec.RateTable = table })
}

func bricks(t *testing.T) *domain.Item {
	t.Helper()
	return rebalanceTestItem(t, rebalanceItem{id: "brick", sideMM: 100, grams: 1000, quantity: 4})
}

func containerIDs(containers []*domain.PackedContainer) []string {
	ids := make([]string, len(containers))
	for index, packed := range containers {
		ids[index] = packed.Container().ID()
	}
	return ids
}

func TestARebalanceMoveNeverPricesAContainerPastItsBracket(t *testing.T) {
	brick := bricks(t)
	wide, narrow := rated(t, "wide", 300, 4_000, 500), rated(t, "narrow", 400, 1_500, 300)
	cfg := landedCostConfig(t, 5_000)
	packed, err := New(cfg, nil).Pack([]*domain.Item{brick}, []*domain.Container{wide, narrow})
	if err != nil {
		t.Fatal(err)
	}
	if ids := containerIDs(packed.Containers); !reflect.DeepEqual(ids, []string{"wide", "narrow"}) {
		t.Fatalf("packed into %v", ids)
	}
	request := requestOf(t, []*domain.Item{brick}, wide, narrow)
	vetoed := rebalance(t, request, packed.Containers, packed.Unpacked, cfg, DefaultRebalanceOptions())
	if len(vetoed.Moves) != 0 || !reflect.DeepEqual(containerIDs(vetoed.Containers), []string{"wide", "narrow"}) {
		t.Fatalf("moves %v", vetoed.Moves)
	}
	// The veto is objective-gated: under a plain configuration the same packing moves.
	moved := rebalance(t, request, packed.Containers, packed.Unpacked, defaultConfig(t), DefaultRebalanceOptions())
	if len(moved.Moves) != 1 {
		t.Fatalf("moves %v", moved.Moves)
	}
}

func TestRebalanceRefusesAnUnpriceableInput(t *testing.T) {
	brick := bricks(t)
	wide, narrow := rated(t, "wide", 300, 4_000, 500), rated(t, "narrow", 400, 1_500, 300)
	cfg := landedCostConfig(t, 5_000)
	packed, err := New(cfg, nil).Pack([]*domain.Item{brick}, []*domain.Container{wide, narrow})
	if err != nil {
		t.Fatal(err)
	}
	// Shrink the wide card after the fact so the input itself is unpriceable.
	stingy := rated(t, "wide", 300, 1_000, 500)
	reshaped := make([]*domain.PackedContainer, len(packed.Containers))
	for index, container := range packed.Containers {
		holder := container.Container()
		if holder.ID() == "wide" {
			holder = stingy
		}
		reshaped[index] = domain.NewPackedContainer(holder, container.Sequence(), container.Placements())
	}
	_, err = RebalanceWeight(requestOf(t, []*domain.Item{brick}, stingy, narrow), reshaped, packed.Unpacked, cfg,
		DefaultRebalanceOptions())
	if _, typed := err.(*UnratedWeightError); !typed || !strings.Contains(err.Error(), "no published price") {
		t.Fatalf("got %v", err)
	}
}

func TestRebalanceAppliesTheSameLandedCostAdmissionAsPack(t *testing.T) {
	parcel := rebalanceTestItem(t, rebalanceItem{id: "parcel", sideMM: 100, grams: 500})
	table, err := domain.NewRateTable([]int64{2_000}, []int64{500}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ratedBox := boxOf(t, "rated", 200, 200, 200, func(spec *domain.ContainerSpec) { spec.RateTable = table })
	valid := landedCostConfig(t, 8_000)
	packed, err := New(valid, nil).Pack([]*domain.Item{parcel}, []*domain.Container{ratedBox})
	if err != nil {
		t.Fatal(err)
	}
	missingDivisor := config.Default(config.Balanced)
	missingDivisor.Objective = algorithm.LandedCostObjective
	_, err = RebalanceWeight(requestOf(t, []*domain.Item{parcel}, ratedBox), packed.Containers, packed.Unpacked,
		configOf(t, missingDivisor), DefaultRebalanceOptions())
	if _, typed := err.(*algorithm.UnknownObjectiveError); !typed || !strings.Contains(err.Error(), "dimensional_weight_divisor") {
		t.Fatalf("got %v", err)
	}
	untabled := boxOf(t, "untabled", 300, 300, 300, nil)
	_, err = RebalanceWeight(requestOf(t, []*domain.Item{parcel}, ratedBox, untabled), packed.Containers,
		packed.Unpacked, valid, DefaultRebalanceOptions())
	if err == nil || !strings.Contains(err.Error(), "rate_table on every container; 'untabled'") {
		t.Fatalf("got %v", err)
	}
}

// ----------------------------------------------------------- moves declined or capped

// heavyLightAlone is the hand-computed 6 kg / 1 kg scene, optionally with the lone item in a
// container type of its own so its free space can be controlled.
func heavyLightAlone(t *testing.T, aloneBox *domain.Container) (*domain.PackingRequest, []*domain.PackedContainer) {
	t.Helper()
	heavy := rebalanceTestItem(t, rebalanceItem{id: "heavy", sideMM: 40, grams: 5000})
	light := rebalanceTestItem(t, rebalanceItem{id: "light", sideMM: 40, grams: 1000})
	alone := rebalanceTestItem(t, rebalanceItem{id: "alone", sideMM: 40, grams: 1000})
	boxType := boxOf(t, "box", 200, 200, 200, nil)
	items := []*domain.Item{heavy, light, alone}
	first := packedOf(boxType, 1, floorPlacement(heavy, 1, 0, 0), floorPlacement(light, 1, 50, 0))
	if aloneBox == nil {
		return requestOf(t, items, boxType), []*domain.PackedContainer{first, packedOf(boxType, 2, floorPlacement(alone, 1, 0, 0))}
	}
	return requestOf(t, items, boxType, aloneBox),
		[]*domain.PackedContainer{first, packedOf(aloneBox, 1, floorPlacement(alone, 1, 0, 0))}
}

func TestTheMoveBudgetCapsHowManyMovesAreCommitted(t *testing.T) {
	request, packed := heavyLightAlone(t, nil)
	options := DefaultRebalanceOptions()
	options.MaxMoves = 1
	outcome := rebalance(t, request, packed, nil, defaultConfig(t), options)
	if !reflect.DeepEqual(movedItems(outcome.Moves), []string{"light#1"}) {
		t.Fatalf("moves %v", outcome.Moves)
	}
}

func TestADestinationWithNoRoomLeftReceivesNothing(t *testing.T) {
	request, packed := heavyLightAlone(t, boxOf(t, "snug", 40, 40, 40, nil))
	outcome := rebalance(t, request, packed, nil, defaultConfig(t), DefaultRebalanceOptions())
	if len(outcome.Moves) != 0 || !reflect.DeepEqual(outcome.Containers, packed) {
		t.Fatalf("moves %v", outcome.Moves)
	}
}

func TestAnItemMovedOntoAFullFloorIsStackedAndStaysSupported(t *testing.T) {
	request, packed := heavyLightAlone(t, boxOf(t, "tower", 40, 40, 80, nil))
	cfg := defaultConfig(t)
	outcome := rebalance(t, request, packed, nil, cfg, DefaultRebalanceOptions())
	if !reflect.DeepEqual(movedItems(outcome.Moves), []string{"light#1"}) {
		t.Fatalf("moves %v", outcome.Moves)
	}
	var moved *domain.Placement
	for _, packedContainer := range outcome.Containers {
		for _, placement := range packedContainer.Placements() {
			if packedContainer.ID() == "tower#1" && placement.Instance().ID() == "light#1" {
				kept := placement
				moved = &kept
			}
		}
	}
	if moved == nil || moved.Position().Z() != 40*units.TicksPerMM || moved.SupportRatio() != 1.0 {
		t.Fatalf("moved placement %+v", moved)
	}
	requireAccountingHolds(t, request, outcome.Containers, nil)
	requireValid(t, request, outcome.Containers, nil, cfg)
}

func TestAWeightlessItemIsNeverMovedSinceItCannotChangeTheSpread(t *testing.T) {
	heavy := rebalanceTestItem(t, rebalanceItem{id: "heavy", sideMM: 40, grams: 5000})
	feather := rebalanceTestItem(t, rebalanceItem{id: "feather", sideMM: 40})
	alone := rebalanceTestItem(t, rebalanceItem{id: "alone", sideMM: 40, grams: 1000})
	boxType := boxOf(t, "box", 200, 200, 200, nil)
	packed := []*domain.PackedContainer{
		packedOf(boxType, 1, floorPlacement(heavy, 1, 0, 0), floorPlacement(feather, 1, 50, 0)),
		packedOf(boxType, 2, floorPlacement(alone, 1, 0, 0)),
	}
	outcome := rebalance(t, requestOf(t, []*domain.Item{heavy, feather, alone}, boxType), packed, nil,
		defaultConfig(t), DefaultRebalanceOptions())
	if len(outcome.Moves) != 0 {
		t.Fatalf("moves %v", outcome.Moves)
	}
}

func TestADeadlineReachedMidSearchDeclinesTheMoveInsteadOfFailing(t *testing.T) {
	// The clock reads zero for the round's own check and then jumps past the limit, so the
	// candidate search itself runs out of time.
	readings := []int64{0, 0}
	restore := rebalanceDeadline
	rebalanceDeadline = func(milliseconds int64) *algorithm.Deadline {
		return algorithm.DeadlineWithClock(milliseconds, func() int64 {
			if len(readings) == 0 {
				return 1 << 62
			}
			reading := readings[0]
			readings = readings[1:]
			return reading
		})
	}
	defer func() { rebalanceDeadline = restore }()
	request, packed := heavyLightAlone(t, nil)
	outcome := rebalance(t, request, packed, nil, defaultConfig(t), DefaultRebalanceOptions())
	if len(outcome.Moves) != 0 || !reflect.DeepEqual(outcome.Containers, packed) {
		t.Fatalf("moves %v", outcome.Moves)
	}
}
