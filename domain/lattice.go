package domain

import (
	"errors"
	"math/big"

	"github.com/toxakara/packvium-go/internal/ints"
)

// LatticeSummary is the compact, O(1)-to-build description of one regular-lattice run. Expand
// reconstructs the exact placements the per-item loop would have built, so the compact form
// never loses information. Only used for items without a nesting height.
type LatticeSummary struct {
	ItemType       string
	Rotation       Rotation
	Physical       Dimensions
	Envelope       Dimensions
	NX, NY         int64
	LayerStep      int64
	ClearanceTicks int64
	Count          int64
	WeightTicks    int64
}

// NewLatticeSummary refuses an empty or zero-capacity lattice.
func NewLatticeSummary(summary LatticeSummary) (*LatticeSummary, error) {
	if summary.Count <= 0 {
		return nil, errors.New("a lattice summary must describe at least one placed instance")
	}
	if summary.NX <= 0 || summary.NY <= 0 {
		return nil, errors.New("a lattice summary requires positive per-axis capacity")
	}
	return &summary, nil
}

func (l *LatticeSummary) perLayer() int64   { return ints.MulExact(l.NX, l.NY) }
func (l *LatticeSummary) FullLayers() int64 { return l.Count / l.perLayer() }
func (l *LatticeSummary) Remainder() int64  { return l.Count % l.perLayer() }

// LayersUsed counts a partial top layer as one.
func (l *LatticeSummary) LayersUsed() int64 {
	if l.Remainder() > 0 {
		return l.FullLayers() + 1
	}
	return l.FullLayers()
}

func (l *LatticeSummary) TotalWeightTicks() int64 { return ints.MulExact(l.Count, l.WeightTicks) }

// UsedVolume is a plain per-item volume sum: no nesting term applies.
func (l *LatticeSummary) UsedVolume() *big.Int {
	return new(big.Int).Mul(big.NewInt(l.Count), l.Physical.Volume())
}

// MaxZTicks is the topmost placed item's envelope z2.
func (l *LatticeSummary) MaxZTicks() int64 {
	return ints.AddExact(ints.MulExact(l.LayersUsed()-1, l.LayerStep), l.Envelope.height.Ticks())
}

func triangular(n int64) int64 { return ints.MulExact(n, n-1) / 2 }

// CentreOfMassOffsetPPM is the closed form of CentreOfMassOffsetPPM for a uniform lattice that
// fills x fastest, then y, then z.
func (l *LatticeSummary) CentreOfMassOffsetPPM(innerLength, innerWidth int64) int64 {
	if l.WeightTicks == 0 || l.Count == 0 {
		return 0
	}
	fullLayers, remainder := l.FullLayers(), l.Remainder()
	rowsInPartial, extraColumns := remainder/l.NX, remainder%l.NX
	sumX := ints.AddExact(ints.AddExact(ints.MulExact(ints.MulExact(fullLayers, l.NY), triangular(l.NX)),
		ints.MulExact(rowsInPartial, triangular(l.NX))), triangular(extraColumns))
	sumY := ints.AddExact(ints.AddExact(ints.MulExact(ints.MulExact(fullLayers, l.NX), triangular(l.NY)),
		ints.MulExact(l.NX, triangular(rowsInPartial))), ints.MulExact(extraColumns, rowsInPartial))
	weight := big.NewInt(l.WeightTicks)
	doubledX := new(big.Int).Mul(weight, big.NewInt(l.doubledAxisSum(l.Envelope.length.Ticks(), sumX, l.Physical.length.Ticks())))
	doubledY := new(big.Int).Mul(weight, big.NewInt(l.doubledAxisSum(l.Envelope.width.Ticks(), sumY, l.Physical.width.Ticks())))
	total := big.NewInt(l.TotalWeightTicks())
	return ints.Max(axisOffsetPPM(doubledX, total, innerLength), axisOffsetPPM(doubledY, total, innerWidth))
}

func (l *LatticeSummary) doubledAxisSum(step, indexSum, dimension int64) int64 {
	return ints.AddExact(ints.MulExact(ints.MulExact(2, step), indexSum),
		ints.MulExact(l.Count, ints.AddExact(ints.MulExact(2, l.ClearanceTicks), dimension)))
}

// Expand is the exact placement list the per-item loop would have produced from
// items[0:count]. O(count).
func (l *LatticeSummary) Expand(items []ItemInstance) []Placement {
	out := make([]Placement, 0, l.Count)
	for index := int64(0); index < l.Count; index++ {
		x, y, z := index%l.NX, (index/l.NX)%l.NY, index/(l.NX*l.NY)
		point := Point{x * l.Envelope.length.Ticks(), y * l.Envelope.width.Ticks(), z * l.LayerStep}
		position := Point{point.x + l.ClearanceTicks, point.y + l.ClearanceTicks, point.z + l.ClearanceTicks}
		out = append(out, NewPlacement(items[index], position, l.Rotation, l.Physical, point, l.Envelope, 1.0))
	}
	return out
}
