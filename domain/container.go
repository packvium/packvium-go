package domain

import (
	"errors"
	"fmt"
	"sort"

	"github.com/toxakara/packvium-go/internal/ints"
	"github.com/toxakara/packvium-go/units"
)

// Obstacle is an excluded zone: a union of exact boxes, so a non-rectangular shape such as
// a wheel arch stays integral.
type Obstacle struct {
	id    string
	boxes []Box
}

// NewObstacle is the box plus any additional boxes making up the zone.
func NewObstacle(id string, box Box, additional []Box) Obstacle {
	return Obstacle{id: id, boxes: append([]Box{box}, additional...)}
}

func (o Obstacle) ID() string { return o.id }

// Boxes returns the primary box followed by the additional ones.
func (o Obstacle) Boxes() []Box { return append([]Box(nil), o.boxes...) }

// Axle is one axle's position along the container's length and its own limit.
type Axle struct {
	position units.Length
	maxLoad  *units.Weight
}

// NewAxle builds an axle; maxLoad may be nil for no limit.
func NewAxle(position units.Length, maxLoad *units.Weight) Axle {
	return Axle{position: position, maxLoad: copyWeight(maxLoad)}
}

func (a Axle) Position() units.Length { return a.position }
func (a Axle) MaxLoad() *units.Weight { return copyWeight(a.maxLoad) }

// RateTable is a carrier rate card carried by the request. The price charged is the one at
// the first bracket at or above the billed weight; everything is an exact integer.
type RateTable struct {
	weightBracketsG       []int64
	pricesMinor           []int64
	minimumChargeMinor    int64
	fuelSurchargePermille int64
}

// NewRateTable validates a rate card in the reference order.
func NewRateTable(weightBracketsG, pricesMinor []int64, minimumChargeMinor, fuelSurchargePermille int64) (*RateTable, error) {
	switch {
	case len(weightBracketsG) == 0:
		return nil, errors.New("rate_table requires at least one weight bracket")
	case len(weightBracketsG) != len(pricesMinor):
		return nil, errors.New("rate_table weight_brackets_g and prices_minor must be the same length")
	case anyAtMost(weightBracketsG, 0):
		return nil, errors.New("rate_table weight brackets must be positive")
	case !strictlyAscending(weightBracketsG):
		return nil, errors.New("rate_table weight brackets must be strictly ascending")
	case anyAtMost(pricesMinor, -1):
		return nil, errors.New("rate_table prices cannot be negative")
	case minimumChargeMinor < 0:
		return nil, errors.New("rate_table minimum_charge_minor cannot be negative")
	case fuelSurchargePermille < 0:
		return nil, errors.New("rate_table fuel_surcharge_permille cannot be negative")
	}
	return &RateTable{append([]int64(nil), weightBracketsG...), append([]int64(nil), pricesMinor...), minimumChargeMinor, fuelSurchargePermille}, nil
}

func strictlyAscending(values []int64) bool {
	for index := 1; index < len(values); index++ {
		if values[index] <= values[index-1] {
			return false
		}
	}
	return true
}

func (r *RateTable) WeightBracketsG() []int64     { return append([]int64(nil), r.weightBracketsG...) }
func (r *RateTable) PricesMinor() []int64         { return append([]int64(nil), r.pricesMinor...) }
func (r *RateTable) MinimumChargeMinor() int64    { return r.minimumChargeMinor }
func (r *RateTable) FuelSurchargePermille() int64 { return r.fuelSurchargePermille }

// ChargeMinorOrNone is the landed cost at this billed weight, or false when the table does
// not price it. O(b) in the bracket count.
func (r *RateTable) ChargeMinorOrNone(billedWeightG int64) (int64, bool) {
	for index, bound := range r.weightBracketsG {
		if billedWeightG <= bound {
			base := ints.Max(r.pricesMinor[index], r.minimumChargeMinor)
			// Ceiling division, never a float: rounding down would let revenue vanish.
			surcharge := ceilDiv(base*r.fuelSurchargePermille, 1000)
			return base + surcharge, true
		}
	}
	return 0, false
}

// UnratedWeightError is a billed weight the rate table does not price.
type UnratedWeightError struct{ message string }

func (e *UnratedWeightError) Error() string { return e.message }

// ChargeMinor is ChargeMinorOrNone for callers that want the refusal.
func (r *RateTable) ChargeMinor(billedWeightG int64) (int64, error) {
	charge, ok := r.ChargeMinorOrNone(billedWeightG)
	if !ok {
		return 0, &UnratedWeightError{fmt.Sprintf(
			"billed weight %d g is above the rate table's last bracket (%d g); the shipment has no published price",
			billedWeightG, r.weightBracketsG[len(r.weightBracketsG)-1])}
	}
	return charge, nil
}

func ceilDiv(numerator, denominator int64) int64 {
	quotient := numerator / denominator
	if numerator%denominator != 0 && (numerator < 0) == (denominator < 0) {
		quotient++
	}
	return quotient
}

