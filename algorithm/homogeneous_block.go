package algorithm

import (
	"math/big"
	"sort"

	"github.com/toxakara/packvium-go/config"
	"github.com/toxakara/packvium-go/constraint"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/internal/ints"
)

// HomogeneousBlockSolver is a deterministic solid single-type block search for unrestricted
// carton loading.
//
// Candidate enumeration is O(B*S*T*R*X*Y) time and O(S+n) space: B committed blocks, S the
// capped maximal-space count, T declared item types, R unique rotations, X/Y the block grid
// extents. Both the deadline and containerPlanNodeLimit bound the product explicitly.
type HomogeneousBlockSolver struct {
	constraints []constraint.PlacementConstraint
}

// NewHomogeneousBlockSolver falls back to the extreme-point beam whenever constraints is not
// empty.
func NewHomogeneousBlockSolver(constraints []constraint.PlacementConstraint) *HomogeneousBlockSolver {
	return &HomogeneousBlockSolver{append([]constraint.PlacementConstraint(nil), constraints...)}
}

func (*HomogeneousBlockSolver) Name() string           { return "homogeneous_blocks" }
func (*HomogeneousBlockSolver) OrderInsensitive() bool { return true }

type blockMode int

const (
	countMode blockMode = iota
	volumeMode
)

// PackOne keeps the better of a count-first and a volume-first block search.
func (s *HomogeneousBlockSolver) PackOne(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) SingleContainerSolution {
	if !s.supports(container, items) {
		return NewExtremePointSolver(s.constraints, DefaultCandidateScorer{}).PackOne(container, sequence, items, cfg,
			stats, deadline)
	}
	var best *SingleContainerSolution
	reached := false
	for _, mode := range []blockMode{countMode, volumeMode} {
		if deadline.Expired() {
			reached = true
			break
		}
		solution := packBlocks(container, sequence, items, cfg, stats, deadline, mode)
		reached = reached || solution.TimeLimitReached
		if best == nil || blockSolutionKey(solution).Compare(blockSolutionKey(*best)) < 0 {
			kept := solution
			best = &kept
		}
	}
	if best == nil {
		return SingleContainerSolution{State: NewContainerState(container, sequence), Unpacked: items,
			TimeLimitReached: reached}
	}
	return SingleContainerSolution{State: best.State, Unpacked: best.Unpacked, TimeLimitReached: reached}
}

func (s *HomogeneousBlockSolver) supports(container *domain.Container, items []domain.ItemInstance) bool {
	if len(s.constraints) > 0 || len(container.Obstacles()) > 0 || container.Axles() != nil ||
		len(container.TagLimitNames()) > 0 || container.MaxStackDensity() != nil || container.VoidFillReserveRatio() > 0 {
		return false
	}
	for _, instance := range items {
		if !unrestricted(instance.Item()) {
			return false
		}
	}
	return true
}

func unrestricted(item *domain.Item) bool {
	rule := item.GroundContactRule()
	return item.Group() == nil && item.Tags().Len() == 0 && item.IncompatibleTags().Len() == 0 &&
		item.EligibleContainerTags().Len() == 0 && item.Stackable() && !item.MustBeOnFloor() &&
		item.MaxTopLoad() == nil && item.MaxStackedItems() == nil && item.MinimumSupportRatio() == 0.0 &&
		(rule == nil || *rule == "free") && item.NestingHeight() == nil && item.StopIndex() == nil
}

func blockSolutionKey(solution SingleContainerSolution) SortKey {
	return SortKey{Int(int64(len(solution.Unpacked))), Big(negated(solution.State.UsedVolume)),
		Int(solution.State.MaxZ), Str(solution.State.PlacementSignature())}
}

// remainingByType is the unplaced instances per declared id, walked in code-point order of
// the id as the reference's sorted map is.
type remainingByType struct {
	ids       []string
	instances map[string][]domain.ItemInstance
}

func newRemainingByType(items []domain.ItemInstance) *remainingByType {
	remaining := &remainingByType{instances: map[string][]domain.ItemInstance{}}
	for _, instance := range items {
		id := instance.Item().ID()
		if _, known := remaining.instances[id]; !known {
			remaining.ids = append(remaining.ids, id)
		}
		remaining.instances[id] = append(remaining.instances[id], instance)
	}
	sort.Strings(remaining.ids)
	return remaining
}

func (r *remainingByType) any() bool {
	for _, id := range r.ids {
		if len(r.instances[id]) > 0 {
			return true
		}
	}
	return false
}

func (r *remainingByType) all() []domain.ItemInstance {
	var out []domain.ItemInstance
	for _, id := range r.ids {
		out = append(out, r.instances[id]...)
	}
	return out
}

// searchCut is the node limit or the deadline cutting the enumeration; the incumbent of that
// round is dropped.
type searchCut struct{}

type block struct {
	space              Space
	itemID             string
	rotation           domain.Rotation
	physical, envelope domain.Dimensions
	nx, ny, nz, count  int64
	score              SortKey
}

