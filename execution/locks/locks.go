// Package locks is operator locks, expressed as request-derived constraints: an operator
// saying "this item stays in this box, in this place" (docs/EXECUTION-PLAN.md).
//
// Honouring a lock must not become a way around the solver or the validator, and it is not.
// A lock is a constraint.PlacementConstraint supplied through the existing
// algorithm.ExtensionRegistry, and everything after that is the ordinary path: the same
// portfolio, the same constraints, the same independent validation. There is no lock-aware
// solver, no relaxed validator and no special case anywhere in the engine, which is why a
// lock cannot produce a placement the engine would otherwise refuse. The strongest thing a
// lock can do is forbid alternatives to itself.
//
// Four properties follow, and the first is what makes the rest safe.
//
// The approved plan is never mutated. ResolveWithLocks returns a LockedResolve carrying a
// separate result; the original is untouched and citable, so "what was approved" and "what
// was proposed after the lock" are two artifacts rather than two states of one.
//
// A physically invalid lock is rejected, not accommodated. The lock constraint refuses every
// candidate that is not the locked one; it never asserts that the locked one is legal.
// Support, top load, overlap and every other rule still run, so a lock that would float an
// item simply leaves it unplaced, and the result says so in the engine's own vocabulary.
//
// Infeasibility is an answer, not an error. A lock set the solver cannot satisfy comes back
// with Preserved false and the ordinary unpacked_items[].proof. Only a lock set that
// contradicts itself, which no request could satisfy, is a *LockSetError, returned before
// any solve.
//
// Preservation is verified, not assumed. Forbidding alternatives does not make the solver try
// the locked point; it only stops it succeeding anywhere else, so whether the lock held is
// checked against the returned result and reported.
//
// A lock reserves a slot; it does not demand priority. Locking one of eight identical cubes
// must leave the other seven free. While a lock is outstanding its box is off limits to every
// other candidate of that item type; the candidate that is the lock is admitted, and every
// candidate that does not touch the reserved volume is untouched. Reading a lock as a rule
// about the whole type, or as "fill this slot first", empties containers that were full: an
// empty container offers only the origin as a candidate point, so a lock on the far corner
// would never be reached. Nothing forces the slot to be filled; a reservation the solve never
// used comes back as a missing lock.
//
// Registering any caller constraint takes a request off the grid solver, which places by
// formula and never evaluates the constraint chain; that is the cost of locking, and a
// do-nothing constraint pays all of it. The lock's own cost per candidate is O(L) for L locks
// on the candidate's item type, plus one O(P) walk of the container's placements for each
// lock whose reserved box the candidate overlaps.
//
// The package is a caller of the engine, not a part of it: it imports serialization and the
// extension types, and nothing in the engine imports it.
package locks

import (
	"fmt"
	"math"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/pyrepr"
	"github.com/packvium/packvium-go/serialization"
)

// LockViolated is the rejection code a locked item type's non-locked candidate carries, named
// like every other constraint code so a trace, a diagnostic and an unpacked_items[].details
// entry read the same whether the refusal came from physics or from an operator.
const LockViolated = "operator_lock"

// LockSetError is a lock set no request could satisfy, returned before any solve.
//
// Whether a lock fits is a question about a request, and its answer is a LockedResolve with
// the lock named in Missing. Two locks whose boxes overlap cannot both hold against any
// request, container or solver; answering that with a solve would return an emptied container
// and call it a result. It is the same line the execution plan draws when it refuses a
// loading order that is not a permutation.
type LockSetError struct{ message string }

// Error is the message, in the reference's wording.
func (e *LockSetError) Error() string { return e.message }

func lockSetError(format string, arguments ...interface{}) error {
	return &LockSetError{fmt.Sprintf(format, arguments...)}
}

// PlacementLock is one operator lock: an item type pinned to an exact position and
// orientation.
//
// It is addressed the way the execution plan addresses a placement -- container index, item
// type, exact integer position ticks and orientation -- never by item id, because an id is an
// instance counter the engines need not agree on, and a lock that meant different boxes in
// different runtimes would be worse than no lock.
type PlacementLock struct {
	containerIndex int
	itemType       string
	orientation    domain.Rotation
	position       [3]int64
}

// NewPlacementLock checks that the address is one the engine could have emitted.
func NewPlacementLock(containerIndex int, itemType, orientation string, position [3]int64) (PlacementLock, error) {
	switch {
	case containerIndex < 0:
		return PlacementLock{}, fmt.Errorf("a lock must name a container index")
	case itemType == "":
		return PlacementLock{}, fmt.Errorf("a lock must name an item type")
	}
	rotation, err := domain.ParseRotation(orientation)
	if err != nil {
		return PlacementLock{}, fmt.Errorf("%s is not an orientation this engine emits", pyrepr.String(orientation))
	}
	for _, ticks := range position {
		if ticks < 0 {
			return PlacementLock{}, fmt.Errorf("a lock's position cannot be negative")
		}
	}
	return PlacementLock{containerIndex, itemType, rotation, position}, nil
}

