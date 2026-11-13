// Package serialization is the JSON adapter: a wire request in, a wire result out.
package serialization

import (
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/packvium/packvium-go/compression"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/hull"
	"github.com/packvium/packvium-go/units"
)

const weightUnit = "g"

// DecodedRequest is a wire request read into the domain, with the output units it asked for.
type DecodedRequest struct {
	Config              *config.PackingConfig
	Items               []*domain.Item
	Containers          []*domain.Container
	OutputLengthUnit    string
	OutputWeightUnit    string
	CatalogVersionsUsed []interface{}
	Policy              interface{}
}

// Decode reads a wire request in the reference's order, so a malformed request earns the
// same first refusal it does there.
func Decode(raw interface{}) (*DecodedRequest, error) {
	request, ok := raw.(map[string]interface{})
	if !ok {
		return nil, errors.New("request must be an object")
	}
	if err := RejectUnsupported(request, CurrentlyUnsupported); err != nil {
		return nil, err
	}
	fields := object(request)
	unitsBlock, err := child(fields, "units")
	if err != nil {
		return nil, err
	}
	unit := unitsBlock.text("length", "mm")
	configuration, err := child(fields, "configuration")
	if err != nil {
		return nil, err
	}
	packingConfig, err := decodeConfig(configuration, unit)
	if err != nil {
		return nil, err
	}
	references, err := catalogVersionsUsed(fields)
	if err != nil {
		return nil, err
	}
	policy, _ := fields.get("policy")
	items, err := decodeAll(fields, "items")
	if err != nil {
		return nil, err
	}
	decodedItems := make([]*domain.Item, 0, len(items))
	for _, entry := range items {
		item, err := decodeItem(entry, unit)
		if err != nil {
			return nil, err
		}
		decodedItems = append(decodedItems, item)
	}
	containers, err := decodeAll(fields, "containers")
	if err != nil {
		return nil, err
	}
	decodedContainers := make([]*domain.Container, 0, len(containers))
	for _, entry := range containers {
		container, err := decodeContainer(entry, unit)
		if err != nil {
			return nil, err
		}
		decodedContainers = append(decodedContainers, container)
	}
	output, err := child(fields, "output")
	if err != nil {
		return nil, err
	}
	return &DecodedRequest{
		Config: packingConfig, Items: decodedItems, Containers: decodedContainers,
		OutputLengthUnit: output.text("length_unit", unit), OutputWeightUnit: output.text("weight_unit", weightUnit),
		CatalogVersionsUsed: references, Policy: policy,
	}, nil
}

func child(fields object, key string) (object, error) {
	value, _ := fields.get(key)
	return asObject(value, key)
}

func decodeAll(fields object, key string) ([]object, error) {
	value, ok := fields.get(key)
	if !ok {
		return nil, fmt.Errorf("'%s'", key)
	}
	list, err := asList(value, key)
	if err != nil {
		return nil, err
	}
	entries := make([]object, len(list))
	for index, element := range list {
		entry, ok := element.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%s entries must be objects", key)
		}
		entries[index] = object(entry)
	}
	return entries, nil
}

