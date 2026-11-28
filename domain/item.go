package domain

import (
	"fmt"
	"sync"

	"github.com/packvium/packvium-go/compression"
	"github.com/packvium/packvium-go/hull"
	"github.com/packvium/packvium-go/internal/pyrepr"
	"github.com/packvium/packvium-go/internal/refusal"
	"github.com/packvium/packvium-go/units"
)

// MaxExactStopIndex is the largest stop_index every engine can carry identically: past
// 2^53 - 1 a JavaScript double collapses consecutive stops into one.
const MaxExactStopIndex int64 = 1<<53 - 1

// groundContactRules is sorted, matching how the reference lists them in its refusal.
var groundContactRules = []string{"covered", "free", "multiple", "single"}

// ItemSpec is the caller's description of an item type. NewItemSpec fills in the same
// defaults the reference applies; NewItem validates the whole spec and freezes it.
type ItemSpec struct {
	ID                        string
	Dimensions                Dimensions
	Weight                    units.Weight
	Quantity                  int64
	AllowedRotations          []Rotation
	KeepUpright               bool
	Stackable                 bool
	MustBeOnFloor             bool
	MaxTopLoad                *units.Weight
	MaxStackedItems           *int64
	MinimumSupportRatio       float64
	GroundContactRule         *string
	Group                     *string
	Tags                      []string
	IncompatibleTags          []string
	Priority                  int64
	EligibleContainerTags     []string
	Metadata                  interface{}
	NestingHeight             *units.Length
	StopIndex                 *int64
	ShapeType                 ShapeType
	HullVertices              []hull.Vertex
	CompressionRatioPPM       *int64
	MaxCompressionPressureKPA *int64
	Value                     *int64
	// StackingGroup is the stacking class this item belongs to, and AcceptsStackingGroups the
	// classes it may carry directly on its top face. An
	// empty list is unconstrained; a non-empty one is closed, so an item with no class rests
	// on no constrained item: a forgotten classification fails closed, never open.
	StackingGroup         *string
	AcceptsStackingGroups []string
}

// NewItemSpec is a spec with the reference defaults: one unit, stackable, any rotation,
// rigid cuboid.
func NewItemSpec(id string, dimensions Dimensions) ItemSpec {
	return ItemSpec{
		ID: id, Dimensions: dimensions, Quantity: 1, Stackable: true,
		AllowedRotations: AllRotations(), ShapeType: RigidCuboid,
	}
}

// Item is one validated, immutable item type.
type Item struct {
	spec   ItemSpec
	tags   TagSet
	banned TagSet
	homes  TagSet
	// carries is the AcceptsStackingGroups set.
	carries TagSet
	shapes  *hullShapes
}

// hullShapes memoises the rotated hull per rotation. Building one is O(v^4), and it
// depends only on the item and the rotation, never on where a candidate sits.
type hullShapes struct {
	once   [6]sync.Once
	shapes [6]*hull.Shape
	errs   [6]error
}

// NewItem validates a spec in the reference order, so a request breaking several rules is
// refused with the same message in every engine.
func NewItem(spec ItemSpec) (*Item, error) {
	if err := validateItem(spec); err != nil {
		return nil, err
	}
	frozen := spec
	frozen.AllowedRotations = nil
	for _, rotation := range spec.AllowedRotations {
		if !spec.KeepUpright || (rotation == LWH || rotation == WLH) {
			frozen.AllowedRotations = append(frozen.AllowedRotations, rotation)
		}
	}
	if len(frozen.AllowedRotations) == 0 {
		return nil, refusal.New("invalid_value", "at least one rotation must be allowed")
	}
	frozen.HullVertices = append([]hull.Vertex(nil), spec.HullVertices...)
	frozen.Tags, frozen.IncompatibleTags, frozen.EligibleContainerTags = nil, nil, nil
	frozen.StackingGroup, frozen.AcceptsStackingGroups = copyString(spec.StackingGroup), nil
	return &Item{
		spec: frozen, tags: NewTagSet(spec.Tags), banned: NewTagSet(spec.IncompatibleTags),
		homes: NewTagSet(spec.EligibleContainerTags), carries: NewTagSet(spec.AcceptsStackingGroups),
		shapes: &hullShapes{},
	}, nil
}