// ContainerIndex is the container's position in the result's container list.
func (l PlacementLock) ContainerIndex() int { return l.containerIndex }

// ItemType is the id of the item type the lock pins.
func (l PlacementLock) ItemType() string { return l.itemType }

// Orientation is the rotation the item must keep.
func (l PlacementLock) Orientation() string { return string(l.orientation) }

// PositionTicks is the exact origin, x, y and z, in ticks.
func (l PlacementLock) PositionTicks() [3]int64 { return l.position }

// Slot is the reference's (orientation, position) pair as its repr prints it, the form its
// diagnostics quote.
func (l PlacementLock) Slot() string {
	return fmt.Sprintf("(%s, %s)", pyrepr.String(string(l.orientation)), l.positionText())
}

func (l PlacementLock) positionText() string {
	return fmt.Sprintf("(%d, %d, %d)", l.position[0], l.position[1], l.position[2])
}

func (l PlacementLock) sameSlot(rotation domain.Rotation, point domain.Point) bool {
	return l.orientation == rotation &&
		l.position == [3]int64{point.X(), point.Y(), point.Z()}
}

// LocksFromPlan reads locks out of an execution plan's own placement references: every step
// of the container at containerIndex whose item type is one of itemTypes, in step order.
//
// The plan is where an operator sees the placements, so it is where they point at one.
// Reading the references the plan already emits rather than a second address format is the
// only reason the two can be trusted to mean the same box.
func LocksFromPlan(plan map[string]interface{}, containerIndex int, itemTypes []string) ([]PlacementLock, error) {
	wanted := make(map[string]bool, len(itemTypes))
	for _, itemType := range itemTypes {
		wanted[itemType] = true
	}
	var locks []PlacementLock
	for _, entry := range listOf(plan["containers"]) {
		container, _ := entry.(map[string]interface{})
		if index, ok := wholeNumber(container["container_index"]); !ok || index != int64(containerIndex) {
			continue
		}
		for _, step := range listOf(container["steps"]) {
			stepFields, _ := step.(map[string]interface{})
			reference, _ := stepFields["placement"].(map[string]interface{})
			itemType, _ := reference["item_type"].(string)
			if !wanted[itemType] {
				continue
			}
			lock, err := lockFromReference(containerIndex, itemType, reference)
			if err != nil {
				return nil, err
			}
			locks = append(locks, lock)
		}
	}
	return locks, nil
}

func lockFromReference(containerIndex int, itemType string, reference map[string]interface{}) (PlacementLock, error) {
	orientation, _ := reference["orientation"].(string)
	ticks, _ := reference["position_ticks"].(map[string]interface{})
	var position [3]int64
	for axis, name := range []string{"x", "y", "z"} {
		value, ok := wholeNumber(ticks[name])
		if !ok {
			return PlacementLock{}, fmt.Errorf("the plan's position_ticks.%s is not an integer", name)
		}
		position[axis] = value
	}
	return NewPlacementLock(containerIndex, itemType, orientation, position)
}

// lockConstraint refuses a locked type's candidates while that type still owes a locked slot.
//
// It can say no and nothing else: it never runs instead of the other constraints, only
// alongside them, and every constraint must allow a candidate for it to be placed. An item
// type with no lock is untouched, a lock whose box cannot fit this container is not applied
// here, and a candidate that misses every reserved volume is allowed wherever it lands.
type lockConstraint struct{ locks []PlacementLock }

// Evaluate refuses a candidate of a locked type that would take an outstanding lock's box
// without being that lock. Equivalent to the reference's filter-then-scan, ordered so the
// placement walk runs only for a lock the candidate actually overlaps.
func (c lockConstraint) Evaluate(context constraint.Context) constraint.Result {
	item := context.Item().Item()
	candidate := region{[3]int64{context.Point().X(), context.Point().Y(), context.Point().Z()}, context.Dimensions()}
	for _, lock := range c.locks {
		if lock.itemType != item.ID() || !applicable(lock, item, context.Container()) {
			continue
		}
		if lock.sameSlot(context.Rotation(), context.Point()) {
			continue
		}
		if !candidate.intersects(reservedBox(lock, item)) || filled(lock, context.Placements()) {
			continue
		}
		return constraint.Reject(LockViolated,
			fmt.Sprintf("%s is locked to %s@%s", item.ID(), lock.orientation, lock.positionText()))
	}
	return constraint.Allow()
}