func packBlocks(container *domain.Container, sequence int64, items []domain.ItemInstance, cfg *config.PackingConfig,
	stats *SearchStats, deadline *Deadline, mode blockMode) SingleContainerSolution {
	state := NewContainerState(container, sequence)
	spaces := []Space{{domain.Point{}, container.InnerDimensions()}}
	remaining := newRemainingByType(items)
	nodes := int64(0)
	reached := false
	for len(spaces) > 0 && remaining.any() {
		best, cut := bestBlockOrCut(container, state, spaces, remaining, cfg, stats, deadline, mode, &nodes)
		if cut {
			reached = true
			break
		}
		if best == nil {
			break
		}
		available := remaining.instances[best.itemID]
		chosen := available[:best.count]
		remaining.instances[best.itemID] = available[best.count:]
		commitBlock(state, best, chosen, cfg, stats)
		blockDimensions := domain.MustDimensions(best.nx*best.envelope.Length().Ticks(),
			best.ny*best.envelope.Width().Ticks(), best.nz*best.envelope.Height().Ticks())
		spaces = SubtractAll(spaces, []domain.Box{domain.NewBox(best.space.Origin, blockDimensions)}, stats)
	}
	return SingleContainerSolution{State: state, Unpacked: remaining.all(), TimeLimitReached: reached || deadline.Expired()}
}

func bestBlockOrCut(container *domain.Container, state *ContainerState, spaces []Space, remaining *remainingByType,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline, mode blockMode, nodes *int64) (best *block, cut bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(searchCut); !ok {
				panic(recovered)
			}
			best, cut = nil, true
		}
	}()
	return bestBlock(container, state, spaces, remaining, cfg, stats, deadline, mode, nodes), false
}

func bestBlock(container *domain.Container, state *ContainerState, spaces []Space, remaining *remainingByType,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline, mode blockMode, nodes *int64) *block {
	var best *block
	for _, space := range spaces {
		spaceVolume := space.Dimensions.Volume()
		for _, id := range remaining.ids {
			available := remaining.instances[id]
			if len(available) == 0 {
				continue
			}
			prototype := available[0]
			capacity := int64(len(available))
			if limit := container.MaxItems(); limit != nil {
				capacity = ints.Min(capacity, *limit-state.PlacementCount())
			}
			if payload := container.MaxPayload(); payload != nil && prototype.Item().Weight().Ticks() > 0 {
				capacity = ints.Min(capacity, (payload.Ticks()-state.PayloadTicks)/prototype.Item().Weight().Ticks())
			}
			if capacity <= 0 {
				continue
			}
			for _, oriented := range prototype.Item().Dimensions().UniqueRotations(prototype.Item().AllowedRotations()) {
				best = bestInRotation(best, space, spaceVolume, id, oriented, capacity, cfg, stats, deadline, mode, nodes)
			}
		}
	}
	return best
}

func bestInRotation(incumbent *block, space Space, spaceVolume *big.Int, itemID string,
	oriented domain.OrientedDimensions, capacity int64, cfg *config.PackingConfig, stats *SearchStats,
	deadline *Deadline, mode blockMode, nodes *int64) *block {
	best := incumbent
	physical := oriented.Dimensions
	envelope := physical
	if cfg.Clearance().Ticks() != 0 {
		envelope = physical.Expand(cfg.Clearance())
	}
	maximumX := space.Dimensions.Length().Ticks() / envelope.Length().Ticks()
	maximumY := space.Dimensions.Width().Ticks() / envelope.Width().Ticks()
	maximumZ := space.Dimensions.Height().Ticks() / envelope.Height().Ticks()
	physicalVolume := physical.Volume()
	for nx := int64(1); nx <= ints.Min(maximumX, capacity); nx++ {
		maximumNY := ints.Min(maximumY, capacity/nx)
		for ny := int64(1); ny <= maximumNY; ny++ {
			if *nodes >= cfg.ContainerPlanNodeLimit() || deadline.Expired() {
				panic(searchCut{})
			}
			*nodes++
			stats.SearchNodesExpanded++
			nz := ints.Min(maximumZ, capacity/(nx*ny))
			if nz <= 0 {
				continue
			}
			count := nx * ny * nz
			usedVolume := new(big.Int).Mul(big.NewInt(count), physicalVolume)
			fill := new(big.Int).Mul(usedVolume, scorePPM)
			spaceFill := exactInt64(fill.Quo(fill, spaceVolume))
			var score SortKey
			if mode == countMode {
				score = SortKey{Int(-count), Int(-spaceFill), Big(negated(usedVolume))}
			} else {
				score = SortKey{Big(negated(usedVolume)), Int(-spaceFill), Int(-count)}
			}
			score = append(score, Int(space.Origin.Z()), Int(space.Origin.Y()), Int(space.Origin.X()), Str(itemID),
				Str(string(oriented.Rotation)), Int(nx), Int(ny), Int(nz))
			if best == nil || score.Compare(best.score) < 0 {
				best = &block{space, itemID, oriented.Rotation, physical, envelope, nx, ny, nz, count, score}
			}
		}
	}
	return best
}

func commitBlock(state *ContainerState, chosen *block, instances []domain.ItemInstance, cfg *config.PackingConfig,
	stats *SearchStats) {
	clearance := cfg.Clearance().Ticks()
	origin := chosen.space.Origin
	index := 0
	for z := int64(0); z < chosen.nz; z++ {
		for y := int64(0); y < chosen.ny; y++ {
			for x := int64(0); x < chosen.nx; x++ {
				point := domain.MustPoint(origin.X()+x*chosen.envelope.Length().Ticks(),
					origin.Y()+y*chosen.envelope.Width().Ticks(), origin.Z()+z*chosen.envelope.Height().Ticks())
				position := domain.MustPoint(point.X()+clearance, point.Y()+clearance, point.Z()+clearance)
				state.AddDirect(domain.NewPlacement(instances[index], position, chosen.rotation, chosen.physical, point,
					chosen.envelope, 1.0))
				index++
				stats.CandidatesEvaluated++
				stats.PlacementsAttempted++
			}
		}
	}
}
