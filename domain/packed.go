package domain

import (
	"fmt"
	"math/big"

	"github.com/packvium/packvium-go/internal/ints"
	"github.com/packvium/packvium-go/units"
)

// PackedContainer is one opened container and what went into it. When the compact lattice
// path built it, placements stay empty and the summary plus the consumed instances carry the
// same information.
type PackedContainer struct {
	container    *Container
	sequence     int64
	placements   []Placement
	lattice      *LatticeSummary
	latticeItems []ItemInstance
}

// NewPackedContainer holds explicit placements.
func NewPackedContainer(container *Container, sequence int64, placements []Placement) *PackedContainer {
	return &PackedContainer{container: container, sequence: sequence, placements: append([]Placement(nil), placements...)}
}

// NewLatticePackedContainer holds a compact lattice instead of placements.
func NewLatticePackedContainer(container *Container, sequence int64, summary *LatticeSummary, items []ItemInstance) *PackedContainer {
	return &PackedContainer{container: container, sequence: sequence, lattice: summary, latticeItems: append([]ItemInstance(nil), items...)}
}

func (p *PackedContainer) Container() *Container           { return p.container }
func (p *PackedContainer) Sequence() int64                 { return p.sequence }
func (p *PackedContainer) Placements() []Placement         { return append([]Placement(nil), p.placements...) }
func (p *PackedContainer) LatticeSummary() *LatticeSummary { return p.lattice }
func (p *PackedContainer) ID() string                      { return fmt.Sprintf("%s#%d", p.container.ID(), p.sequence) }

// PlacementCount counts lattice instances too.
func (p *PackedContainer) PlacementCount() int64 {
	if p.lattice != nil {
		return p.lattice.Count
	}
	return int64(len(p.placements))
}

func (p *PackedContainer) PayloadWeight() units.Weight {
	if p.lattice != nil {
		return units.MustWeight(p.lattice.TotalWeightTicks())
	}
	total := int64(0)
	for _, placement := range p.placements {
		total = ints.AddExact(total, placement.instance.Item().Weight().Ticks())
	}
	return units.MustWeight(total)
}

func (p *PackedContainer) GrossWeight() units.Weight {
	return units.MustWeight(ints.AddExact(p.container.TareWeight().Ticks(), p.PayloadWeight().Ticks()))
}

func (p *PackedContainer) UsedVolume() *big.Int {
	if p.lattice != nil {
		return p.lattice.UsedVolume()
	}
	return UsedVolume(p.placements)
}

// Utilization is used volume over inner volume, correctly rounded to the nearest double as
// the reference's exact integer division is.
func (p *PackedContainer) Utilization() float64 {
	ratio, _ := new(big.Rat).SetFrac(p.UsedVolume(), p.container.InnerDimensions().Volume()).Float64()
	return ratio
}

func (p *PackedContainer) MaxZTicks() int64 {
	if p.lattice != nil {
		return p.lattice.MaxZTicks()
	}
	top := int64(0)
	for _, placement := range p.placements {
		top = ints.Max(top, placement.EnvelopeBox().Z2())
	}
	return top
}

func (p *PackedContainer) CentreOfMassOffsetPPM() int64 {
	inner := p.container.InnerDimensions()
	if p.lattice != nil {
		return p.lattice.CentreOfMassOffsetPPM(inner.length.Ticks(), inner.width.Ticks())
	}
	return CentreOfMassOffsetPPM(inner, p.placements)
}

// ExpandPlacements is every placement, reconstructed from the lattice when there is one.
func (p *PackedContainer) ExpandPlacements() []Placement {
	if p.lattice != nil {
		return p.lattice.Expand(p.latticeItems)
	}
	return p.Placements()
}

// AsItem turns the packed container into an item for the next nesting level.
func (p *PackedContainer) AsItem(id string, keepUpright bool, maxTopLoad *units.Weight) (*Item, error) {
	if id == "" {
		id = p.ID()
	}
	spec := NewItemSpec(id, p.container.EffectiveOuterDimensions())
	spec.Weight = p.GrossWeight()
	spec.KeepUpright = keepUpright
	spec.MaxTopLoad = maxTopLoad
	spec.Metadata = map[string]interface{}{"source_packed_container": p.ID()}
	return NewItem(spec)
}
