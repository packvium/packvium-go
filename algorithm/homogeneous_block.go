package algorithm

import (
	"math/big"
	"sort"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
)

// HomogeneousBlockSolver is a deterministic solid single-type block search for unrestricted
// carton loading.
//
// A block is used only for the plain-box subset no per-placement business rule can
// distinguish, and only when no support is required: a block set on a smaller one overhangs
// it, so the members of its bottom layer report the support they really have. Requests
// outside that subset fall back to the extreme-point beam rather than weakening a constraint.
//
// Candidate enumeration is O(B*S*T*R*X*Y) time and O(S+n) space: B committed blocks, S the
// capped maximal-space count, T declared item types, R unique rotations, X/Y the block grid
// extents. Both the deadline and containerPlanNodeLimit bound the product explicitly.
type HomogeneousBlockSolver struct {
	constraints []constraint.PlacementConstraint
	modes       []blockMode
	// nodeLimit replaces the configuration's containerPlanNodeLimit when set: fast and
	// balanced leave that at one, which would stop the lattice enumeration before its first
	// block, so the block start they add carries its own bound.
	nodeLimit *int64
}

// NewHomogeneousBlockSolver falls back to the extreme-point beam whenever constraints is not
// empty.
func NewHomogeneousBlockSolver(constraints []constraint.PlacementConstraint) *HomogeneousBlockSolver {
	return &HomogeneousBlockSolver{constraints: append([]constraint.PlacementConstraint(nil), constraints...),
		modes: []blockMode{countMode, volumeMode}}
}

// newBoundedBlockSolver is the block start fast and balanced add: the given modes, and its
// own lattice node bound per mode.
func newBoundedBlockSolver(modes []blockMode, nodeLimit int64) *HomogeneousBlockSolver {
	return &HomogeneousBlockSolver{modes: modes, nodeLimit: &nodeLimit}
}

func (*HomogeneousBlockSolver) Name() string           { return "homogeneous_blocks" }
func (*HomogeneousBlockSolver) OrderInsensitive() bool { return true }

type blockMode int

const (
	countMode blockMode = iota
	volumeMode
)

