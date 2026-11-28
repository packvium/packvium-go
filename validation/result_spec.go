package validation

import (
	"math/big"
	"sort"
)

// The request, read independently of the engine's own model.

var (
	exitDirections   = []string{"+x", "-x", "+y", "-y", "+z", "-z"}
	rotationCodes    = []string{"LWH", "LHW", "WLH", "WHL", "HLW", "HWL"}
	uprightRotations = []string{"LWH", "WLH"}
	rotationAxes     = map[string][3]int{
		"LWH": {0, 1, 2}, "LHW": {0, 2, 1}, "WLH": {1, 0, 2}, "WHL": {1, 2, 0}, "HLW": {2, 0, 1}, "HWL": {2, 1, 0},
	}
)

// tagSet is a frozenset of the request's tag strings.
type tagSet map[string]bool

func (s tagSet) intersects(other tagSet) bool {
	for tag := range s {
		if other[tag] {
			return true
		}
	}
	return false
}

func (s tagSet) sorted() []string {
	tags := make([]string, 0, len(s))
	for tag := range s {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

// holds is `value in tags`; an unhashable value is the reference's TypeError.
func (s tagSet) holds(value interface{}) bool {
	hashKey(value)
	typed, ok := value.(string)
	return ok && s[typed]
}

type itemSpec struct {
	id                    string
	dimensions            [3]*big.Int
	weight                *big.Int
	quantity              interface{}
	allowedRotations      []interface{}
	stackable             bool
	mustBeOnFloor         bool
	maxTopLoad            *big.Int
	minimumSupportRatio   *big.Rat
	tags                  tagSet
	eligibleContainerTags tagSet
	maxStackedItems       *big.Int
	groundContactRule     interface{}
	nestingHeight         *big.Int
	incompatibleTags      tagSet
	stopIndex             *big.Int
	value                 interface{}
	shapeType             interface{}
	hullVertices          [][3]*big.Int
	compressionRatioPPM   *big.Int
	maxCompressionKPA     *big.Int
	stackingGroup         interface{}
	acceptsStackingGroups tagSet
}

func (s *itemSpec) allows(orientation interface{}) bool {
	for _, allowed := range s.allowedRotations {
		if equals(allowed, orientation) {
			return true
		}
	}
	return false
}

func (s *itemSpec) isConvexHull() bool { return isText(s.shapeType, "convex_hull") }

func (s *itemSpec) rotated(orientation string) []*big.Int {
	axes := axesOf(orientation)
	return []*big.Int{s.dimensions[axes[0]], s.dimensions[axes[1]], s.dimensions[axes[2]]}
}

func (s *itemSpec) nominalVolume() *big.Int {
	return mul(mul(s.dimensions[0], s.dimensions[1]), s.dimensions[2])
}

type machineSpec struct {
	minimum, increment, allowance [3]*big.Int
	pricePerSquareMetreMinor      *big.Int
	tarePerSquareMetre            *big.Int
}

type axleSpec struct {
	position *big.Int
	limit    *big.Int
}

type containerSpec struct {
	id                    interface{}
	inner                 [3]*big.Int
	outer                 *[3]*big.Int
	costMinor             *big.Int
	maxPayload            *big.Int
	maxItems              *big.Int
	quantity              *big.Int
	obstacles             []cuboid
	tags                  tagSet
	tagLimits             []tagLimit
	voidFillReserveRatio  *big.Rat
	tareWeight            *big.Int
	axles                 []axleSpec
	hasAxles              bool
	maxStackDensity       *big.Int
	rateTable             interface{}
	accessDirections      []string
	flatBag               bool
	reportsAchievedExtent bool
	madeToMeasure         *machineSpec
}

type tagLimit struct {
	tag   string
	limit *big.Int
}

func (c *containerSpec) volume() *big.Int { return mul(mul(c.inner[0], c.inner[1]), c.inner[2]) }

func axesOf(orientation string) [3]int {
	axes, known := rotationAxes[orientation]
	if !known {
		panic(shapeRefusal("unknown orientation " + pyRepr(orientation)))
	}
	return axes
}

// isRotation is `orientation in ROTATIONS` for any JSON value; an unhashable one is the
// reference's TypeError.
func isRotation(orientation interface{}) bool {
	hashKey(orientation)
	typed, ok := orientation.(string)
	if !ok {
		return false
	}
	_, known := rotationAxes[typed]
	return known
}

func parseItems(request interface{}, unit string) *orderedMap {
	specs := newOrderedMap()
	for _, raw := range items(member(request, "items"), "request.items") {
		var rotations []interface{}
		switch declared := get(raw, "allowed_rotations", nil); {
		case declared != nil:
			rotations = items(declared, "allowed_rotations")
		case truthy(get(raw, "keep_upright", nil)):
			rotations = stringValues(uprightRotations)
		default:
			rotations = stringValues(rotationCodes)
		}
		id := member(raw, "id")
		spec := &itemSpec{
			id:                    text(id, "an item id"),
			dimensions:            dimensionsOf(member(raw, "dimensions"), unit),
			weight:                toTicks(get(raw, "weight", bi(0)), "g", weightTicks),
			quantity:              get(raw, "quantity", bi(1)),
			allowedRotations:      rotations,
			stackable:             truthy(get(raw, "stackable", true)),
			mustBeOnFloor:         truthy(get(raw, "must_be_on_floor", false)),
			maxTopLoad:            measured(raw, "max_top_load", "g", weightTicks),
			minimumSupportRatio:   ratioOf(get(raw, "minimum_support_ratio", bi(0))),
			tags:                  tagsOf(get(raw, "tags", []interface{}{}), "tags"),
			eligibleContainerTags: tagsOf(get(raw, "eligible_container_tags", []interface{}{}), "eligible_container_tags"),
			maxStackedItems:       optionalInteger(get(raw, "max_stacked_items", nil), "max_stacked_items"),
			groundContactRule:     get(raw, "ground_contact_rule", nil),
			nestingHeight:         measured(raw, "nesting_height", unit, lengthTicks),
			incompatibleTags:      tagsOf(get(raw, "incompatible_tags", []interface{}{}), "incompatible_tags"),
			stopIndex:             optionalInteger(get(raw, "stop_index", nil), "stop_index"),
			value:                 get(raw, "value", nil),
			shapeType:             get(raw, "shape_type", "rigid_cuboid"),
			hullVertices:          hullOf(raw, unit),
			maxCompressionKPA: optionalInteger(get(raw, "max_compression_pressure_kpa", nil),
				"max_compression_pressure_kpa"),
			stackingGroup:         get(raw, "stacking_group", nil),
			acceptsStackingGroups: tagSet{},
		}
		if ratio := get(raw, "compression_ratio", nil); ratio != nil {
			// The solvers' ratio rule, applied once so nothing downstream sees a float.
			spec.compressionRatioPPM = bi(ratioPPM(ratioOf(ratio)))
		}
		if accepted := get(raw, "accepts_stacking_groups", nil); truthy(accepted) {
			spec.acceptsStackingGroups = tagsOf(accepted, "accepts_stacking_groups")
		}
		specs.set(id, spec)
	}
	return specs
}

func stringValues(texts []string) []interface{} {
	values := make([]interface{}, len(texts))
	for index, value := range texts {
		values[index] = value
	}
	return values
}

func parseContainers(request interface{}, unit string) *orderedMap {
	specs := newOrderedMap()
	for _, raw := range items(member(request, "containers"), "request.containers") {
		spec := &containerSpec{
			id:                   member(raw, "id"),
			inner:                dimensionsOf(member(raw, "inner_dimensions"), unit),
			accessDirections:     accessDirectionsOf(raw),
			costMinor:            integer(get(raw, "cost_minor", bi(0)), "cost_minor"),
			maxPayload:           measured(raw, "max_payload", "g", weightTicks),
			maxItems:             optionalInteger(get(raw, "max_items", nil), "max_items"),
			quantity:             optionalInteger(get(raw, "quantity", nil), "quantity"),
			obstacles:            obstaclesOf(raw, unit),
			tags:                 tagsOf(get(raw, "tags", []interface{}{}), "tags"),
			tagLimits:            tagLimitsOf(raw),
			voidFillReserveRatio: ratioOf(get(raw, "void_fill_reserve_ratio", bi(0))),
			tareWeight:           toTicks(get(raw, "tare_weight", bi(0)), "g", weightTicks),
			maxStackDensity:      measured(raw, "max_stack_density", "g", weightTicks),
			rateTable:            get(raw, "rate_table", nil),
			flatBag:              get(raw, "deformable_boundary", nil) != nil,
		}
		if outer := get(raw, "outer_dimensions", nil); outer != nil {
			sides := dimensionsOf(outer, unit)
			spec.outer = &sides
		}
		spec.axles, spec.hasAxles = axlesOf(raw, unit)
		machine := get(raw, "made_to_measure", nil)
		spec.reportsAchievedExtent = spec.flatBag || machine != nil
		if machine != nil {
			spec.madeToMeasure = machineOf(machine, unit)
		}
		specs.set(spec.id, spec)
	}
	return specs
}

// measured is None when the key is absent or null, else the measure in ticks.
func measured(raw interface{}, key, unit string, table unitTable) *big.Int {
	value := get(raw, key, nil)
	if value == nil {
		return nil
	}
	return toTicks(value, unit, table)
}

func tagsOf(value interface{}, what string) tagSet {
	tags := tagSet{}
	for _, tag := range items(value, what) {
		tags[text(tag, what)] = true
	}
	return tags
}

func hullOf(raw interface{}, unit string) [][3]*big.Int {
	declared := get(raw, "hull_vertices", nil)
	if declared == nil {
		return nil
	}
	vertices := [][3]*big.Int{}
	for _, vertex := range items(declared, "hull_vertices") {
		vertices = append(vertices, [3]*big.Int{
			toTicks(member(vertex, "x"), unit, lengthTicks), toTicks(member(vertex, "y"), unit, lengthTicks),
			toTicks(member(vertex, "z"), unit, lengthTicks),
		})
	}
	return vertices
}

func accessDirectionsOf(raw interface{}) []string {
	declared := items(get(raw, "access_directions", []interface{}{}), "access_directions")
	for _, direction := range declared {
		hashKey(direction)
	}
	var directions []string
	for _, direction := range exitDirections {
		for _, named := range declared {
			if isText(named, direction) {
				directions = append(directions, direction)
				break
			}
		}
	}
	return directions
}

func obstaclesOf(raw interface{}, unit string) []cuboid {
	var boxes []cuboid
	for _, obstacle := range items(get(raw, "obstacles", []interface{}{}), "obstacles") {
		components := append([]interface{}{obstacle},
			items(get(obstacle, "additional_boxes", []interface{}{}), "additional_boxes")...)
		for _, component := range components {
			origin := get(component, "origin", newJSONObject())
			sides := dimensionsOf(member(component, "dimensions"), unit)
			boxes = append(boxes, newCuboid(
				toTicks(get(origin, "x", bi(0)), unit, lengthTicks), toTicks(get(origin, "y", bi(0)), unit, lengthTicks),
				toTicks(get(origin, "z", bi(0)), unit, lengthTicks), sides[0], sides[1], sides[2]))
		}
	}
	return boxes
}

// tagLimitsOf is {str(tag): int(limit)}, in tag order as the reference walks them.
func tagLimitsOf(raw interface{}) []tagLimit {
	declared, ok := get(raw, "tag_limits", newJSONObject()).(*jsonObject)
	if !ok {
		panic(shapeRefusal("tag_limits must be an object"))
	}
	limits := make([]tagLimit, 0, len(declared.keys))
	for _, tag := range declared.keys {
		limits = append(limits, tagLimit{tag, pyInt(declared.values[tag], "tag_limits")})
	}
	sort.SliceStable(limits, func(left, right int) bool { return limits[left].tag < limits[right].tag })
	return limits
}

func axlesOf(raw interface{}, unit string) ([]axleSpec, bool) {
	declared := get(raw, "axles", nil)
	if declared == nil {
		return nil, false
	}
	var axles []axleSpec
	for _, axle := range items(declared, "axles") {
		axles = append(axles, axleSpec{toTicks(member(axle, "position"), unit, lengthTicks),
			measured(axle, "max_load", "g", weightTicks)})
	}
	return axles, true
}

func machineOf(raw interface{}, unit string) *machineSpec {
	sides := func(name string) [3]*big.Int {
		value := get(raw, name, nil)
		if value == nil {
			return [3]*big.Int{bi(0), bi(0), bi(0)}
		}
		return dimensionsOf(value, unit)
	}
	machine := &machineSpec{minimum: sides("minimum"), increment: sides("increment"),
		allowance: sides("outer_allowance"), pricePerSquareMetreMinor: bi(0), tarePerSquareMetre: bi(0)}
	if price := get(raw, "cost_per_square_metre_minor", nil); truthy(price) {
		machine.pricePerSquareMetreMinor = integer(price, "cost_per_square_metre_minor")
	}
	if tare := get(raw, "tare_per_square_metre", nil); tare != nil {
		machine.tarePerSquareMetre = toTicks(tare, "g", weightTicks)
	}
	return machine
}
