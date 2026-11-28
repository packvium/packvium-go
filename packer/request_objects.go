package packer

import (
	"math/big"
	"strconv"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/canonicaljson"
	"github.com/packvium/packvium-go/internal/pointshape"
)

// checkObjects applies the request schema's rules to a request built from objects, in the
// JSON rules' order: configuration, each item, item ids, each container, container ids, fixed
// placements. The constructors already refuse most of the same values but cannot know where
// in a request they will sit; this names the reason and the RFC 6901 field the JSON path
// would. Only rules the JSON path also applies are checked, so a decoded request, which passed
// them already, is never refused here. Unordered sets (accepted stacking groups, tag limits)
// are walked in code point order. O(size of the request).
func checkObjects(cfg *config.PackingConfig, items []*domain.Item, containers []*domain.Container,
	fixed []domain.FixedPlacement) error {
	if err := checkConfiguration(cfg); err != nil {
		return err
	}
	for index, item := range items {
		if err := checkItem(item, domain.Pointer("items", strconv.Itoa(index))); err != nil {
			return err
		}
	}
	if err := requireUniqueIDs(requestItemIDs(items), "items"); err != nil {
		return err
	}
	for index, container := range containers {
		if err := checkContainer(container, domain.Pointer("containers", strconv.Itoa(index))); err != nil {
			return err
		}
	}
	if err := requireUniqueIDs(requestContainerIDs(containers), "containers"); err != nil {
		return err
	}
	for index, placement := range fixed {
		if err := checkFixedPlacement(placement, index); err != nil {
			return err
		}
	}
	return nil
}

// configurationIntegers are the configuration's integer fields in the JSON rules' order.
func configurationIntegers(cfg *config.PackingConfig) []boundedInteger {
	optional := func(value *int64, name string, minimum int64) boundedInteger {
		return boundedInteger{value, name, minimum}
	}
	value := func(number int64) *int64 { return &number }
	return []boundedInteger{
		optional(value(cfg.TimeLimitMS()), "time_limit_ms", 1), optional(value(cfg.TopK()), "alternatives", 1),
		optional(cfg.MaxContainers(), "max_containers", 1), optional(value(cfg.ExactItemLimit()), "exact_item_limit", 1),
		optional(value(cfg.MultiStartOrders()), "multi_start_orders", 1),
		optional(value(cfg.MaxCandidatesPerItem()), "max_candidates_per_item", 1),
		optional(value(cfg.MaxCandidatePoints()), "max_candidate_points", 16),
		optional(value(cfg.ContainerPlanBeamWidth()), "container_plan_beam_width", 1),
		optional(value(cfg.ContainerPlanNodeLimit()), "container_plan_node_limit", 1),
		optional(cfg.DimensionalWeightDivisor(), "dimensional_weight_divisor", 1),
	}
}

type boundedInteger struct {
	value   *int64
	name    string
	minimum int64
}

func checkConfiguration(cfg *config.PackingConfig) error {
	// The objective is not checked here: an unknown one keeps its own error on this path,
	// raised when the objective is resolved.
	where := "/configuration"
	if err := checkIntegers(configurationIntegers(cfg), where); err != nil {
		return err
	}
	if err := ratio(cfg.MinimumSupportRatio(), where+"/minimum_support_ratio"); err != nil {
		return err
	}
	budget := cfg.EffortBudget()
	if budget == nil {
		return nil
	}
	return checkIntegers([]boundedInteger{
		{budget.MaxCandidatesEvaluated, "max_candidates_evaluated", 1},
		{budget.MaxPlacementAttempts, "max_placement_attempts", 1},
		{budget.MaxSearchNodes, "max_search_nodes", 1}, {budget.MaxRestarts, "max_restarts", 1},
	}, where+"/effort_budget")
}

func checkIntegers(fields []boundedInteger, where string) error {
	for _, field := range fields {
		if field.value == nil {
			continue
		}
		if err := integer(*field.value, where+domain.Pointer(field.name), field.minimum); err != nil {
			return err
		}
	}
	return nil
}