// PackOne keeps the best of its modes' block searches: count first and volume first.
func (s *HomogeneousBlockSolver) PackOne(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) SingleContainerSolution {
	if !s.supports(container, items, cfg) {
		return NewExtremePointSolver(s.constraints, DefaultCandidateScorer{}).PackOne(container, sequence, items, cfg,
			stats, deadline)
	}
	nodeLimit := cfg.ContainerPlanNodeLimit()
	if s.nodeLimit != nil {
		nodeLimit = *s.nodeLimit
	}
	byVolume := ranksByVolume(cfg)
	var best *SingleContainerSolution
	reached := false
	// Group enumerations are pure arithmetic, so the second mode and every later round reuse
	// them (see blockLattice).
	lattices := map[latticeKey]latticeSummary{}
	for _, mode := range s.modes {
		if deadline.Expired() {
			reached = true
			break
		}
		solution := packBlocks(container, sequence, items, cfg, stats, deadline, mode, nodeLimit, lattices)
		if cfg.Profile() == config.Quality && !deadline.Expired() {
			solution = blockLookahead(container, sequence, items, cfg, stats, deadline, mode,
				nodeLimit*blockLookaheadNodeFactor, lattices, solution)
		}
		reached = reached || solution.TimeLimitReached
		if best == nil || blockSolutionKey(solution, byVolume).Compare(blockSolutionKey(*best, byVolume)) < 0 {
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

func (s *HomogeneousBlockSolver) supports(container *domain.Container, items []domain.ItemInstance,
	cfg *config.PackingConfig) bool {
	if len(s.constraints) > 0 || len(container.Obstacles()) > 0 || container.HasPreloaded() || container.Axles() != nil ||
		cfg.MinimumSupportRatio() > 0 || container.IsFlatBag() ||
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
	return item.ShapeType() == domain.RigidCuboid && item.Group() == nil && item.Tags().Len() == 0 && item.IncompatibleTags().Len() == 0 &&
		item.EligibleContainerTags().Len() == 0 && item.Stackable() && !item.MustBeOnFloor() &&
		item.MaxTopLoad() == nil && item.MaxStackedItems() == nil && item.MinimumSupportRatio() == 0.0 &&
		(rule == nil || *rule == "free") && item.NestingHeight() == nil && item.StopIndex() == nil &&
		// Block-on-block contacts would need a second implementation of the stacking-group
		// rule; the general solver already walks every edge.
		item.AcceptsStackingGroups().Len() == 0
}

func blockSolutionKey(solution SingleContainerSolution, byVolume bool) SortKey {
	if byVolume {
		return SortKey{Big(negated(solution.State.UsedVolume)), Int(int64(len(solution.Unpacked))),
			Int(solution.State.MaxZ), Nested(placementTuples(solution.State))}
	}
	return SortKey{Int(int64(len(solution.Unpacked))), Big(negated(solution.State.UsedVolume)),
		Int(solution.State.MaxZ), Nested(placementTuples(solution.State))}
}

// placementTuples is the placements in order as (id, x, y, z) envelope origins, compared
// field by field: the reference's last tie-break between two block answers is this tuple, not
// a signature string, and the two order coordinates differently ("10" before "9"). O(m).
func placementTuples(state *ContainerState) SortKey {
	tuples := make(SortKey, len(state.Placements))
	for index, placement := range state.Placements {
		origin := placement.EnvelopeOrigin()
		tuples[index] = Nested(SortKey{Str(placement.Instance().ID()), Int(origin.X()), Int(origin.Y()), Int(origin.Z())})
	}
	return tuples
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

type block struct {
	space              Space
	itemID             string
	rotation           domain.Rotation
	physical, envelope domain.Dimensions
	nx, ny, nz, count  int64
	score              SortKey
	// lead is -count, the count mode's first key field, kept to skip losing groups cheaply.
	lead int64
}

// The quality block lookahead completes this many leading blocks per round, within
// blockLookaheadNodeFactor times the mode's lattice node limit.
const (
	blockLookaheadWidth      = 4
	blockLookaheadNodeFactor = 64
)

func (r *remainingByType) copy() *remainingByType {
	other := &remainingByType{ids: r.ids, instances: make(map[string][]domain.ItemInstance, len(r.instances))}
	for id, available := range r.instances {
		// The solver only reslices, never appends, so the backing arrays can be shared.
		other.instances[id] = available
	}
	return other
}

// blockSearch is one block solve from the empty container: its state, free spaces, unplaced
// instances and each type's rotations.
type blockSearch struct {
	state     *ContainerState
	spaces    []Space
	remaining *remainingByType
	forms     map[string][]blockForm
}

func newBlockSearch(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig) blockSearch {
	remaining := newRemainingByType(items)
	forms := map[string][]blockForm{}
	for _, id := range remaining.ids {
		forms[id] = blockForms(remaining.instances[id][0], cfg)
	}
	return blockSearch{NewContainerState(container, sequence), []Space{{domain.Point{}, container.InnerDimensions()}},
		remaining, forms}
}

func (s blockSearch) copy() blockSearch {
	return blockSearch{s.state.Copy(), s.spaces, s.remaining.copy(), s.forms}
}

// commit places every member of chosen and removes its box from the free spaces.
func (s *blockSearch) commit(chosen *block, cfg *config.PackingConfig, stats *SearchStats) {
	available := s.remaining.instances[chosen.itemID]
	s.remaining.instances[chosen.itemID] = available[chosen.count:]
	commitBlock(s.state, chosen, available[:chosen.count], cfg, stats)
	blockDimensions := domain.MustDimensions(chosen.nx*chosen.envelope.Length().Ticks(),
		chosen.ny*chosen.envelope.Width().Ticks(), chosen.nz*chosen.envelope.Height().Ticks())
	s.spaces = SubtractAll(s.spaces, []domain.Box{domain.NewBox(chosen.space.Origin, blockDimensions)}, stats)
}

func packBlocks(container *domain.Container, sequence int64, items []domain.ItemInstance, cfg *config.PackingConfig,
	stats *SearchStats, deadline *Deadline, mode blockMode, nodeLimit int64,
	lattices map[latticeKey]latticeSummary) SingleContainerSolution {
	round := blockRound{container, cfg, stats, deadline, mode, nodeLimit, lattices, 0}
	solution := round.complete(newBlockSearch(container, sequence, items, cfg))
	solution.TimeLimitReached = solution.TimeLimitReached || deadline.Expired()
	return solution
}

// complete commits each round's leading block until nothing more fits, mutating search. The
// answer is marked stopped only when a limit cut a round, which then commits nothing.
func (r *blockRound) complete(search blockSearch) SingleContainerSolution {
	reached := false
	for len(search.spaces) > 0 && search.remaining.any() {
		leading, cut := r.leading(search, 1)
		if cut {
			reached = true
			break
		}
		if len(leading) == 0 {
			break
		}
		search.commit(leading[0], r.cfg, r.stats)
	}
	return SingleContainerSolution{State: search.state, Unpacked: search.remaining.all(), TimeLimitReached: reached}
}

// completion is the greedy completion after committing chosen, on a copy of the round's
// search.
func (r *blockRound) completion(search blockSearch, chosen *block) SingleContainerSolution {
	trial := search.copy()
	trial.commit(chosen, r.cfg, r.stats)
	return r.complete(trial)
}

// blockLookahead is greedy, or a better answer from completing each round's leading blocks.
// Each round ranks its blockLookaheadWidth leading blocks, completes each greedily on a copy
// and commits the block whose completion ranks best. The greedy answer is the first
// completion, so the result is never worse than it; the round's first block is the greedy
// choice after the block committed last, so its completion is already known and is not
// recomputed, unless the greedy pass was stopped by a limit, when the first round completes
// it under the lookahead's larger budget. The work counts against its own nodeBudget lattice
// nodes; when that runs out the best completion so far stands, and only the deadline or the
// effort budget marks the answer as stopped by a limit. Each round costs at most W + 1
// completions of the greedy pass's O(B*S*T*R*X*Y), all inside nodeBudget lattice nodes.
func blockLookahead(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline, mode blockMode, nodeBudget int64,
	lattices map[latticeKey]latticeSummary, greedy SingleContainerSolution) SingleContainerSolution {
	byVolume := ranksByVolume(cfg)
	round := blockRound{container, cfg, stats, deadline, mode, nodeBudget, lattices, 0}
	search := newBlockSearch(container, sequence, items, cfg)
	best, bestKey := greedy, blockSolutionKey(greedy, byVolume)
	var known *SingleContainerSolution
	if !greedy.TimeLimitReached {
		known = &greedy
	}
	for len(search.spaces) > 0 && search.remaining.any() {
		leading, cut := round.leading(search, blockLookaheadWidth)
		if cut || len(leading) == 0 {
			break
		}
		var chosen *lookaheadChoice
		trials := leading
		if known != nil {
			chosen = &lookaheadChoice{blockSolutionKey(*known, byVolume), *known, leading[0]}
			trials = leading[1:]
		}
		for _, candidate := range trials {
			trial := round.completion(search, candidate)
			if trial.TimeLimitReached {
				cut = true
				break
			}
			key := blockSolutionKey(trial, byVolume)
			if chosen == nil || key.Compare(chosen.key) < 0 {
				chosen = &lookaheadChoice{key, trial, candidate}
			}
		}
		if cut || chosen == nil {
			break
		}
		known = &chosen.solution
		if chosen.key.Compare(bestKey) < 0 {
			best, bestKey = chosen.solution, chosen.key
		}
		search.commit(chosen.block, cfg, stats)
	}
	return SingleContainerSolution{State: best.State, Unpacked: best.Unpacked,
		TimeLimitReached: best.TimeLimitReached || deadline.Expired()}
}

type lookaheadChoice struct {
	key      SortKey
	solution SingleContainerSolution
	block    *block
}

// blockForm is one unique rotation of a type with its envelope and physical volume.
type blockForm struct {
	rotation           domain.Rotation
	physical, envelope domain.Dimensions
	volume             *big.Int
}

func blockForms(prototype domain.ItemInstance, cfg *config.PackingConfig) []blockForm {
	var forms []blockForm
	for _, oriented := range prototype.Item().Dimensions().UniqueRotations(prototype.Item().AllowedRotations()) {
		envelope := oriented.Dimensions
		if cfg.Clearance().Ticks() != 0 {
			envelope = envelope.Expand(cfg.Clearance())
		}
		forms = append(forms, blockForm{oriented.Rotation, oriented.Dimensions, envelope, oriented.Dimensions.Volume()})
	}
	return forms
}

// latticeMemoLimit is the distinct group shapes one block solve remembers. The memo only
// saves arithmetic, so a full one is simply not added to.
const latticeMemoLimit = 4096

type latticeKey struct{ columns, rows, layers, capacity int64 }

// latticeSummary is one group's node count and its first largest block in (nx, ny) order.
type latticeSummary struct{ nodes, count, nx, ny, nz int64 }

// blockLattice summarises one (space, type, rotation) group's (nx, ny) enumeration: nx in
// 1..columns, ny in 1..min(rows, capacity / nx), every node counted whether or not it has a
// layer. Within a group every key field but the lead is equal and the lead improves strictly
// with the count in both modes, so the first largest count is the only node that can win.
// The answer depends on these four integers alone, which is what lets it be memoised.
// O(columns * rows) time, O(1) space.
func blockLattice(key latticeKey) latticeSummary {
	var summary latticeSummary
	for nx := int64(1); nx <= key.columns; nx++ {
		maximumNY := ints.Min(key.rows, key.capacity/nx)
		summary.nodes += maximumNY
		for ny := int64(1); ny <= maximumNY; ny++ {
			nz := ints.Min(key.layers, key.capacity/(nx*ny))
			if nz > 0 && nx*ny*nz > summary.count {
				summary.count, summary.nx, summary.ny, summary.nz = nx*ny*nz, nx, ny, nz
			}
		}
	}
	return summary
}

// blockRound is one mode's search state across its rounds: the node count it has spent.
type blockRound struct {
	container *domain.Container
	cfg       *config.PackingConfig
	stats     *SearchStats
	deadline  *Deadline
	mode      blockMode
	nodeLimit int64
	lattices  map[latticeKey]latticeSummary
	nodes     int64
}

// capacity is how many of a type one block may still take: stock, item limit and payload.
func (r *blockRound) capacity(state *ContainerState, available []domain.ItemInstance) int64 {
	capacity := int64(len(available))
	if limit := r.container.MaxItems(); limit != nil {
		capacity = ints.Min(capacity, *limit-state.PlacementCount())
	}
	if payload := r.container.MaxPayload(); payload != nil && available[0].Item().Weight().Ticks() > 0 {
		capacity = ints.Min(capacity, (payload.Ticks()-state.PayloadTicks)/available[0].Item().Weight().Ticks())
	}
	return capacity
}

// leading is one round's leaders leading blocks, best first, or cut when a limit stopped the
// round, which then commits nothing. A group's nodes are counted at once: its first node
// polls the deadline as a per-node loop would, and a counted limit falling inside the group
// is charged exactly the nodes such a loop would have expanded before stopping. O(S * T * R)
// groups per round, each O(X * Y) on a memo miss, plus O(W) to keep W leaders.
func (r *blockRound) leading(search blockSearch, leaders int) ([]*block, bool) {
	ceiling := r.deadline.searchNodeCeiling(r.stats)
	type eligible struct {
		id       string
		capacity int64
	}
	// Capacity reads only the committed state, so it is fixed for the whole round.
	var types []eligible
	for _, id := range search.remaining.ids {
		if available := search.remaining.instances[id]; len(available) > 0 {
			if capacity := r.capacity(search.state, available); capacity > 0 {
				types = append(types, eligible{id, capacity})
			}
		}
	}
	var leading []*block
	for _, space := range search.spaces {
		length, width, height := space.Dimensions.Length().Ticks(), space.Dimensions.Width().Ticks(),
			space.Dimensions.Height().Ticks()
		for _, candidate := range types {
			for _, form := range search.forms[candidate.id] {
				columns := ints.Min(length/form.envelope.Length().Ticks(), candidate.capacity)
				rows := width / form.envelope.Width().Ticks()
				if columns == 0 || rows == 0 {
					continue
				}
				if r.nodes >= r.nodeLimit || r.deadline.Expired() {
					return leading, true
				}
				key := latticeKey{columns, rows, height / form.envelope.Height().Ticks(), candidate.capacity}
				summary, known := r.lattices[key]
				if !known {
					summary = blockLattice(key)
					if len(r.lattices) < latticeMemoLimit {
						r.lattices[key] = summary
					}
				}
				budget := r.nodeLimit - r.nodes
				if ceiling != nil {
					budget = ints.Min(budget, *ceiling-r.stats.SearchNodesExpanded)
				}
				if summary.nodes > budget {
					r.stats.SearchNodesExpanded += budget
					r.nodes += budget
					return leading, true
				}
				r.nodes += summary.nodes
				r.stats.SearchNodesExpanded += summary.nodes
				if summary.count > 0 {
					leading = r.keep(leading, leaders, space, candidate.id, form, summary)
				}
			}
		}
	}
	return leading, false
}

// keep is leading with this group's winning block among the leaders when its key ranks
// strictly ahead of the last kept one; equal keys keep their arrival order.
func (r *blockRound) keep(leading []*block, leaders int, space Space, itemID string, form blockForm,
	summary latticeSummary) []*block {
	count := summary.count
	full := len(leading) == leaders
	// The key's first field is -count or -used volume; a group that loses on it to the last
	// kept block loses on the key, so its key is never built.
	if full && r.mode == countMode && -count > leading[len(leading)-1].lead {
		return leading
	}
	usedVolume := new(big.Int).Mul(big.NewInt(count), form.volume)
	fill := new(big.Int).Mul(usedVolume, scorePPM)
	spaceFill := exactInt64(fill.Quo(fill, space.Dimensions.Volume()))
	var score SortKey
	if r.mode == countMode {
		score = SortKey{Int(-count), Int(-spaceFill), Big(negated(usedVolume))}
	} else {
		score = SortKey{Big(negated(usedVolume)), Int(-spaceFill), Int(-count)}
	}
	score = append(score, Int(space.Origin.Z()), Int(space.Origin.Y()), Int(space.Origin.X()), Str(itemID),
		Str(string(form.rotation)), Int(summary.nx), Int(summary.ny), Int(summary.nz))
	if full && score.Compare(leading[len(leading)-1].score) >= 0 {
		return leading
	}
	at := len(leading)
	for at > 0 && score.Compare(leading[at-1].score) < 0 {
		at--
	}
	kept := &block{space, itemID, form.rotation, form.physical, form.envelope, summary.nx, summary.ny, summary.nz,
		count, score, -count}
	leading = append(leading, nil)
	copy(leading[at+1:], leading[at:])
	leading[at] = kept
	if len(leading) > leaders {
		leading = leading[:leaders]
	}
	return leading
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
				// Above its bottom layer a member rests on an identical footprint; the bottom
				// layer rests on whatever is there.
				support := 1.0
				if z == 0 {
					// O(m) over the m placements already in the container, once per bottom member.
					support = SupportRatio(state, instances[index], Candidate{Point: point, EnvelopeDimensions: chosen.envelope})
				}
				state.AddDirect(domain.NewPlacement(instances[index], position, chosen.rotation, chosen.physical, point,
					chosen.envelope, support))
				index++
				stats.CandidatesEvaluated++
				stats.PlacementsAttempted++
			}
		}
	}
}
