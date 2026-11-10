package algorithm

import (
	"fmt"
	"sort"
	"strings"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
	"github.com/packvium/packvium-go/units"
)

// GridSolver lays a single item type out as a regular lattice. The lattice cannot express
// per-item physical rules, so every rule a regular stack could break is folded into the layer
// count and capacity before any placement is made; anything it cannot honour falls back to
// the general solver. O(r) to choose the lattice, O(n) to lay it out, O(r) for the compact
// form.
type GridSolver struct{}

func (GridSolver) Name() string           { return "grid" }
func (GridSolver) OrderInsensitive() bool { return true }

// latticeProfile is everything that decides an item's behaviour in a lattice, keyed by
// achievable physical footprint rather than declared id: the same carton listed under two
// SKUs, one rotated, is one physical item. Only equality of profiles is ever asked, so the
// profile is an unambiguous string.
func latticeProfile(item *domain.Item) string {
	var footprints []string
	for _, oriented := range item.Dimensions().UniqueRotations(item.AllowedRotations()) {
		physical := oriented.Dimensions
		footprints = append(footprints, fmt.Sprintf("%dx%dx%d", physical.Length().Ticks(), physical.Width().Ticks(),
			physical.Height().Ticks()))
	}
	sort.Strings(footprints)
	var profile strings.Builder
	field := func(value interface{}) { fmt.Fprintf(&profile, "%#v;", value) }
	field(footprints)
	field(item.Weight().Ticks())
	field(item.Stackable())
	field(item.MustBeOnFloor())
	field(optionalWeight(item.MaxTopLoad()))
	field(item.Tags().Sorted())
	field(item.IncompatibleTags().Sorted())
	field(item.EligibleContainerTags().Sorted())
	field(optionalString(item.GroundContactRule()))
	// Nesting overlap is valid only within one declared type: never borrow a prototype's
	// overlap allowance for another SKU.
	if item.NestingHeight() != nil {
		field(item.ID())
		field(item.NestingHeight().Ticks())
	} else {
		field(nil)
		field(nil)
	}
	field(optionalString(item.Group()))
	// Fusing a column limit of 1 with an unlimited item, or stop 0 with stop 1, would produce
	// a tiling neither allows.
	field(optionalInt(item.MaxStackedItems()))
	field(optionalInt(item.StopIndex()))
	return profile.String()
}

