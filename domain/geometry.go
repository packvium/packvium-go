// Package domain holds the immutable value objects a packing request and result are made
// of: items, containers, obstacles, rotations, placements and outcomes. Every type is
// immutable by construction -- unexported fields, validating constructors, no setters --
// and nothing here depends on a solver.
package domain

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/packvium/packvium-go/internal/ints"
	"github.com/packvium/packvium-go/units"
)

// Rotation names which item axis lies along each container axis.
type Rotation string

const (
	LWH Rotation = "LWH"
	LHW Rotation = "LHW"
	WLH Rotation = "WLH"
	WHL Rotation = "WHL"
	HLW Rotation = "HLW"
	HWL Rotation = "HWL"
)

// AllRotations is every rotation in declaration order.
func AllRotations() []Rotation { return []Rotation{LWH, LHW, WLH, WHL, HLW, HWL} }

// UprightRotations keeps the item's height vertical.
func UprightRotations() []Rotation { return []Rotation{LWH, WLH} }

// ParseRotation reads a rotation name.
func ParseRotation(name string) (Rotation, error) {
	for _, rotation := range AllRotations() {
		if string(rotation) == name {
			return rotation, nil
		}
	}
	return "", fmt.Errorf("'%s' is not a valid Rotation", name)
}

// ShapeType says how much of an item's declared box the item actually occupies.
type ShapeType string

const (
	RigidCuboid  ShapeType = "rigid_cuboid"
	ConvexHull   ShapeType = "convex_hull"
	Compressible ShapeType = "compressible"
)

// ParseShapeType reads a shape type name.
func ParseShapeType(name string) (ShapeType, error) {
	switch shape := ShapeType(name); shape {
	case RigidCuboid, ConvexHull, Compressible:
		return shape, nil
	}
	return "", fmt.Errorf("'%s' is not a valid ShapeType", name)
}

// Dimensions is a positive length, width and height.
type Dimensions struct {
	length, width, height units.Length
}

// NewDimensions refuses any non-positive side.
func NewDimensions(length, width, height units.Length) (Dimensions, error) {
	if ints.Min(length.Ticks(), width.Ticks(), height.Ticks()) <= 0 {
		return Dimensions{}, errors.New("all dimensions must be positive")
	}
	return Dimensions{length, width, height}, nil
}

// DimensionsOfTicks builds dimensions from raw tick counts.
func DimensionsOfTicks(length, width, height int64) (Dimensions, error) {
	sides := [3]units.Length{}
	for index, ticks := range [3]int64{length, width, height} {
		side, err := units.NewLength(ticks)
		if err != nil {
			return Dimensions{}, err
		}
		sides[index] = side
	}
	return NewDimensions(sides[0], sides[1], sides[2])
}

func (d Dimensions) Length() units.Length { return d.length }
func (d Dimensions) Width() units.Length  { return d.width }
func (d Dimensions) Height() units.Length { return d.height }

// Volume is the exact volume in cubic ticks; it can exceed 64 bits.
func (d Dimensions) Volume() *big.Int {
	volume := big.NewInt(d.length.Ticks())
	volume.Mul(volume, big.NewInt(d.width.Ticks()))
	return volume.Mul(volume, big.NewInt(d.height.Ticks()))
}

// BaseArea is the exact footprint in square ticks; an overflow panics rather than wraps.
func (d Dimensions) BaseArea() int64 {
	return ints.MulExact(d.length.Ticks(), d.width.Ticks())
}

// MaxEdge is the longest side in ticks.
func (d Dimensions) MaxEdge() int64 {
	return ints.Max(d.length.Ticks(), d.width.Ticks(), d.height.Ticks())
}

// Rotated returns the dimensions as laid out by rotation.
func (d Dimensions) Rotated(rotation Rotation) Dimensions {
	l, w, h := d.length, d.width, d.height
	switch rotation {
	case LHW:
		return Dimensions{l, h, w}
	case WLH:
		return Dimensions{w, l, h}
	case WHL:
		return Dimensions{w, h, l}
	case HLW:
		return Dimensions{h, l, w}
	case HWL:
		return Dimensions{h, w, l}
	}
	return d
}

// OrientedDimensions pairs a rotation with the dimensions it produces.
type OrientedDimensions struct {
	Rotation   Rotation
	Dimensions Dimensions
}

// UniqueRotations keeps the first rotation of each distinct oriented shape, in the order
// given.
func (d Dimensions) UniqueRotations(allowed []Rotation) []OrientedDimensions {
	seen := make(map[Dimensions]struct{}, len(allowed))
	result := make([]OrientedDimensions, 0, len(allowed))
	for _, rotation := range allowed {
		rotated := d.Rotated(rotation)
		if _, duplicate := seen[rotated]; duplicate {
			continue
		}
		seen[rotated] = struct{}{}
		result = append(result, OrientedDimensions{rotation, rotated})
	}
	return result
}

// FitsInside reports whether every side is at most the other's.
func (d Dimensions) FitsInside(other Dimensions) bool {
	return d.length.Ticks() <= other.length.Ticks() && d.width.Ticks() <= other.width.Ticks() &&
		d.height.Ticks() <= other.height.Ticks()
}

// Expand grows every side by the clearance on both sides.
func (d Dimensions) Expand(clearance units.Length) Dimensions {
	margin := ints.MulExact(clearance.Ticks(), 2)
	return MustDimensions(ints.AddExact(d.length.Ticks(), margin), ints.AddExact(d.width.Ticks(), margin),
		ints.AddExact(d.height.Ticks(), margin))
}

