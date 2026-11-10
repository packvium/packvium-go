package constraint

import "github.com/packvium/packvium-go/domain"

// LoadUnit is the only facts load propagation needs about a box, real or hypothetical.
//
// The compression fields are set only for a compressible item. Load propagation already
// computes the cumulative mass above every unit, which is exactly the numerator the pressure
// model needs, so the crush check rides the graph that is built anyway. A nil pointer is an
// absent limit.
type LoadUnit struct {
	Box                       domain.Box
	WeightTicks               int64
	MaxTopLoadTicks           *int64
	MaxStackedItems           *int64
	Label                     string
	NestingItemID             *string
	NestingHeightTicks        *int64
	CompressionRatioPPM       *int64
	MaxCompressionPressureKPA *int64
}

// Units is one load unit per placement, in placement order, with room left for one more.
// O(n).
func Units(placements []domain.Placement) []LoadUnit {
	units := make([]LoadUnit, len(placements), len(placements)+1)
	for index, placement := range placements {
		units[index] = limitedUnit(placement.EnvelopeBox(), placement.Instance())
	}
	return units
}

// UnitsWith is Units plus one more unit appended last.
func UnitsWith(placements []domain.Placement, extra LoadUnit) []LoadUnit {
	return append(Units(placements), extra)
}

// bareUnit carries the weight and nesting facts of an instance but none of its limits: the
// route and axle checks place a hypothetical box whose own limits they never read.
func bareUnit(box domain.Box, instance domain.ItemInstance) LoadUnit {
	item := instance.Item()
	unit := LoadUnit{Box: box, WeightTicks: item.Weight().Ticks(), Label: instance.ID()}
	if depth := item.NestingHeight(); depth != nil {
		id, ticks := item.ID(), depth.Ticks()
		unit.NestingItemID, unit.NestingHeightTicks = &id, &ticks
	}
	return unit
}

// limitedUnit is bareUnit plus every bearing, stacking and crushing limit the item declares.
func limitedUnit(box domain.Box, instance domain.ItemInstance) LoadUnit {
	unit := bareUnit(box, instance)
	item := instance.Item()
	if limit := item.MaxTopLoad(); limit != nil {
		ticks := limit.Ticks()
		unit.MaxTopLoadTicks = &ticks
	}
	unit.MaxStackedItems = item.MaxStackedItems()
	unit.CompressionRatioPPM = item.CompressionRatioPPM()
	unit.MaxCompressionPressureKPA = item.MaxCompressionPressureKPA()
	return unit
}