func optionalString(value *string) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func optionalInt(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func optionalWeight(value *units.Weight) interface{} {
	if value == nil {
		return nil
	}
	return value.Ticks()
}

// Supports reports whether every item shares one lattice profile.
func (GridSolver) Supports(items []domain.ItemInstance) bool {
	if len(items) == 0 {
		return false
	}
	profiles := map[*domain.Item]bool{}
	first := ""
	for index, instance := range items {
		if profiles[instance.Item()] {
			continue
		}
		profiles[instance.Item()] = true
		profile := latticeProfile(instance.Item())
		if index == 0 {
			first = profile
		} else if profile != first {
			return false
		}
	}
	return true
}

type lattice struct {
	rotation           domain.Rotation
	physical, envelope domain.Dimensions
	nx, ny, layerStep  int64
	capacity           int64
}

// PackOne builds the lattice, compactly when no coordinates are required.
func (g GridSolver) PackOne(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) SingleContainerSolution {
	if g.mustDelegate(container, items) {
		return NewExtremePointSolver(nil, DefaultCandidateScorer{}).PackOne(container, sequence, items, cfg, stats,
			deadline)
	}
	prototype := items[0].Item()
	state := NewContainerState(container, sequence)
	best := bestLattice(container, prototype, cfg)
	total := ints.Min(best.capacity, int64(len(items)))
	// A group is all-or-nothing: a lattice that cannot hold every member holds none.
	if prototype.Group() != nil && total < int64(len(items)) {
		return SingleContainerSolution{State: state, Unpacked: items, DominantLattice: true}
	}
	nestingTicks := int64(0)
	if height := prototype.NestingHeight(); height != nil {
		nestingTicks = height.Ticks()
	}
	placedSlice := items[:total]
	sameDeclaredType := true
	for _, item := range placedSlice {
		sameDeclaredType = sameDeclaredType && item.Item().ID() == placedSlice[0].Item().ID()
	}
	if total > 0 && !cfg.RequirePlacementCoordinates() && nestingTicks == 0 && sameDeclaredType {
		// Compact form: the lattice already determines every coordinate, so the per-instance
		// loop is skipped. Only for one declared id, which the summary records.
		summary, err := domain.NewLatticeSummary(domain.LatticeSummary{
			ItemType: prototype.ID(), Rotation: best.rotation, Physical: best.physical, Envelope: best.envelope,
			NX: best.nx, NY: best.ny, LayerStep: best.layerStep, ClearanceTicks: cfg.Clearance().Ticks(),
			Count: total, WeightTicks: prototype.Weight().Ticks(),
		})
		if err != nil {
			panic(err)
		}
		state.AddLattice(summary, placedSlice)
		stats.SearchNodesExpanded++
		stats.CandidatePointsConsidered += total
		stats.CandidatesEvaluated += total
		stats.PlacementsAttempted += total
		return plainSolution(state, items[total:])
	}
	placed := layOut(state, items, best, total, cfg, stats, deadline)
	if prototype.Group() != nil && placed < int64(len(items)) {
		return SingleContainerSolution{State: NewContainerState(container, sequence), Unpacked: items,
			TimeLimitReached: deadline.Expired(), DominantLattice: true}
	}
	return SingleContainerSolution{State: state, Unpacked: items[placed:],
		TimeLimitReached: placed < total && deadline.Expired(), DominantLattice: true}
}

func (g GridSolver) mustDelegate(container *domain.Container, items []domain.ItemInstance) bool {
	// A mixed-type list would place every item with the first item's dimensions.
	if len(container.Obstacles()) > 0 || len(items) == 0 || !g.Supports(items) {
		return true
	}
	prototype := items[0].Item()
	if prototype.Tags().Intersects(prototype.IncompatibleTags()) {
		return true
	}
	if prototype.EligibleContainerTags().Len() > 0 && !prototype.EligibleContainerTags().Intersects(container.Tags()) {
		return true
	}
	// Stack density needs the cumulative column load, axles the changing moment, and a hull or
	// a compressible item per-candidate geometry: the closed form sees none of them.
	return container.VoidFillReserveRatio() > 0 || container.MaxStackDensity() != nil || container.Axles() != nil ||
		prototype.ShapeType() != domain.RigidCuboid
}

func bestLattice(container *domain.Container, prototype *domain.Item, cfg *config.PackingConfig) lattice {
	// Every non-floor cell has one full-area direct supporter, which satisfies ratio 1, covered
	// and single; multiple is satisfiable only on the floor, so cap the layers.
	rule := prototype.GroundContactRule()
	singleLayer := prototype.MustBeOnFloor() || !prototype.Stackable() || (rule != nil && *rule == "multiple")
	// Zero unless the item sinks into an identical one beneath it.
	nestingTicks := int64(0)
	if height := prototype.NestingHeight(); height != nil {
		nestingTicks = height.Ticks()
	}
	var best lattice
	var bestScore SortKey
	inner := container.InnerDimensions()
	for _, oriented := range prototype.Dimensions().UniqueRotations(prototype.AllowedRotations()) {
		physical := oriented.Dimensions
		envelope := physical
		if cfg.Clearance().Ticks() != 0 {
			envelope = physical.Expand(cfg.Clearance())
		}
		nx := inner.Length().Ticks() / envelope.Length().Ticks()
		ny := inner.Width().Ticks() / envelope.Width().Ticks()
		layerStep := envelope.Height().Ticks() - nestingTicks
		innerHeight := inner.Height().Ticks()
		nz := int64(0)
		if innerHeight >= envelope.Height().Ticks() {
			nz = (innerHeight-envelope.Height().Ticks())/layerStep + 1
		}
		if singleLayer {
			nz = ints.Min(nz, 1)
		}
		// A uniform column's bottom item bears (nz - 1) * weight, so the safe layer count is
		// floor(maxTopLoad / weight) + 1; weightless items leave the geometry alone.
		if limit := prototype.MaxTopLoad(); limit != nil && prototype.Weight().Ticks() > 0 {
			if layersAbove := limit.Ticks() / prototype.Weight().Ticks(); layersAbove < nz-1 {
				nz = layersAbove + 1
			}
		}
		if limit := prototype.MaxStackedItems(); limit != nil {
			nz = ints.Min(nz, *limit+1)
		}
		capacity := latticeCapacity(container, prototype, ints.MulExact(ints.MulExact(nx, ny), nz))
		score := SortKey{Int(-capacity), Big(envelope.Volume()), Str(string(oriented.Rotation))}
		if bestScore == nil || score.Compare(bestScore) < 0 {
			bestScore = score
			best = lattice{oriented.Rotation, physical, envelope, nx, ny, layerStep, capacity}
		}
	}
	return best
}

func latticeCapacity(container *domain.Container, prototype *domain.Item, cells int64) int64 {
	capacity := cells
	if limit := container.MaxItems(); limit != nil {
		capacity = ints.Min(capacity, *limit)
	}
	if payload := container.MaxPayload(); payload != nil && prototype.Weight().Ticks() > 0 {
		capacity = ints.Min(capacity, payload.Ticks()/prototype.Weight().Ticks())
	}
	for _, tag := range prototype.Tags().Sorted() {
		if limit, ok := container.TagLimit(tag); ok {
			capacity = ints.Min(capacity, limit)
		}
	}
	return capacity
}

func layOut(state *ContainerState, items []domain.ItemInstance, best lattice, total int64, cfg *config.PackingConfig,
	stats *SearchStats, deadline *Deadline) int64 {
	clearance := cfg.Clearance().Ticks()
	placed := int64(0)
	for index := int64(0); index < total; index++ {
		if deadline.Expired() {
			break
		}
		stats.SearchNodesExpanded++
		stats.CandidatePointsConsidered++
		x, y, z := index%best.nx, index/best.nx%best.ny, index/(best.nx*best.ny)
		point := domain.MustPoint(x*best.envelope.Length().Ticks(), y*best.envelope.Width().Ticks(), z*best.layerStep)
		position := domain.MustPoint(point.X()+clearance, point.Y()+clearance, point.Z()+clearance)
		// A mixed-declared-type lattice shares footprints, not rotation labels, so the stored
		// rotation is looked up per item.
		item := items[index]
		own, found := matchingRotation(item.Item(), best.physical)
		if !found {
			panic("algorithm: the lattice profile guarantees a matching rotation")
		}
		state.AddDirect(domain.NewPlacement(item, position, own.Rotation, own.Dimensions, point, best.envelope, 1.0))
		placed++
		stats.CandidatesEvaluated++
		stats.PlacementsAttempted++
	}
	return placed
}

func matchingRotation(item *domain.Item, physical domain.Dimensions) (domain.OrientedDimensions, bool) {
	for _, candidate := range item.Dimensions().UniqueRotations(item.AllowedRotations()) {
		if candidate.Dimensions == physical {
			return candidate, true
		}
	}
	return domain.OrientedDimensions{}, false
}