func decodeConfig(cfg object, unit string) (*config.PackingConfig, error) {
	profile, err := config.ParseSolverProfile(cfg.text("solver_profile", "balanced"))
	if err != nil {
		return nil, err
	}
	quality := profile == config.Quality
	options := config.Default(config.Balanced)
	options.Profile = profile
	breadth, nodes := int64(1), int64(1)
	if quality {
		breadth, nodes = 16, 100_000
	}
	readers := []struct {
		key      string
		fallback int64
		target   *int64
	}{
		{"time_limit_ms", 1000, &options.TimeLimitMS}, {"alternatives", 3, &options.TopK}, {"seed", 42, &options.Seed},
	}
	for _, reader := range readers {
		if *reader.target, err = cfg.integer(reader.key, reader.fallback); err != nil {
			return nil, err
		}
	}
	if options.MaxContainers, err = cfg.optionalInteger("max_containers"); err != nil {
		return nil, err
	}
	clearance := interface{}(int64(0))
	if value, ok := cfg.get("clearance"); ok {
		clearance = value
	}
	if options.Clearance, err = parseLength(clearance, unit); err != nil {
		return nil, err
	}
	if options.MinimumSupportRatio, err = cfg.number("minimum_support_ratio", 0); err != nil {
		return nil, err
	}
	later := []struct {
		key      string
		fallback int64
		target   *int64
	}{
		{"exact_item_limit", 7, &options.ExactItemLimit}, {"multi_start_orders", 8, &options.MultiStartOrders},
		{"max_candidates_per_item", breadth, &options.MaxCandidatesPerItem},
		{"max_candidate_points", 4096, &options.MaxCandidatePoints},
	}
	for _, reader := range later {
		if *reader.target, err = cfg.integer(reader.key, reader.fallback); err != nil {
			return nil, err
		}
	}
	if options.Solvers, err = cfg.strings("solvers"); err != nil {
		return nil, err
	}
	options.Objective = cfg.text("objective", "default")
	if options.EffortBudget, err = effortBudget(cfg); err != nil {
		return nil, err
	}
	if options.DimensionalWeightDivisor, err = cfg.optionalInteger("dimensional_weight_divisor"); err != nil {
		return nil, err
	}
	options.DimensionalWeightLengthUnit = cfg.text("dimensional_weight_length_unit", "in")
	options.DimensionalWeightWeightUnit = cfg.text("dimensional_weight_weight_unit", "lb")
	options.RequirePlacementCoordinates = cfg.boolean("require_placement_coordinates", true)
	if options.ContainerPlanBeamWidth, err = cfg.integer("container_plan_beam_width", breadth); err != nil {
		return nil, err
	}
	if options.ContainerPlanNodeLimit, err = cfg.integer("container_plan_node_limit", nodes); err != nil {
		return nil, err
	}
	return config.New(options)
}

func effortBudget(cfg object) (*config.EffortBudget, error) {
	value, ok := cfg.get("effort_budget")
	if !ok {
		return nil, nil
	}
	raw, err := asObject(value, "effort_budget")
	if err != nil {
		return nil, err
	}
	budget := &config.EffortBudget{}
	targets := []struct {
		key    string
		target **int64
	}{
		{"max_candidates_evaluated", &budget.MaxCandidatesEvaluated}, {"max_placement_attempts", &budget.MaxPlacementAttempts},
		{"max_search_nodes", &budget.MaxSearchNodes}, {"max_restarts", &budget.MaxRestarts},
	}
	for _, field := range targets {
		if *field.target, err = raw.optionalInteger(field.key); err != nil {
			return nil, err
		}
	}
	return budget, nil
}

func decodeDimensions(value interface{}, unit string) (domain.Dimensions, error) {
	raw, err := asObject(value, "dimensions")
	if err != nil {
		return domain.Dimensions{}, err
	}
	var sides [3]units.Length
	for index, key := range []string{"length", "width", "height"} {
		side, ok := raw[key]
		if !ok {
			return domain.Dimensions{}, fmt.Errorf("'%s'", key)
		}
		if sides[index], err = parseLength(side, unit); err != nil {
			return domain.Dimensions{}, err
		}
	}
	return domain.NewDimensions(sides[0], sides[1], sides[2])
}

func optionalWeight(fields object, key string) (*units.Weight, error) {
	value, ok := fields.get(key)
	if !ok {
		return nil, nil
	}
	weight, err := parseWeight(value, weightUnit)
	return &weight, err
}

func optionalText(fields object, key string) *string {
	value, ok := fields.get(key)
	if !ok {
		return nil
	}
	text := toText(value)
	return &text
}

