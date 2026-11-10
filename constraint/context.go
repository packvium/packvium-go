// Package constraint holds the placement rules every solver runs and the whole-scene load
// analysis they share: contact and support graphs, bearing, crushing, stacking, density,
// route order, stop accessibility and axle loads. It depends only on domain and units, and
// nothing here imports a solver.
package constraint

import "github.com/packvium/packvium-go/domain"

// Context is one candidate placement and the state it is judged against.
//
// StackSensitive is false only when nothing in the container can refuse or be crushed by a
// load, which lets the bearing check skip a walk of the whole stack per candidate.
// RouteSensitive is false when no item in play declares a stop index. Placements is not
// copied: the caller must not modify the slice while a context refers to it.
type Context struct {
	container          *domain.Container
	placements         []domain.Placement
	item               domain.ItemInstance
	point              domain.Point
	rotation           domain.Rotation
	dimensions         domain.Dimensions
	envelopeDimensions domain.Dimensions
	stackSensitive     bool
	routeSensitive     bool
}

// NewContext is a candidate judged as stack and route sensitive, the conservative default.
func NewContext(container *domain.Container, placements []domain.Placement, item domain.ItemInstance,
	point domain.Point, rotation domain.Rotation, dimensions, envelopeDimensions domain.Dimensions) Context {
	return Context{container, placements, item, point, rotation, dimensions, envelopeDimensions, true, true}
}

// WithSensitivity is the same candidate with the opt-in rules switched as given.
func (c Context) WithSensitivity(stackSensitive, routeSensitive bool) Context {
	c.stackSensitive, c.routeSensitive = stackSensitive, routeSensitive
	return c
}

func (c Context) Container() *domain.Container          { return c.container }
func (c Context) Placements() []domain.Placement        { return c.placements }
func (c Context) Item() domain.ItemInstance             { return c.item }
func (c Context) Point() domain.Point                   { return c.point }
func (c Context) Rotation() domain.Rotation             { return c.rotation }
func (c Context) Dimensions() domain.Dimensions         { return c.dimensions }
func (c Context) EnvelopeDimensions() domain.Dimensions { return c.envelopeDimensions }
func (c Context) StackSensitive() bool                  { return c.stackSensitive }
func (c Context) RouteSensitive() bool                  { return c.routeSensitive }
func (c Context) EnvelopeBox() domain.Box               { return domain.NewBox(c.point, c.envelopeDimensions) }

// Result is a constraint's verdict on one candidate.
type Result struct {
	allowed      bool
	code, detail string
}

// Allow is the verdict of a candidate no rule refuses.
func Allow() Result { return Result{true, "allowed", ""} }

// Reject is a refusal with a stable code and a detail naming what broke the rule.
func Reject(code, detail string) Result { return Result{false, code, detail} }

func (r Result) Allowed() bool  { return r.allowed }
func (r Result) Code() string   { return r.code }
func (r Result) Detail() string { return r.detail }

// Violation is a broken rule as a code and a detail: the pair every load and route check
// returns. A nil *Violation means the rule holds.
type Violation struct {
	Code   string
	Detail string
}

func resultOf(failure *Violation) Result {
	if failure == nil {
		return Allow()
	}
	return Reject(failure.Code, failure.Detail)
}

// PlacementConstraint judges one candidate placement.
type PlacementConstraint interface {
	Evaluate(context Context) Result
}

// ProvenRejection is a constraint that can rule an item out of every offered container
// without searching for a placement first.
//
// A rejected candidate only means this position failed. A constraint implementing this can
// say that no position in any offered container could have worked, which lets an unpacked
// reason be reported as proven and name the rule responsible. Proving nothing (nil) is the
// right answer for most rules: a segregation rule or a per-container cap depends on what else
// was packed, a property of the search rather than of the request.
type ProvenRejection interface {
	ProvesUnplaceable(item domain.ItemInstance, containers []*domain.Container) *Violation
}