func validateItem(spec ItemSpec) error {
	switch {
	case spec.ID == "":
		return refusal.New("invalid_value", "item id is required")
	case spec.Quantity <= 0:
		return refusal.New("below_minimum", "item quantity must be positive")
	case !(spec.MinimumSupportRatio >= 0 && spec.MinimumSupportRatio <= 1):
		return refusal.Ratio(spec.MinimumSupportRatio, "minimum_support_ratio must be between 0 and 1")
	case spec.MaxStackedItems != nil && *spec.MaxStackedItems < 1:
		return refusal.New("below_minimum", "max_stacked_items must be at least 1")
	case spec.StopIndex != nil && *spec.StopIndex < 0:
		return refusal.New("below_minimum", "stop_index must be a non-negative safe integer")
	case spec.StopIndex != nil && *spec.StopIndex > MaxExactStopIndex:
		return refusal.New("above_maximum", "stop_index must be a non-negative safe integer")
	case spec.Value != nil && *spec.Value < 0:
		return refusal.New("below_minimum", "value must be non-negative")
	case spec.NestingHeight != nil && spec.NestingHeight.Ticks() >= spec.Dimensions.Height().Ticks():
		return refusal.New("invalid_value", "nesting_height must be at least zero and strictly less than the item's own height")
	case spec.GroundContactRule != nil && !containsString(groundContactRules, *spec.GroundContactRule):
		return refusal.New("not_allowed", "ground_contact_rule must be one of "+pyrepr.StringList(groundContactRules))
	}
	return validateShape(spec)
}

func validateShape(spec ItemSpec) error {
	if _, err := ParseShapeType(string(spec.ShapeType)); err != nil {
		return err
	}
	// The value's own floor first, as the JSON rules check it before the shape.
	if spec.MaxCompressionPressureKPA != nil && *spec.MaxCompressionPressureKPA < 0 {
		return refusal.New("below_minimum", "max_compression_pressure_kpa cannot be negative")
	}
	for _, field := range foreignToShape(spec) {
		if field.present {
			return refusal.New("invalid_value", fmt.Sprintf("%s is not part of a %s item", field.name, spec.ShapeType))
		}
	}
	if spec.NestingHeight != nil && spec.ShapeType != RigidCuboid {
		return refusal.New("invalid_value", fmt.Sprintf("nesting_height with shape_type %s is not supported yet", spec.ShapeType))
	}
	switch spec.ShapeType {
	case ConvexHull:
		return validateHull(spec)
	case Compressible:
		return validateCompression(spec)
	}
	return nil
}

type shapeField struct {
	name    string
	present bool
}

func foreignToShape(spec ItemSpec) []shapeField {
	vertices := shapeField{"hull_vertices", spec.HullVertices != nil}
	ratio := shapeField{"compression_ratio_ppm", spec.CompressionRatioPPM != nil}
	pressure := shapeField{"max_compression_pressure_kpa", spec.MaxCompressionPressureKPA != nil}
	switch spec.ShapeType {
	case ConvexHull:
		return []shapeField{ratio, pressure}
	case Compressible:
		return []shapeField{vertices}
	}
	return []shapeField{vertices, ratio, pressure}
}

func validateHull(spec ItemSpec) error {
	if spec.HullVertices == nil {
		return refusal.New("invalid_value", "a convex_hull item requires hull_vertices")
	}
	vertices, err := hull.Validate(spec.HullVertices)
	if err != nil {
		return err
	}
	lower, upper := hull.BoundingExtent(vertices)
	extent := [3]int64{upper[0] - lower[0], upper[1] - lower[1], upper[2] - lower[2]}
	declared := [3]int64{spec.Dimensions.Length().Ticks(), spec.Dimensions.Width().Ticks(), spec.Dimensions.Height().Ticks()}
	for axis := range extent {
		if extent[axis] > declared[axis] {
			return refusal.New("invalid_value", fmt.Sprintf("hull_vertices span (%d, %d, %d) does not fit inside dimensions (%d, %d, %d)",
				extent[0], extent[1], extent[2], declared[0], declared[1], declared[2]))
		}
	}
	return nil
}

func validateCompression(spec ItemSpec) error {
	switch {
	case spec.CompressionRatioPPM == nil || spec.MaxCompressionPressureKPA == nil:
		return refusal.New("invalid_value", "a compressible item requires both compression_ratio and max_compression_pressure_kpa")
	case *spec.CompressionRatioPPM < 0 || *spec.CompressionRatioPPM > compression.PPM:
		return refusal.New("invalid_value", "compression_ratio must be between zero and one")
	}
	return nil
}