// ContainerSpec is the caller's description of a container type.
type ContainerSpec struct {
	ID                   string
	InnerDimensions      Dimensions
	OuterDimensions      *Dimensions
	TareWeight           units.Weight
	MaxPayload           *units.Weight
	CostMinor            int64
	RateTable            *RateTable
	Quantity             *int64
	Obstacles            []Obstacle
	Tags                 []string
	MaxItems             *int64
	VoidFillReserveRatio float64
	TagLimits            map[string]int64
	Metadata             interface{}
	MaxStackDensity      *units.Weight
	Axles                *[2]Axle
}

// Container is one validated, immutable container type.
type Container struct {
	spec          ContainerSpec
	tags          TagSet
	tagLimitNames []string
}

// NewContainer validates a spec in the reference order.
func NewContainer(spec ContainerSpec) (*Container, error) {
	if err := validateContainer(spec); err != nil {
		return nil, err
	}
	frozen := spec
	frozen.OuterDimensions, frozen.MaxPayload, frozen.Quantity = copyDimensions(spec.OuterDimensions), copyWeight(spec.MaxPayload), copyInt64(spec.Quantity)
	frozen.MaxItems, frozen.MaxStackDensity, frozen.Axles = copyInt64(spec.MaxItems), copyWeight(spec.MaxStackDensity), copyAxles(spec.Axles)
	frozen.Obstacles, frozen.Tags = append([]Obstacle(nil), spec.Obstacles...), nil
	frozen.TagLimits = make(map[string]int64, len(spec.TagLimits))
	names := make([]string, 0, len(spec.TagLimits))
	for name, limit := range spec.TagLimits {
		frozen.TagLimits[name] = limit
		names = append(names, name)
	}
	sort.Strings(names)
	return &Container{spec: frozen, tags: NewTagSet(spec.Tags), tagLimitNames: names}, nil
}

func validateContainer(spec ContainerSpec) error {
	switch {
	case spec.ID == "":
		return errors.New("container id is required")
	case spec.Quantity != nil && *spec.Quantity <= 0:
		return errors.New("container quantity must be positive")
	case spec.CostMinor < 0:
		return errors.New("container cost cannot be negative")
	case !(spec.VoidFillReserveRatio >= 0 && spec.VoidFillReserveRatio <= 1):
		return errors.New("void_fill_reserve_ratio must be between 0 and 1")
	}
	if err := validateAxles(spec); err != nil {
		return err
	}
	for _, limit := range spec.TagLimits {
		if limit < 1 {
			return errors.New("tag_limits must be at least 1")
		}
	}
	if spec.OuterDimensions != nil && !spec.InnerDimensions.FitsInside(*spec.OuterDimensions) {
		return errors.New("outer dimensions cannot be smaller than inner dimensions")
	}
	boundary := NewBox(Point{}, spec.InnerDimensions)
	for _, obstacle := range spec.Obstacles {
		for _, box := range obstacle.boxes {
			if !boundary.Contains(box) {
				return fmt.Errorf("obstacle %s lies outside container", obstacle.id)
			}
		}
	}
	return nil
}

func validateAxles(spec ContainerSpec) error {
	if spec.Axles == nil {
		return nil
	}
	front, rear := spec.Axles[0], spec.Axles[1]
	if front.position.Ticks() >= rear.position.Ticks() {
		return errors.New("the front axle must be strictly nearer the origin than the rear axle")
	}
	if rear.position.Ticks() > spec.InnerDimensions.Length().Ticks() {
		return errors.New("axle positions must lie within the container's length")
	}
	return nil
}

func (c *Container) ID() string                     { return c.spec.ID }
func (c *Container) InnerDimensions() Dimensions    { return c.spec.InnerDimensions }
func (c *Container) OuterDimensions() *Dimensions   { return copyDimensions(c.spec.OuterDimensions) }
func (c *Container) TareWeight() units.Weight       { return c.spec.TareWeight }
func (c *Container) MaxPayload() *units.Weight      { return copyWeight(c.spec.MaxPayload) }
func (c *Container) CostMinor() int64               { return c.spec.CostMinor }
func (c *Container) RateTable() *RateTable          { return c.spec.RateTable }
func (c *Container) Quantity() *int64               { return copyInt64(c.spec.Quantity) }
func (c *Container) Obstacles() []Obstacle          { return append([]Obstacle(nil), c.spec.Obstacles...) }
func (c *Container) Tags() TagSet                   { return c.tags }
func (c *Container) MaxItems() *int64               { return copyInt64(c.spec.MaxItems) }
func (c *Container) VoidFillReserveRatio() float64  { return c.spec.VoidFillReserveRatio }
func (c *Container) Metadata() interface{}          { return c.spec.Metadata }
func (c *Container) MaxStackDensity() *units.Weight { return copyWeight(c.spec.MaxStackDensity) }
func (c *Container) Axles() *[2]Axle                { return copyAxles(c.spec.Axles) }

// TagLimitNames are the limited tags in ascending order.
func (c *Container) TagLimitNames() []string { return append([]string(nil), c.tagLimitNames...) }

// TagLimit is the limit for a tag, if one is set.
func (c *Container) TagLimit(tag string) (int64, bool) {
	limit, ok := c.spec.TagLimits[tag]
	return limit, ok
}

// EffectiveOuterDimensions is the outer box, or the inner one when none was declared.
func (c *Container) EffectiveOuterDimensions() Dimensions {
	if c.spec.OuterDimensions != nil {
		return *c.spec.OuterDimensions
	}
	return c.spec.InnerDimensions
}