// MustDimensions builds dimensions from tick counts the caller has already proven positive;
// anything else is a programming error inside the engine.
func MustDimensions(length, width, height int64) Dimensions {
	dimensions, err := DimensionsOfTicks(length, width, height)
	if err != nil {
		panic(err)
	}
	return dimensions
}

// MustPoint builds a point from coordinates the caller has already proven non-negative.
func MustPoint(x, y, z int64) Point {
	point, err := NewPoint(x, y, z)
	if err != nil {
		panic(err)
	}
	return point
}

// ToMap renders the dimensions in a length unit.
func (d Dimensions) ToMap(unit string) map[string]interface{} {
	return map[string]interface{}{
		"length": d.length.ToMap(unit), "width": d.width.ToMap(unit), "height": d.height.ToMap(unit),
	}
}

// ToMap renders the point in a length unit.
func (p Point) ToMap(unit string) map[string]interface{} {
	return map[string]interface{}{
		"x": units.MustLength(p.x).ToMap(unit), "y": units.MustLength(p.y).ToMap(unit),
		"z": units.MustLength(p.z).ToMap(unit),
	}
}

// Point is a non-negative position in container ticks.
type Point struct{ x, y, z int64 }

// NewPoint refuses a negative coordinate.
func NewPoint(x, y, z int64) (Point, error) {
	if ints.Min(x, y, z) < 0 {
		return Point{}, errors.New("coordinates cannot be negative")
	}
	return Point{x, y, z}, nil
}

func (p Point) X() int64 { return p.x }
func (p Point) Y() int64 { return p.y }
func (p Point) Z() int64 { return p.z }

// Less orders points by x, then y, then z.
func (p Point) Less(other Point) bool {
	if p.x != other.x {
		return p.x < other.x
	}
	if p.y != other.y {
		return p.y < other.y
	}
	return p.z < other.z
}

// Box is an axis-aligned box: an origin and dimensions.
type Box struct {
	origin     Point
	dimensions Dimensions
}

// NewBox places dimensions at an origin.
func NewBox(origin Point, dimensions Dimensions) Box { return Box{origin, dimensions} }

func (b Box) Origin() Point          { return b.origin }
func (b Box) Dimensions() Dimensions { return b.dimensions }
func (b Box) X2() int64              { return b.origin.x + b.dimensions.length.Ticks() }
func (b Box) Y2() int64              { return b.origin.y + b.dimensions.width.Ticks() }
func (b Box) Z2() int64              { return b.origin.z + b.dimensions.height.Ticks() }

// Intersects uses half-open intervals: boxes that only touch do not intersect.
func (b Box) Intersects(other Box) bool {
	return !(b.X2() <= other.origin.x || other.X2() <= b.origin.x ||
		b.Y2() <= other.origin.y || other.Y2() <= b.origin.y ||
		b.Z2() <= other.origin.z || other.Z2() <= b.origin.z)
}

// Contains reports whether other lies entirely within b.
func (b Box) Contains(other Box) bool {
	return b.origin.x <= other.origin.x && b.origin.y <= other.origin.y && b.origin.z <= other.origin.z &&
		other.X2() <= b.X2() && other.Y2() <= b.Y2() && other.Z2() <= b.Z2()
}

// ContainsPoint is half-open on every axis.
func (b Box) ContainsPoint(point Point) bool {
	return b.origin.x <= point.x && point.x < b.X2() && b.origin.y <= point.y && point.y < b.Y2() &&
		b.origin.z <= point.z && point.z < b.Z2()
}

// OverlapAreaXY is the exact footprint overlap in square ticks.
func (b Box) OverlapAreaXY(other Box) int64 {
	dx := ints.Max(0, ints.Min(b.X2(), other.X2())-ints.Max(b.origin.x, other.origin.x))
	dy := ints.Max(0, ints.Min(b.Y2(), other.Y2())-ints.Max(b.origin.y, other.origin.y))
	return ints.MulExact(dx, dy)
}

// DimensionalWeight is a carrier's volumetric weight of a box: (L x W x H in lengthUnit) /
// divisor, truncated. The divisor is the carrier's published constant and already encodes
// the unit convention, so the caller names both units rather than the library guessing.
func DimensionalWeight(dimensions Dimensions, divisor int64, lengthUnit, weightUnit string) (units.Weight, error) {
	if divisor <= 0 {
		return units.Weight{}, errors.New("divisor must be positive")
	}
	lengthTicks, err := units.LengthUnitTicks(lengthUnit)
	if err != nil {
		return units.Weight{}, err
	}
	weightTicks, err := units.WeightUnitTicks(weightUnit)
	if err != nil {
		return units.Weight{}, err
	}
	numerator := new(big.Int).Mul(dimensions.Volume(), big.NewInt(weightTicks))
	perUnit := big.NewInt(lengthTicks)
	denominator := new(big.Int).Mul(new(big.Int).Mul(perUnit, perUnit), perUnit)
	denominator.Mul(denominator, big.NewInt(divisor))
	billed := numerator.Quo(numerator, denominator)
	if !billed.IsInt64() {
		return units.Weight{}, errors.New("weight exceeds the 64-bit tick range")
	}
	return units.NewWeight(billed.Int64())
}

// DimensionsOf reads three decimal or fractional texts ("12.5", "3/16", "12 3/8") in one
// unit, each rounded to the nearest tick: an inch is a whole number of ticks, so fractional
// inches stay exact.
func DimensionsOf(length, width, height, unit string) (Dimensions, error) {
	var sides [3]units.Length
	for index, text := range [3]string{length, width, height} {
		side, err := units.LengthOf(units.TextQuantity(text), unit, units.Nearest)
		if err != nil {
			return Dimensions{}, err
		}
		sides[index] = side
	}
	return NewDimensions(sides[0], sides[1], sides[2])
}