// applicable is whether this container could hold the locked box at all. A constraint cannot
// know which container index it is packing, so without this a lock read from one container
// would refuse every candidate of its type in a container too small for the locked origin.
// The test is the physical box, not the clearance envelope: it only drops locks that plainly
// cannot apply, and a lock it admits is still judged by the ordinary geometry rules. Written
// as subtractions so a far-away lock cannot overflow.
func applicable(lock PlacementLock, item *domain.Item, container *domain.Container) bool {
	box := item.Dimensions().Rotated(lock.orientation)
	inner := container.InnerDimensions()
	return lock.position[0] <= inner.Length().Ticks()-box.Length().Ticks() &&
		lock.position[1] <= inner.Width().Ticks()-box.Width().Ticks() &&
		lock.position[2] <= inner.Height().Ticks()-box.Height().Ticks()
}

// reservedBox is the volume the locked item will occupy, sized from its own type. The
// physical box rather than the clearance envelope: the clearance rule already guards a margin
// around whatever ends up here, and reserving it twice would refuse neighbours the engine is
// willing to place.
func reservedBox(lock PlacementLock, item *domain.Item) region {
	return region{lock.position, item.Dimensions().Rotated(lock.orientation)}
}

// filled is whether the container already holds the locked placement. Once it does, the
// ordinary overlap rule defends the volume and the reservation lapses.
func filled(lock PlacementLock, placements []domain.Placement) bool {
	for _, placement := range placements {
		if placement.Instance().Item().ID() == lock.itemType && lock.sameSlot(placement.Rotation(), placement.Position()) {
			return true
		}
	}
	return false
}

// region is an axis-aligned box over raw ticks. A lock's origin is whatever an operator typed,
// so the overlap test compares differences of non-negative coordinates instead of adding a
// size to a position, which could overflow.
type region struct {
	origin [3]int64
	size   domain.Dimensions
}

func (r region) intersects(other region) bool {
	sizes := [3]int64{r.size.Length().Ticks(), r.size.Width().Ticks(), r.size.Height().Ticks()}
	otherSizes := [3]int64{other.size.Length().Ticks(), other.size.Width().Ticks(), other.size.Height().Ticks()}
	for axis := 0; axis < 3; axis++ {
		if r.origin[axis]-other.origin[axis] >= otherSizes[axis] || other.origin[axis]-r.origin[axis] >= sizes[axis] {
			return false
		}
	}
	return true
}

// LockRegistry is the caller's extensions plus the lock constraint, appended after the
// caller's own constraints so locking never drops an application's rules. A registry rather
// than a new solver argument, because that is the extension point the engine already has and
// using it means the locked run is the ordinary run. With no locks, base itself is returned;
// a nil base is the built-ins only.
func LockRegistry(locks []PlacementLock, base *algorithm.ExtensionRegistry) *algorithm.ExtensionRegistry {
	if base == nil {
		base = algorithm.NoExtensions()
	}
	if len(locks) == 0 {
		return base
	}
	locked := lockConstraint{append([]PlacementLock(nil), locks...)}
	return algorithm.NewExtensionRegistry(append(base.PlacementConstraints(), locked), base.ItemOrderStrategies(),
		base.Solvers(), base.CandidateScorer(), base.ContainerSelector())
}

// LockedResolve is a re-solve under a lock set, beside the plan it came from, never replacing
// it.
//
// Preserved is measured against Result, not promised by the constraint. Missing names the
// locks the result does not contain, which is what a caller shows an operator whose lock
// could not be honoured.
type LockedResolve struct {
	locks     []PlacementLock
	result    map[string]interface{}
	preserved bool
	missing   []PlacementLock
}

// NewLockedResolve refuses a resolve whose verdict contradicts its own list of missing locks.
func NewLockedResolve(locks []PlacementLock, result map[string]interface{}, preserved bool,
	missing []PlacementLock) (LockedResolve, error) {
	if preserved && len(missing) > 0 {
		return LockedResolve{}, fmt.Errorf("a preserved resolve cannot be missing a lock")
	}
	if !preserved && len(missing) == 0 {
		return LockedResolve{}, fmt.Errorf("an unpreserved resolve must name the locks it could not honour")
	}
	return LockedResolve{append([]PlacementLock(nil), locks...), result, preserved,
		append([]PlacementLock(nil), missing...)}, nil
}

// Locks is the lock set the resolve was run under.
func (r LockedResolve) Locks() []PlacementLock { return append([]PlacementLock(nil), r.locks...) }

// Result is the locked solve's own wire result, a separate document from the approved one. It
// is not copied: treat it as read-only.
func (r LockedResolve) Result() map[string]interface{} { return r.result }

// Preserved is whether every lock appears in Result at its exact address.
func (r LockedResolve) Preserved() bool { return r.preserved }

// Missing lists, in lock order, the locks Result does not contain.
func (r LockedResolve) Missing() []PlacementLock { return append([]PlacementLock(nil), r.missing...) }