func checkItem(item *domain.Item, where string) error {
	quantity := item.Quantity()
	if err := checkIntegers([]boundedInteger{
		{&quantity, "quantity", 1}, {item.MaxStackedItems(), "max_stacked_items", 1},
		{item.StopIndex(), "stop_index", 0}, {item.Value(), "value", 0},
		{item.MaxCompressionPressureKPA(), "max_compression_pressure_kpa", 0},
	}, where); err != nil {
		return err
	}
	if err := ratio(item.MinimumSupportRatio(), where+"/minimum_support_ratio"); err != nil {
		return err
	}
	if group := item.StackingGroup(); group != nil {
		if err := stackingGroupName(*group, where+"/stacking_group"); err != nil {
			return err
		}
	}
	for index, name := range item.AcceptsStackingGroups().Sorted() {
		if err := stackingGroupName(name, where+domain.Pointer("accepts_stacking_groups", strconv.Itoa(index))); err != nil {
			return err
		}
	}
	return nil
}

func checkContainer(container *domain.Container, where string) error {
	cost := container.CostMinor()
	if err := checkIntegers([]boundedInteger{
		{container.Quantity(), "quantity", 1}, {container.MaxItems(), "max_items", 1}, {&cost, "cost_minor", 0},
	}, where); err != nil {
		return err
	}
	if err := ratio(container.VoidFillReserveRatio(), where+"/void_fill_reserve_ratio"); err != nil {
		return err
	}
	for _, tag := range container.TagLimitNames() {
		limit, _ := container.TagLimit(tag)
		if err := integer(limit, where+domain.Pointer("tag_limits", tag), 1); err != nil {
			return err
		}
	}
	if table := container.RateTable(); table != nil {
		if err := checkRateTable(table, where+"/rate_table"); err != nil {
			return err
		}
	}
	if container.IsFlatBag() {
		if err := checkFlatBag(container, where); err != nil {
			return err
		}
	}
	if machine := container.MadeToMeasure(); machine != nil {
		return checkMadeToMeasure(container, machine, where)
	}
	return nil
}

func checkRateTable(table *domain.RateTable, where string) error {
	for _, list := range []struct {
		name    string
		values  []int64
		minimum int64
	}{{"weight_brackets_g", table.WeightBracketsG(), 1}, {"prices_minor", table.PricesMinor(), 0}} {
		for index, value := range list.values {
			if err := integer(value, where+domain.Pointer(list.name, strconv.Itoa(index)), list.minimum); err != nil {
				return err
			}
		}
	}
	minimum, fuel := table.MinimumChargeMinor(), table.FuelSurchargePermille()
	return checkIntegers([]boundedInteger{{&minimum, "minimum_charge_minor", 0}, {&fuel, "fuel_surcharge_permille", 0}},
		where)
}

// checkFlatBag names the first field a bag cannot carry, in the JSON rules' order. The
// constructor refuses all but the made-to-measure machine already, without a field.
func checkFlatBag(container *domain.Container, where string) error {
	if container.DeformableBoundary() != domain.FlatBag {
		return domain.NewInvalidRequestError("not_allowed", where+"/deformable_boundary/kind",
			"must be one of "+canonicaljson.Spelling([]interface{}{domain.FlatBag}))
	}
	beside := []struct {
		name    string
		present bool
	}{
		{"obstacles", len(container.Obstacles()) > 0}, {"axles", container.Axles() != nil},
		{"access_directions", len(container.AccessDirections()) > 0},
		{"max_stack_density", container.MaxStackDensity() != nil},
		{"outer_dimensions", container.OuterDimensions() != nil},
		{"void_fill_reserve_ratio", container.VoidFillReserveRatio() > 0},
		{"made_to_measure", container.MadeToMeasure() != nil},
	}
	for _, field := range beside {
		if field.present {
			return domain.NewInvalidRequestError("not_allowed", where+domain.Pointer(field.name),
				"cannot be combined with deformable_boundary")
		}
	}
	return nil
}