func decodeItem(raw object, unit string) (*domain.Item, error) {
	rotations := domain.AllRotations()
	if value, ok := raw.get("allowed_rotations"); ok {
		names, err := asList(value, "allowed_rotations")
		if err != nil {
			return nil, err
		}
		rotations = make([]domain.Rotation, 0, len(names))
		for _, name := range names {
			rotation, err := domain.ParseRotation(toText(name))
			if err != nil {
				return nil, err
			}
			rotations = append(rotations, rotation)
		}
	}
	id, ok := raw.get("id")
	if !ok {
		return nil, errors.New("'id'")
	}
	dimensionsValue, ok := raw.get("dimensions")
	if !ok {
		return nil, errors.New("'dimensions'")
	}
	dimensions, err := decodeDimensions(dimensionsValue, unit)
	if err != nil {
		return nil, err
	}
	spec := domain.NewItemSpec(toText(id), dimensions)
	spec.AllowedRotations = rotations
	if value, ok := raw.get("weight"); ok {
		if spec.Weight, err = parseWeight(value, weightUnit); err != nil {
			return nil, err
		}
	}
	if spec.Quantity, err = raw.integer("quantity", 1); err != nil {
		return nil, err
	}
	spec.KeepUpright = raw.boolean("keep_upright", false)
	spec.Stackable = raw.boolean("stackable", true)
	spec.MustBeOnFloor = raw.boolean("must_be_on_floor", false)
	if spec.MaxTopLoad, err = optionalWeight(raw, "max_top_load"); err != nil {
		return nil, err
	}
	if spec.MaxStackedItems, err = raw.optionalInteger("max_stacked_items"); err != nil {
		return nil, err
	}
	if spec.MinimumSupportRatio, err = raw.number("minimum_support_ratio", 0); err != nil {
		return nil, err
	}
	spec.Group = optionalText(raw, "group")
	if spec.Tags, err = raw.strings("tags"); err != nil {
		return nil, err
	}
	if spec.IncompatibleTags, err = raw.strings("incompatible_tags"); err != nil {
		return nil, err
	}
	if spec.Priority, err = raw.integer("priority", 0); err != nil {
		return nil, err
	}
	if spec.EligibleContainerTags, err = raw.strings("eligible_container_tags"); err != nil {
		return nil, err
	}
	spec.GroundContactRule = optionalText(raw, "ground_contact_rule")
	spec.Metadata, _ = raw.get("metadata")
	if value, ok := raw.get("nesting_height"); ok {
		height, err := parseLength(value, unit)
		if err != nil {
			return nil, err
		}
		spec.NestingHeight = &height
	}
	if spec.StopIndex, err = stopIndex(raw); err != nil {
		return nil, err
	}
	if spec.Value, err = raw.optionalInteger("value"); err != nil {
		return nil, err
	}
	if spec.ShapeType, err = domain.ParseShapeType(raw.text("shape_type", string(domain.RigidCuboid))); err != nil {
		return nil, err
	}
	if spec.HullVertices, err = hullVertices(raw, unit); err != nil {
		return nil, err
	}
	if value, ok := raw.get("compression_ratio"); ok {
		ratio, err := toFloat(value, "compression_ratio")
		if err != nil {
			return nil, err
		}
		ppm, err := compression.RatioToPPM(ratio)
		if err != nil {
			return nil, err
		}
		spec.CompressionRatioPPM = &ppm
	}
	if spec.MaxCompressionPressureKPA, err = raw.optionalInteger("max_compression_pressure_kpa"); err != nil {
		return nil, err
	}
	return domain.NewItem(spec)
}

// stopIndex refuses values past the exact range with the domain's own message, including
// ones too large for int64 to carry at all.
func stopIndex(raw object) (*int64, error) {
	value, ok := raw.get("stop_index")
	if !ok {
		return nil, nil
	}
	if big, isBig := value.(*big.Int); isBig && big.Sign() > 0 {
		return nil, errors.New("stop_index must be a non-negative safe integer")
	}
	parsed, err := toInt(value, "stop_index")
	return &parsed, err
}

// hullVertices reads wire vertices as non-negative offsets from the item's own box corner:
// they go through Length, which refuses a negative value.
func hullVertices(raw object, unit string) ([]hull.Vertex, error) {
	value, ok := raw.get("hull_vertices")
	if !ok {
		return nil, nil
	}
	list, err := asList(value, "hull_vertices")
	if err != nil {
		return nil, err
	}
	vertices := make([]hull.Vertex, 0, len(list))
	for _, element := range list {
		point, err := asObject(element, "hull_vertices")
		if err != nil {
			return nil, err
		}
		var vertex hull.Vertex
		for axis, key := range []string{"x", "y", "z"} {
			coordinate, ok := point[key]
			if !ok {
				return nil, fmt.Errorf("'%s'", key)
			}
			length, err := parseLength(coordinate, unit)
			if err != nil {
				return nil, err
			}
			vertex[axis] = length.Ticks()
		}
		vertices = append(vertices, vertex)
	}
	return vertices, nil
}

