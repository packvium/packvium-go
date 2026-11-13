package algorithm

import (
	"math"
	"math/big"
	"sort"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/hull"
)

// unlimited is the candidate count of a finder call that keeps every feasible placement.
const unlimited = -1

// FindCandidates is every feasible placement of item, best first, from the state's own
// points; max of unlimited keeps them all, otherwise at most max. restricted, when non-nil,
// limits the scan to those origins.
//
// O(p * r * (q + k)) per call for p scanned points (capped at maxCandidatePoints), r distinct
// rotations, q solids returned by the spatial index per query and k constraint checks.
func FindCandidates(state *ContainerState, item domain.ItemInstance, cfg *config.PackingConfig,
	constraints []constraint.PlacementConstraint, stats *SearchStats, deadline *Deadline, scorer CandidateScorer,
	max int, restricted []domain.Point) []Candidate {
	container := state.Container
	if limit := container.MaxItems(); limit != nil && int64(len(state.Placements)) >= *limit {
		return nil
	}
	if payload := container.MaxPayload(); payload != nil &&
		state.PayloadTicks+item.Item().Weight().Ticks() > payload.Ticks() {
		return nil
	}
	compressionSensitive := state.CompressionSensitive || item.Item().ShapeType() == domain.Compressible
	reserveNeedsCandidate := item.Item().NestingHeight() != nil || item.Item().ShapeType() == domain.ConvexHull ||
		compressionSensitive
	if container.VoidFillReserveRatio() > 0 && !reserveNeedsCandidate &&
		new(big.Int).Add(state.UsedVolume, item.Item().Dimensions().Volume()).Cmp(state.UsableVolume()) > 0 {
		return nil
	}
	forms := formsOf(item, cfg)
	scan := newScan(state, item, cfg, constraints, stats, deadline, scorer, max, compressionSensitive,
		reserveNeedsCandidate)
	for _, point := range scanOrder(state, item, cfg, forms, restricted) {
		scan.visit(point, forms)
	}
	return scan.result()
}

// form is one rotation's envelope and extents; they depend only on the rotation, so they are
// built once per call.
type form struct {
	rotation   domain.Rotation
	physical   domain.Dimensions
	envelope   domain.Dimensions
	shape      *hull.Shape
	dx, dy, dz int64
}

// formsOf keeps every allowed rotation of a hull, which is not the same solid under two
// rotations that give the same box; cuboids keep the box deduplication.
func formsOf(item domain.ItemInstance, cfg *config.PackingConfig) []form {
	clearance := cfg.Clearance().Ticks()
	spec := item.Item()
	exactHull := spec.HullCollisionIsExact(clearance == 0)
	var rotations []domain.OrientedDimensions
	if spec.ShapeType() == domain.ConvexHull {
		for _, rotation := range spec.AllowedRotations() {
			rotations = append(rotations, domain.OrientedDimensions{Rotation: rotation, Dimensions: spec.Dimensions().Rotated(rotation)})
		}
	} else {
		rotations = spec.Dimensions().UniqueRotations(spec.AllowedRotations())
	}
	forms := make([]form, 0, len(rotations))
	for _, oriented := range rotations {
		physical := oriented.Dimensions
		envelope := physical
		if clearance != 0 {
			envelope = physical.Expand(cfg.Clearance())
		}
		var shape *hull.Shape
		if exactHull {
			built, err := spec.HullShape(oriented.Rotation)
			if err != nil {
				panic(err)
			}
			shape = built
		}
		forms = append(forms, form{oriented.Rotation, physical, envelope, shape,
			envelope.Length().Ticks(), envelope.Width().Ticks(), envelope.Height().Ticks()})
	}
	return forms
}

func sortedZYX(points []domain.Point) []domain.Point {
	sort.SliceStable(points, func(i, j int) bool { return compareZYX(points[i], points[j]) < 0 })
	return points
}

