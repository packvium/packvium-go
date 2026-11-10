package domain

import (
	"github.com/packvium/packvium-go/hull"
	"github.com/packvium/packvium-go/units"
)

// Placement is one item instance at a position and rotation, with the clearance envelope it
// reserves.
type Placement struct {
	instance           ItemInstance
	position           Point
	rotation           Rotation
	dimensions         Dimensions
	envelopeOrigin     Point
	envelopeDimensions Dimensions
	supportRatio       float64
	topLoad            units.Weight
}

// NewPlacement builds a placement with no load resting on it.
func NewPlacement(instance ItemInstance, position Point, rotation Rotation, dimensions Dimensions,
	envelopeOrigin Point, envelopeDimensions Dimensions, supportRatio float64) Placement {
	return Placement{instance, position, rotation, dimensions, envelopeOrigin, envelopeDimensions, supportRatio, units.Weight{}}
}

// WithTopLoad is the same placement carrying the given load.
func (p Placement) WithTopLoad(load units.Weight) Placement {
	p.topLoad = load
	return p
}

// WithSupportRatio is the same placement with a recomputed support ratio.
func (p Placement) WithSupportRatio(ratio float64) Placement {
	p.supportRatio = ratio
	return p
}

func (p Placement) Instance() ItemInstance         { return p.instance }
func (p Placement) Position() Point                { return p.position }
func (p Placement) Rotation() Rotation             { return p.rotation }
func (p Placement) Dimensions() Dimensions         { return p.dimensions }
func (p Placement) EnvelopeOrigin() Point          { return p.envelopeOrigin }
func (p Placement) EnvelopeDimensions() Dimensions { return p.envelopeDimensions }
func (p Placement) SupportRatio() float64          { return p.supportRatio }
func (p Placement) TopLoad() units.Weight          { return p.topLoad }
func (p Placement) Box() Box                       { return Box{p.position, p.dimensions} }
func (p Placement) EnvelopeBox() Box               { return Box{p.envelopeOrigin, p.envelopeDimensions} }

// HullShape is this placement's rotated hull, or nil when its box is the honest answer.
func (p Placement) HullShape() *hull.Shape {
	item := p.instance.item
	if !item.HullCollisionIsExact(p.envelopeDimensions == p.dimensions) {
		return nil
	}
	shape, err := item.HullShape(p.rotation)
	if err != nil {
		panic(err)
	}
	return shape
}

// PlacementsCollide reports whether two placed items actually overlap. The envelope test is
// the mandatory broad phase; a hull only refines it. One definition, so the solver, the
// sequence check and the final validation cannot disagree about what collides.
func PlacementsCollide(left, right Placement) bool {
	leftBox, rightBox := left.EnvelopeBox(), right.EnvelopeBox()
	if !leftBox.Intersects(rightBox) {
		return false
	}
	leftShape, rightShape := left.HullShape(), right.HullShape()
	if leftShape == nil && rightShape == nil {
		return true
	}
	if leftShape == nil {
		leftShape = boxShape(leftBox)
	}
	if rightShape == nil {
		rightShape = boxShape(rightBox)
	}
	return hull.Collide(leftShape, corner(leftBox), rightShape, corner(rightBox))
}

// PlacementHitsBox reports whether a placed item overlaps a plain box, such as an obstacle.
func PlacementHitsBox(placement Placement, box Box) bool {
	envelope := placement.EnvelopeBox()
	if !envelope.Intersects(box) {
		return false
	}
	shape := placement.HullShape()
	if shape == nil {
		return true
	}
	return hull.Collide(shape, corner(envelope), boxShape(box), corner(box))
}

func corner(box Box) hull.Vertex {
	return hull.Vertex{box.origin.x, box.origin.y, box.origin.z}
}

func boxShape(box Box) *hull.Shape {
	return hull.BoxShape(box.dimensions.length.Ticks(), box.dimensions.width.Ticks(), box.dimensions.height.Ticks())
}