func decodeBox(raw object, unit string) (domain.Box, error) {
	originValue, _ := raw.get("origin")
	origin, err := asObject(originValue, "origin")
	if err != nil {
		return domain.Box{}, err
	}
	var coordinates [3]int64
	for axis, key := range []string{"x", "y", "z"} {
		value := interface{}(int64(0))
		if given, ok := origin.get(key); ok {
			value = given
		}
		length, err := parseLength(value, unit)
		if err != nil {
			return domain.Box{}, err
		}
		coordinates[axis] = length.Ticks()
	}
	point, err := domain.NewPoint(coordinates[0], coordinates[1], coordinates[2])
	if err != nil {
		return domain.Box{}, err
	}
	dimensionsValue, ok := raw.get("dimensions")
	if !ok {
		return domain.Box{}, errors.New("'dimensions'")
	}
	dimensions, err := decodeDimensions(dimensionsValue, unit)
	if err != nil {
		return domain.Box{}, err
	}
	return domain.NewBox(point, dimensions), nil
}

func decodeObstacles(raw object, unit string) ([]domain.Obstacle, error) {
	value, _ := raw.get("obstacles")
	list, err := asList(value, "obstacles")
	if err != nil {
		return nil, err
	}
	obstacles := make([]domain.Obstacle, 0, len(list))
	for _, element := range list {
		entry, err := asObject(element, "obstacle")
		if err != nil {
			return nil, err
		}
		id, ok := entry.get("id")
		if !ok {
			return nil, errors.New("'id'")
		}
		box, err := decodeBox(entry, unit)
		if err != nil {
			return nil, err
		}
		extraValue, _ := entry.get("additional_boxes")
		extras, err := asList(extraValue, "additional_boxes")
		if err != nil {
			return nil, err
		}
		additional := make([]domain.Box, 0, len(extras))
		for _, extra := range extras {
			extraObject, err := asObject(extra, "additional_box")
			if err != nil {
				return nil, err
			}
			extraBox, err := decodeBox(extraObject, unit)
			if err != nil {
				return nil, err
			}
			additional = append(additional, extraBox)
		}
		obstacles = append(obstacles, domain.NewObstacle(toText(id), box, additional))
	}
	return obstacles, nil
}

func decodeRateTable(raw object) (*domain.RateTable, error) {
	value, ok := raw.get("rate_table")
	if !ok {
		return nil, nil
	}
	table, err := asObject(value, "rate_table")
	if err != nil {
		return nil, err
	}
	lists := [2][]int64{}
	for index, key := range []string{"weight_brackets_g", "prices_minor"} {
		listValue, ok := table.get(key)
		if !ok {
			return nil, fmt.Errorf("'%s'", key)
		}
		elements, err := asList(listValue, key)
		if err != nil {
			return nil, err
		}
		for _, element := range elements {
			parsed, err := toInt(element, key)
			if err != nil {
				return nil, err
			}
			lists[index] = append(lists[index], parsed)
		}
	}
	minimum, err := table.integer("minimum_charge_minor", 0)
	if err != nil {
		return nil, err
	}
	surcharge, err := table.integer("fuel_surcharge_permille", 0)
	if err != nil {
		return nil, err
	}
	return domain.NewRateTable(lists[0], lists[1], minimum, surcharge)
}

func decodeAxles(raw object, unit string) (*[2]domain.Axle, error) {
	value, ok := raw.get("axles")
	if !ok {
		return nil, nil
	}
	list, err := asList(value, "axles")
	if err != nil {
		return nil, err
	}
	if len(list) != 2 {
		return nil, fmt.Errorf("too many values to unpack (expected 2, got %d)", len(list))
	}
	var axles [2]domain.Axle
	for index, element := range list {
		entry, err := asObject(element, "axle")
		if err != nil {
			return nil, err
		}
		positionValue, ok := entry.get("position")
		if !ok {
			return nil, errors.New("'position'")
		}
		position, err := parseLength(positionValue, unit)
		if err != nil {
			return nil, err
		}
		maxLoad, err := optionalWeight(entry, "max_load")
		if err != nil {
			return nil, err
		}
		axles[index] = domain.NewAxle(position, maxLoad)
	}
	return &axles, nil
}