func scanOrder(state *ContainerState, item domain.ItemInstance, cfg *config.PackingConfig, forms []form,
	restricted []domain.Point) []domain.Point {
	if restricted != nil {
		return sortedZYX(append([]domain.Point(nil), restricted...))
	}
	limit := cfg.MaxCandidatePoints()
	if limit > math.MaxInt32 {
		limit = math.MaxInt32
	}
	cap := int(limit)
	var scan []domain.Point
	if item.Item().NestingHeight() == nil {
		scan = append([]domain.Point(nil), state.OrderedPoints[:minInt(cap, len(state.OrderedPoints))]...)
	} else {
		merged := append([]domain.Point(nil), state.OrderedPoints...)
		known := make(map[domain.Point]struct{}, len(merged))
		for _, point := range merged {
			known[point] = struct{}{}
		}
		for _, point := range nestingPoints(state, item) {
			if _, duplicate := known[point]; !duplicate {
				known[point] = struct{}{}
				merged = append(merged, point)
			}
		}
		scan = sortedZYX(merged)
		scan = scan[:minInt(cap, len(scan))]
	}
	if state.Container.Axles() != nil {
		scan = append(scan, axleBalancedPoints(state, item, forms)...)
	}
	return scan
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// nestingPoints are the origins that would sink the item into an identical one already
// placed.
func nestingPoints(state *ContainerState, item domain.ItemInstance) []domain.Point {
	depth := item.Item().NestingHeight().Ticks()
	var points []domain.Point
	known := map[domain.Point]struct{}{}
	for _, placement := range state.Placements {
		if placement.Instance().Item().ID() != item.Item().ID() {
			continue
		}
		z := placement.EnvelopeBox().Z2() - depth
		if z < 0 {
			continue
		}
		point := domain.MustPoint(placement.EnvelopeOrigin().X(), placement.EnvelopeOrigin().Y(), z)
		if _, duplicate := known[point]; !duplicate {
			known[point] = struct{}{}
			points = append(points, point)
		}
	}
	return sortedZYX(points)
}

// axleBalancedPoints are floor-level candidates that seat the item on an axle's own limit.
// Every other point is flush against a wall or a box, but with axle limits the only feasible
// spot can be open floor, chosen to keep the item's moment off one axle.
func axleBalancedPoints(state *ContainerState, item domain.ItemInstance, forms []form) []domain.Point {
	container := state.Container
	otherUnits := constraint.Units(state.Placements)
	tareTicks := container.TareWeight().Ticks()
	limitX := container.InnerDimensions().Length().Ticks()
	var floorYs []int64
	seenY := map[int64]bool{}
	for _, point := range state.points {
		if point.Z() == 0 && !seenY[point.Y()] {
			seenY[point.Y()] = true
			floorYs = append(floorYs, point.Y())
		}
	}
	if len(floorYs) == 0 {
		floorYs = []int64{0}
	}
	axles := *container.Axles()
	var points []domain.Point
	for _, candidate := range forms {
		dx := candidate.dx
		for _, x1 := range constraint.AxleBalancedOrigins(axles, otherUnits, tareTicks, limitX,
			item.Item().Weight().Ticks(), dx) {
			if x1 < 0 || x1 > limitX-dx {
				continue
			}
			for _, y := range floorYs {
				points = append(points, domain.MustPoint(x1, y, 0))
			}
		}
	}
	return points
}

// activeRule is one constraint the scan evaluates, and whether reaching it counts as a
// support check.
type activeRule struct {
	rule          constraint.PlacementConstraint
	countsSupport bool
}

// scan is the per-call state: what has been found so far and the inputs every point shares.
type scan struct {
	state                *ContainerState
	item                 domain.ItemInstance
	cfg                  *config.PackingConfig
	active               []activeRule
	stats                *SearchStats
	deadline             *Deadline
	scorer               CandidateScorer
	max                  int
	compressionSensitive bool
	reserveCheck         bool
	nesting              bool
	stackSensitive       bool
	routeSensitive       bool
	placementOffset      int
	clearance            int64
	found                []Candidate
	best                 *Candidate
}

func newScan(state *ContainerState, item domain.ItemInstance, cfg *config.PackingConfig,
	constraints []constraint.PlacementConstraint, stats *SearchStats, deadline *Deadline, scorer CandidateScorer,
	max int, compressionSensitive, reserveNeedsCandidate bool) *scan {
	stackSensitive := state.StackSensitive || item.Item().IsStackSensitive() || state.Container.MaxStackDensity() != nil
	routeSensitive := state.RouteSensitive || item.Item().StopIndex() != nil
	return &scan{
		state: state, item: item, cfg: cfg,
		active: activeRules(constraints, state.Container, item, stackSensitive, routeSensitive),
		stats:  stats, deadline: deadline, scorer: scorer, max: max, compressionSensitive: compressionSensitive,
		reserveCheck:    state.Container.VoidFillReserveRatio() > 0 && reserveNeedsCandidate,
		nesting:         item.Item().NestingHeight() != nil,
		stackSensitive:  stackSensitive,
		routeSensitive:  routeSensitive,
		placementOffset: len(state.Bounds) - len(state.Placements),
		clearance:       cfg.Clearance().Ticks(),
	}
}

// activeRules prunes the chain once per call rather than answering "allow" once per position.
// The support rule is never inert, so SupportChecks still counts every time the chain reaches
// it.
func activeRules(constraints []constraint.PlacementConstraint, container *domain.Container, item domain.ItemInstance,
	stackSensitive, routeSensitive bool) []activeRule {
	kept := constraint.Active(constraints, container, item, stackSensitive, routeSensitive)
	rules := make([]activeRule, len(kept))
	for index, rule := range kept {
		_, support := rule.(*constraint.SupportConstraint)
		rules[index] = activeRule{rule, support}
	}
	return rules
}

func (s *scan) visit(point domain.Point, forms []form) {
	s.deadline.Check()
	s.stats.CandidatePointsConsidered++
	inner := s.state.Container.InnerDimensions()
	for index := range forms {
		candidateForm := &forms[index]
		s.stats.PlacementsAttempted++
		x2, y2, z2 := point.X()+candidateForm.dx, point.Y()+candidateForm.dy, point.Z()+candidateForm.dz
		if x2 > inner.Length().Ticks() || y2 > inner.Width().Ticks() || z2 > inner.Height().Ticks() {
			continue
		}
		position := domain.MustPoint(point.X()+s.clearance, point.Y()+s.clearance, point.Z()+s.clearance)
		var tentative *domain.Placement
		if s.nesting {
			placement := s.placementAt(position, point, candidateForm)
			tentative = &placement
		}
		if s.blocked(point, x2, y2, z2, candidateForm, tentative) || !s.allowed(point, candidateForm) {
			continue
		}
		candidate := Candidate{point, position, candidateForm.rotation, candidateForm.physical, candidateForm.envelope,
			s.scorer.Score(s.state, point, candidateForm.envelope)}
		if s.reserveCheck {
			reservePlacement := s.placementAt(position, point, candidateForm)
			if tentative != nil {
				reservePlacement = *tentative
			}
			if s.exceedsReserve(reservePlacement) {
				continue
			}
		}
		s.stats.CandidatesEvaluated++
		s.keep(candidate)
	}
}

func (s *scan) placementAt(position, point domain.Point, candidateForm *form) domain.Placement {
	return domain.NewPlacement(s.item, position, candidateForm.rotation, candidateForm.physical, point,
		candidateForm.envelope, 1.0)
}

func (s *scan) blocked(point domain.Point, x2, y2, z2 int64, candidateForm *form, tentative *domain.Placement) bool {
	x1, y1, z1 := point.X(), point.Y(), point.Z()
	for _, candidateIndex := range s.state.Index.Query(x1, y1, z1, x2, y2, z2) {
		b := s.state.Bounds[candidateIndex]
		s.stats.CollisionChecks++
		if !(x1 < b[3] && b[0] < x2 && y1 < b[4] && b[1] < y2 && z1 < b[5] && b[2] < z2) {
			continue
		}
		placementIndex := candidateIndex - s.placementOffset
		if tentative != nil && placementIndex >= 0 &&
			domain.IsValidNesting(s.state.Placements[placementIndex], *tentative) {
			continue
		}
		blocker := s.state.HullShapes[candidateIndex]
		// The box test is the broad phase; only a hull on either side may overrule it, so a
		// request of ordinary boxes never reaches the exact test.
		if candidateForm.shape != nil || blocker != nil {
			own := candidateForm.shape
			if own == nil {
				own = hull.BoxShape(candidateForm.dx, candidateForm.dy, candidateForm.dz)
			}
			if blocker == nil {
				blocker = hull.BoxShape(b[3]-b[0], b[4]-b[1], b[5]-b[2])
			}
			if !hull.Collide(own, hull.Vertex{x1, y1, z1}, blocker, hull.Vertex{b[0], b[1], b[2]}) {
				continue
			}
		}
		return true
	}
	return false
}

func (s *scan) allowed(point domain.Point, candidateForm *form) bool {
	context := constraint.NewContext(s.state.Container, s.state.Placements, s.item, point, candidateForm.rotation,
		candidateForm.physical, candidateForm.envelope).WithSensitivity(s.stackSensitive, s.routeSensitive)
	for _, active := range s.active {
		if active.countsSupport {
			s.stats.SupportChecks++
		}
		if !active.rule.Evaluate(context).Allowed() {
			return false
		}
	}
	return true
}

func (s *scan) exceedsReserve(reservePlacement domain.Placement) bool {
	var projected *big.Int
	if s.compressionSensitive {
		// A zero-load candidate is at its largest, and adding load only shrinks existing
		// compressible supports. If that upper bound fits, the exact non-local refresh cannot
		// reject it; only candidates near the reserve boundary pay for it.
		upperBound := new(big.Int).Add(s.state.UsedVolume, domain.OccupiedVolume(reservePlacement))
		if upperBound.Cmp(s.state.UsableVolume()) <= 0 {
			projected = upperBound
		} else {
			withCandidate := append(append([]domain.Placement(nil), s.state.Placements...), reservePlacement)
			projected = domain.UsedVolume(AssignTopLoads(withCandidate))
		}
	} else {
		projected = new(big.Int).Add(s.state.UsedVolume, domain.UsedVolumeDelta(s.state.Placements, reservePlacement))
	}
	return projected.Cmp(s.state.UsableVolume()) > 0
}

func (s *scan) keep(candidate Candidate) {
	if s.max == 1 {
		if s.best == nil || candidate.Score.Compare(s.best.Score) < 0 {
			kept := candidate
			s.best = &kept
		}
		return
	}
	s.found = append(s.found, candidate)
}

func (s *scan) result() []Candidate {
	if s.max == 1 {
		if s.best == nil {
			return nil
		}
		return []Candidate{*s.best}
	}
	order := stableOrder(len(s.found), func(index int) SortKey { return s.found[index].Score })
	limit := len(order)
	if s.max != unlimited && s.max < limit {
		limit = s.max
	}
	out := make([]Candidate, limit)
	for position := 0; position < limit; position++ {
		out[position] = s.found[order[position]]
	}
	return out
}

// SupportRatio is the reported support fraction, from resting contact with other placements
// only.
func SupportRatio(state *ContainerState, item domain.ItemInstance, candidate Candidate) float64 {
	if candidate.Point.Z() == 0 {
		return 1.0
	}
	box := domain.NewBox(candidate.Point, candidate.EnvelopeDimensions)
	support := constraint.CandidateView(state.Placements, item, box)
	return float64(support.SupportingArea()) / float64(candidate.EnvelopeDimensions.BaseArea())
}