func (i *Item) ID() string             { return i.spec.ID }
func (i *Item) Dimensions() Dimensions { return i.spec.Dimensions }
func (i *Item) Weight() units.Weight   { return i.spec.Weight }
func (i *Item) Quantity() int64        { return i.spec.Quantity }
func (i *Item) AllowedRotations() []Rotation {
	return append([]Rotation(nil), i.spec.AllowedRotations...)
}
func (i *Item) KeepUpright() bool             { return i.spec.KeepUpright }
func (i *Item) Stackable() bool               { return i.spec.Stackable }
func (i *Item) MustBeOnFloor() bool           { return i.spec.MustBeOnFloor }
func (i *Item) MaxTopLoad() *units.Weight     { return copyWeight(i.spec.MaxTopLoad) }
func (i *Item) MaxStackedItems() *int64       { return copyInt64(i.spec.MaxStackedItems) }
func (i *Item) MinimumSupportRatio() float64  { return i.spec.MinimumSupportRatio }
func (i *Item) GroundContactRule() *string    { return copyString(i.spec.GroundContactRule) }
func (i *Item) Group() *string                { return copyString(i.spec.Group) }
func (i *Item) Tags() TagSet                  { return i.tags }
func (i *Item) IncompatibleTags() TagSet      { return i.banned }
func (i *Item) Priority() int64               { return i.spec.Priority }
func (i *Item) EligibleContainerTags() TagSet { return i.homes }
func (i *Item) Metadata() interface{}         { return i.spec.Metadata }
func (i *Item) NestingHeight() *units.Length  { return copyLength(i.spec.NestingHeight) }
func (i *Item) StopIndex() *int64             { return copyInt64(i.spec.StopIndex) }
func (i *Item) ShapeType() ShapeType          { return i.spec.ShapeType }
func (i *Item) HullVertices() []hull.Vertex {
	return append([]hull.Vertex(nil), i.spec.HullVertices...)
}
func (i *Item) CompressionRatioPPM() *int64       { return copyInt64(i.spec.CompressionRatioPPM) }
func (i *Item) MaxCompressionPressureKPA() *int64 { return copyInt64(i.spec.MaxCompressionPressureKPA) }
func (i *Item) Value() *int64                     { return copyInt64(i.spec.Value) }
func (i *Item) StackingGroup() *string            { return copyString(i.spec.StackingGroup) }

// AcceptsStackingGroups are the stacking classes this item may carry directly; empty is
// unconstrained.
func (i *Item) AcceptsStackingGroups() TagSet { return i.carries }

// CarriesItself reports whether a column of this one item type passes every stacking edge.
func (i *Item) CarriesItself() bool {
	return i.spec.Stackable && (i.carries.Len() == 0 ||
		(i.spec.StackingGroup != nil && i.carries.Has(*i.spec.StackingGroup)))
}

// StackingRefusal is why this item refuses one of upperGroup (nil for none) resting directly
// on it, or "" when it does not. O(1). A non-empty accepted list is closed, so an item with
// no group is in none of them.
func (i *Item) StackingRefusal(upperGroup *string) string {
	if !i.spec.Stackable {
		return "non_stackable"
	}
	if i.carries.Len() > 0 && (upperGroup == nil || !i.carries.Has(*upperGroup)) {
		return "stacking_group_incompatible"
	}
	return ""
}

// HullShape is the item's hull in one rotation, built at most once per rotation.
func (i *Item) HullShape(rotation Rotation) (*hull.Shape, error) {
	index := rotationIndex(rotation)
	i.shapes.once[index].Do(func() {
		i.shapes.shapes[index], i.shapes.errs[index] = hull.ShapeFor(i.spec.HullVertices, string(rotation))
	})
	return i.shapes.shapes[index], i.shapes.errs[index]
}

// IsStackSensitive reports whether what rests on this item can change a verdict: it
// refuses load, limits it, or yields to it.
func (i *Item) IsStackSensitive() bool {
	// A stacking group alone does not count: such an item is refused only by a constrained
	// item beneath it, and that item already switched the graph on.
	return !i.spec.Stackable || i.carries.Len() > 0 || i.spec.MaxTopLoad != nil || i.spec.MaxStackedItems != nil ||
		i.spec.MaxCompressionPressureKPA != nil
}

// HullCollisionIsExact reports whether this item's collisions may be decided by its hull
// rather than its box: it must be a hull, unpadded by clearance, and off any route.
func (i *Item) HullCollisionIsExact(envelopeMatchesPhysical bool) bool {
	return i.spec.ShapeType == ConvexHull && envelopeMatchesPhysical && i.spec.StopIndex == nil
}

// Instances are the item's units, numbered from one.
func (i *Item) Instances() []ItemInstance {
	instances := make([]ItemInstance, i.spec.Quantity)
	for index := range instances {
		instances[index] = ItemInstance{item: i, sequence: int64(index) + 1}
	}
	return instances
}

// ItemInstance is one numbered unit of an item type.
type ItemInstance struct {
	item     *Item
	sequence int64
}

func (i ItemInstance) Item() *Item     { return i.item }
func (i ItemInstance) Sequence() int64 { return i.sequence }

// ID is the per-unit label, e.g. "cube#3".
func (i ItemInstance) ID() string { return fmt.Sprintf("%s#%d", i.item.ID(), i.sequence) }
