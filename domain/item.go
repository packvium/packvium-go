package domain

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/packvium/packvium-go/compression"
	"github.com/packvium/packvium-go/hull"
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
	shapes *hullShapes
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
		return nil, errors.New("at least one rotation must be allowed")
	}
	frozen.HullVertices = append([]hull.Vertex(nil), spec.HullVertices...)
	frozen.Tags, frozen.IncompatibleTags, frozen.EligibleContainerTags = nil, nil, nil
	return &Item{
		spec: frozen, tags: NewTagSet(spec.Tags), banned: NewTagSet(spec.IncompatibleTags),
		homes: NewTagSet(spec.EligibleContainerTags), shapes: &hullShapes{},
	}, nil
}

func validateItem(spec ItemSpec) error {
	switch {
	case spec.ID == "":
		return errors.New("item id is required")
	case spec.Quantity <= 0:
		return errors.New("item quantity must be positive")
	case !(spec.MinimumSupportRatio >= 0 && spec.MinimumSupportRatio <= 1):
		return errors.New("minimum_support_ratio must be between 0 and 1")
	case spec.MaxStackedItems != nil && *spec.MaxStackedItems < 1:
		return errors.New("max_stacked_items must be at least 1")
	case spec.StopIndex != nil && (*spec.StopIndex < 0 || *spec.StopIndex > MaxExactStopIndex):
		return errors.New("stop_index must be a non-negative safe integer")
	case spec.Value != nil && *spec.Value < 0:
		return errors.New("value must be non-negative")
	case spec.NestingHeight != nil && spec.NestingHeight.Ticks() >= spec.Dimensions.Height().Ticks():
		return errors.New("nesting_height must be at least zero and strictly less than the item's own height")
	case spec.GroundContactRule != nil && !containsString(groundContactRules, *spec.GroundContactRule):
		return fmt.Errorf("ground_contact_rule must be one of ['%s']", strings.Join(groundContactRules, "', '"))
	}
	return validateShape(spec)
}

func validateShape(spec ItemSpec) error {
	if _, err := ParseShapeType(string(spec.ShapeType)); err != nil {
		return err
	}
	for _, field := range foreignToShape(spec) {
		if field.present {
			return fmt.Errorf("%s is not part of a %s item", field.name, spec.ShapeType)
		}
	}
	if spec.NestingHeight != nil && spec.ShapeType != RigidCuboid {
		return fmt.Errorf("nesting_height with shape_type %s is not supported yet", spec.ShapeType)
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
		return errors.New("a convex_hull item requires hull_vertices")
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
			return fmt.Errorf("hull_vertices span (%d, %d, %d) does not fit inside dimensions (%d, %d, %d)",
				extent[0], extent[1], extent[2], declared[0], declared[1], declared[2])
		}
	}
	return nil
}

func validateCompression(spec ItemSpec) error {
	switch {
	case spec.CompressionRatioPPM == nil || spec.MaxCompressionPressureKPA == nil:
		return errors.New("a compressible item requires both compression_ratio and max_compression_pressure_kpa")
	case *spec.CompressionRatioPPM < 0 || *spec.CompressionRatioPPM > compression.PPM:
		return errors.New("compression_ratio must be between zero and one")
	case *spec.MaxCompressionPressureKPA < 0:
		return errors.New("max_compression_pressure_kpa cannot be negative")
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
	return !i.spec.Stackable || i.spec.MaxTopLoad != nil || i.spec.MaxStackedItems != nil ||
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