func decodeContainer(raw object, unit string) (*domain.Container, error) {
	obstacles, err := decodeObstacles(raw, unit)
	if err != nil {
		return nil, err
	}
	id, ok := raw.get("id")
	if !ok {
		return nil, errors.New("'id'")
	}
	innerValue, ok := raw.get("inner_dimensions")
	if !ok {
		return nil, errors.New("'inner_dimensions'")
	}
	inner, err := decodeDimensions(innerValue, unit)
	if err != nil {
		return nil, err
	}
	spec := domain.ContainerSpec{ID: toText(id), InnerDimensions: inner, Obstacles: obstacles}
	if value, ok := raw.get("outer_dimensions"); ok {
		outer, err := decodeDimensions(value, unit)
		if err != nil {
			return nil, err
		}
		spec.OuterDimensions = &outer
	}
	if value, ok := raw.get("tare_weight"); ok {
		if spec.TareWeight, err = parseWeight(value, weightUnit); err != nil {
			return nil, err
		}
	}
	if spec.MaxPayload, err = optionalWeight(raw, "max_payload"); err != nil {
		return nil, err
	}
	if spec.CostMinor, err = raw.integer("cost_minor", 0); err != nil {
		return nil, err
	}
	if spec.RateTable, err = decodeRateTable(raw); err != nil {
		return nil, err
	}
	if spec.Quantity, err = raw.optionalInteger("quantity"); err != nil {
		return nil, err
	}
	if spec.Tags, err = raw.strings("tags"); err != nil {
		return nil, err
	}
	if spec.MaxItems, err = raw.optionalInteger("max_items"); err != nil {
		return nil, err
	}
	if spec.VoidFillReserveRatio, err = raw.number("void_fill_reserve_ratio", 0); err != nil {
		return nil, err
	}
	if spec.TagLimits, err = tagLimits(raw); err != nil {
		return nil, err
	}
	spec.Metadata, _ = raw.get("metadata")
	if spec.MaxStackDensity, err = optionalWeight(raw, "max_stack_density"); err != nil {
		return nil, err
	}
	if spec.Axles, err = decodeAxles(raw, unit); err != nil {
		return nil, err
	}
	if spec.AccessDirections, err = raw.strings("access_directions"); err != nil {
		return nil, err
	}
	return domain.NewContainer(spec)
}

func tagLimits(raw object) (map[string]int64, error) {
	value, _ := raw.get("tag_limits")
	limits, err := asObject(value, "tag_limits")
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(limits))
	for key := range limits {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	typed := make(map[string]int64, len(limits))
	for _, key := range keys {
		if typed[key], err = toInt(limits[key], key); err != nil {
			return nil, err
		}
	}
	return typed, nil
}

var catalogReferenceKeys = []string{"catalog_id", "effective_at", "resolved_at", "version"}

// catalogVersionsUsed checks the catalog references a caller echoes into the result: each
// with exactly the four keys, a non-empty unique id and integer versions and timestamps.
func catalogVersionsUsed(fields object) ([]interface{}, error) {
	value, present := fields["catalog_versions_used"]
	if !present {
		return []interface{}{}, nil
	}
	list, ok := value.([]interface{})
	if !ok {
		return nil, errors.New("catalog_versions_used must be an array")
	}
	seen := map[string]bool{}
	references := make([]interface{}, 0, len(list))
	for index, element := range list {
		reference, ok := element.(map[string]interface{})
		if !ok || !hasExactlyKeys(reference, catalogReferenceKeys) {
			return nil, fmt.Errorf("catalog_versions_used[%d] must contain exactly ['catalog_id', 'effective_at', 'resolved_at', 'version']", index)
		}
		id, ok := reference["catalog_id"].(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("catalog_versions_used[%d].catalog_id must be non-empty", index)
		}
		if seen[id] {
			return nil, fmt.Errorf("catalog_versions_used contains ambiguous duplicate '%s'", id)
		}
		seen[id] = true
		for _, field := range []struct {
			name    string
			minimum int64
		}{{"version", 1}, {"effective_at", 0}, {"resolved_at", 0}} {
			if !integerAtLeast(reference[field.name], field.minimum) {
				return nil, fmt.Errorf("catalog_versions_used[%d].%s must be >= %d", index, field.name, field.minimum)
			}
		}
		copied := make(map[string]interface{}, len(reference))
		for key, element := range reference {
			copied[key] = element
		}
		references = append(references, copied)
	}
	return references, nil
}

func hasExactlyKeys(object map[string]interface{}, keys []string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func integerAtLeast(value interface{}, minimum int64) bool {
	switch typed := value.(type) {
	case int64:
		return typed >= minimum
	case *big.Int:
		return typed.Sign() > 0
	}
	return false
}