func checkMadeToMeasure(container *domain.Container, machine *domain.MadeToMeasure, where string) error {
	price := where + "/made_to_measure/cost_per_square_metre_minor"
	if err := integer(machine.CostPerSquareMetreMinor(), price, 0); err != nil {
		return err
	}
	if container.OuterDimensions() != nil {
		return domain.NewInvalidRequestError("not_allowed", where+"/outer_dimensions",
			"cannot be combined with made_to_measure")
	}
	largest := domain.BoxCostMinorFor(machine.CostPerSquareMetreMinor(), container.CostMinor(),
		domain.Triple(container.InnerDimensions()))
	if largest.Cmp(big.NewInt(canonicaljson.MaxExactMagnitude)) > 0 {
		return domain.NewInvalidRequestError("invalid_value", price,
			"prices the largest box above "+strconv.FormatInt(canonicaljson.MaxExactMagnitude, 10))
	}
	return nil
}

// checkFixedPlacement holds the fixed-placement shape rules an object can break, in their
// order and with their refusal; whether the set can hold is admitFixedPlacements' to say.
func checkFixedPlacement(placement domain.FixedPlacement, index int) error {
	where := "fixed_placements[" + strconv.Itoa(index) + "]"
	field := domain.Pointer("fixed_placements", strconv.Itoa(index))
	for _, name := range []struct{ json, value string }{
		{"item_type", placement.ItemID()}, {"container_type", placement.ContainerID()},
	} {
		if name.value == "" {
			return domain.NewFixedPlacementError("malformed", field+"/"+name.json, where+"."+name.json+" is a non-empty string")
		}
	}
	if !pointshape.IsOrientation(string(placement.Rotation())) {
		return domain.NewFixedPlacementError("malformed", field+"/orientation",
			where+".orientation "+pointshape.OrientationRule())
	}
	if placement.ContainerInstance() < 1 || placement.ContainerInstance() > canonicaljson.MaxExactMagnitude {
		return domain.NewFixedPlacementError("malformed", field+"/container_instance",
			where+".container_instance counts from 1")
	}
	return nil
}

// integer is the JSON rules' integer floor over a typed value: a whole number past
// 2^53 - 1 is out of range, which way said. O(1).
func integer(value int64, field string, minimum int64) error {
	if value < minimum {
		return domain.NewInvalidRequestError("below_minimum", field, "must be at least "+strconv.FormatInt(minimum, 10))
	}
	if value > canonicaljson.MaxExactMagnitude {
		return domain.NewInvalidRequestError("above_maximum", field,
			"must be at most "+strconv.FormatInt(canonicaljson.MaxExactMagnitude, 10))
	}
	return nil
}

// ratio is the JSON rules' ratio: from 0 to 1 inclusive. A NaN never reaches it: every
// constructor that holds a ratio refuses one first.
func ratio(value float64, field string) error {
	if value < 0 {
		return domain.NewInvalidRequestError("below_minimum", field, "must be at least 0")
	}
	if value > 1 {
		return domain.NewInvalidRequestError("above_maximum", field, "must be at most 1")
	}
	return nil
}

func stackingGroupName(name, field string) error {
	if name == "" {
		return domain.NewInvalidRequestError("invalid_value", field, "must not be empty")
	}
	return nil
}

func requestItemIDs(items []*domain.Item) []string {
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.ID()
	}
	return ids
}

func requestContainerIDs(containers []*domain.Container) []string {
	ids := make([]string, len(containers))
	for index, container := range containers {
		ids[index] = container.ID()
	}
	return ids
}

// requireUniqueIDs names the later of two equal ids, as the JSON path does.
func requireUniqueIDs(ids []string, key string) error {
	seen := make(map[string]bool, len(ids))
	for index, id := range ids {
		if seen[id] {
			return domain.NewInvalidRequestError("duplicate_id", domain.Pointer(key, strconv.Itoa(index), "id"),
				"repeats the id "+canonicaljson.Spelling(id))
		}
		seen[id] = true
	}
	return nil
}