// ResolveWithLocks re-solves request with locks applied, through the ordinary packing path,
// and reports which locks the result honours. base is the caller's own extensions, kept; nil
// is none. A self-contradictory lock set is a *LockSetError before any solve; a malformed
// request is whatever error the decoder or the packer returns. request is not modified.
func ResolveWithLocks(request map[string]interface{}, locks []PlacementLock,
	base *algorithm.ExtensionRegistry) (LockedResolve, error) {
	locks = append([]PlacementLock(nil), locks...)
	items, err := serialization.DecodeItems(request)
	if err != nil {
		return LockedResolve{}, err
	}
	if err := refuseContradictorySet(locks, itemsByID(items)); err != nil {
		return LockedResolve{}, err
	}
	result, err := serialization.PackWithExtensions(shallowCopy(request), LockRegistry(locks, base))
	if err != nil {
		return LockedResolve{}, err
	}
	placed := placedAddresses(result)
	var missing []PlacementLock
	for _, lock := range locks {
		if !placed[address{lock.containerIndex, lock.itemType, string(lock.orientation), lock.position}] {
			missing = append(missing, lock)
		}
	}
	return LockedResolve{locks, result, len(missing) == 0, missing}, nil
}

// itemsByID keys the request's item types by id; a repeated id keeps the later entry, as the
// reference's dict does.
func itemsByID(items []*domain.Item) map[string]*domain.Item {
	byID := make(map[string]*domain.Item, len(items))
	for _, item := range items {
		byID[item.ID()] = item
	}
	return byID
}

// refuseContradictorySet is every way a lock set can be wrong without a request being
// consulted. O(L^2) over a set an operator typed, against a solve that is the expensive thing
// here. Locks in different containers describe different boxes and are never compared.
func refuseContradictorySet(locks []PlacementLock, items map[string]*domain.Item) error {
	boxes := make([]region, len(locks))
	for index, lock := range locks {
		item := items[lock.itemType]
		if item == nil {
			return lockSetError("the request has no item type %s to lock", pyrepr.String(lock.itemType))
		}
		boxes[index] = reservedBox(lock, item)
	}
	for index, lock := range locks {
		for offset, other := range locks[index+1:] {
			if lock.containerIndex != other.containerIndex {
				continue
			}
			if lock.orientation == other.orientation && lock.position == other.position && lock.itemType == other.itemType {
				return lockSetError("the same placement is locked twice: %s", lock.Slot())
			}
			if boxes[index].intersects(boxes[index+1+offset]) {
				return lockSetError("two locks claim overlapping space in container %d: %s at %s@%s and %s at %s@%s",
					lock.containerIndex, lock.itemType, lock.orientation, lock.positionText(),
					other.itemType, other.orientation, other.positionText())
			}
		}
	}
	return nil
}

// address is a placement keyed the way a lock addresses one.
type address struct {
	containerIndex int
	itemType       string
	orientation    string
	position       [3]int64
}

// placedAddresses is every placement in the result. The container index is the container's
// position in the emitted list, the order the execution plan indexes; a placement without
// readable integer ticks is skipped, as the reference skips it.
func placedAddresses(result map[string]interface{}) map[address]bool {
	placed := map[address]bool{}
	for index, entry := range listOf(result["containers"]) {
		container, _ := entry.(map[string]interface{})
		for _, raw := range listOf(container["placements"]) {
			placement, _ := raw.(map[string]interface{})
			position, ok := ticksOf(placement["position"])
			if !ok {
				continue
			}
			placed[address{index, fmt.Sprint(placement["item_type"]), fmt.Sprint(placement["orientation"]), position}] = true
		}
	}
	return placed
}

func ticksOf(value interface{}) ([3]int64, bool) {
	axes, _ := value.(map[string]interface{})
	var position [3]int64
	for index, name := range []string{"x", "y", "z"} {
		scalar, _ := axes[name].(map[string]interface{})
		ticks, ok := wholeNumber(scalar["ticks"])
		if !ok {
			return position, false
		}
		position[index] = ticks
	}
	return position, true
}

// wholeNumber reads an integer as a built plan, a result from serialization.Pack or a
// document decoded by encoding/json carries one: int, int64, or a float64 with no fraction.
func wholeNumber(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed == math.Trunc(typed) && math.Abs(typed) < 1<<63 {
			return int64(typed), true
		}
	}
	return 0, false
}

func listOf(value interface{}) []interface{} {
	list, _ := value.([]interface{})
	return list
}

// shallowCopy is the reference's dict(request): the solve gets its own top-level map, so
// nothing it does can reach the caller's.
func shallowCopy(request map[string]interface{}) map[string]interface{} {
	duplicate := make(map[string]interface{}, len(request))
	for key, value := range request {
		duplicate[key] = value
	}
	return duplicate
}
