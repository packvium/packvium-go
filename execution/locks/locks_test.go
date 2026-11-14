package locks

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/execution"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/serialization"
)

// The suite is written against the failure modes rather than the happy path: a lock must not
// mutate the approved plan, must not place anything the engine would refuse, must answer an
// unsatisfiable lock with a result rather than an error, must have its preservation measured,
// and must not cost the load anything it did not ask for. The last one is not in the design
// document; two earlier readings of a lock failed it, each by emptying a full container.

// Millimetres in the fixed-point ticks the schema's exactScalar carries.
const (
	mm    = 16000
	mm100 = 100 * mm
)

// eightCubesJSON is the corpus's exact-fit fixture: eight 100 mm cubes that exactly fill a
// 200 mm box, the simplest tiling. It is inlined so the suite runs wherever the module does.
const eightCubesJSON = `{
  "configuration": {"solver_profile": "fast", "time_limit_ms": 300000, "alternatives": 1,
    "effort_budget": {"max_candidates_evaluated": 1000000, "max_placement_attempts": 1000000, "max_search_nodes": 1000000}},
  "items": [{"id": "cube", "quantity": 8, "dimensions": {"length": "100", "width": "100", "height": "100"}}],
  "containers": [{"id": "box", "inner_dimensions": {"length": "200", "width": "200", "height": "200"}}]
}`

func decode(t *testing.T, text string) map[string]interface{} {
	t.Helper()
	value, err := jsonvalue.DecodeBytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]interface{})
}

func eightCubes(t *testing.T) map[string]interface{} { return decode(t, eightCubesJSON) }

func lock(t *testing.T, containerIndex int, itemType, orientation string, x, y, z int64) PlacementLock {
	t.Helper()
	made, err := NewPlacementLock(containerIndex, itemType, orientation, [3]int64{x, y, z})
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func pack(t *testing.T, request map[string]interface{}) map[string]interface{} {
	t.Helper()
	result, err := serialization.Pack(request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func resolve(t *testing.T, request map[string]interface{}, locks ...PlacementLock) LockedResolve {
	t.Helper()
	resolved, err := ResolveWithLocks(request, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func placed(result map[string]interface{}) map[address]bool { return placedAddresses(result) }

func count(result map[string]interface{}) int {
	total := 0
	for _, container := range result["containers"].([]interface{}) {
		total += len(container.(map[string]interface{})["placements"].([]interface{}))
	}
	return total
}

func plan(t *testing.T, request, result map[string]interface{}) map[string]interface{} {
	t.Helper()
	built, err := execution.BuildPlan(request, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	return built
}

// ----------------------------------------------------- a lock binds one instance

// The regression the suite exists for: read as a rule about the item type, "keep this cube
// here" becomes "no cube may be anywhere else", which drops seven items from a feasible load.
func TestLockingOneCubeLeavesTheOtherSevenFree(t *testing.T) {
	request := eightCubes(t)
	if count(pack(t, request)) != 8 {
		t.Fatal("the fixture no longer tiles; the rest of this test is void")
	}
	resolved := resolve(t, request, lock(t, 0, "cube", "LWH", 0, 0, 0))
	if !resolved.Preserved() || count(resolved.Result()) != 8 {
		t.Fatalf("preserved=%v, %d placed", resolved.Preserved(), count(resolved.Result()))
	}
}

// The far corner is the second regression: an empty container offers only the origin as a
// candidate point, so a lock that had to be filled first could never be reached.
func TestTheLockedSlotIsTheOneTheOperatorNamed(t *testing.T) {
	for _, position := range [][3]int64{{0, 0, 0}, {mm100, 0, 0}, {mm100, mm100, mm100}} {
		request := eightCubes(t)
		resolved := resolve(t, request, lock(t, 0, "cube", "LWH", position[0], position[1], position[2]))
		if !resolved.Preserved() {
			t.Errorf("%v: not preserved", position)
		}
		if !placed(resolved.Result())[address{0, "cube", "LWH", position}] {
			t.Errorf("%v: the locked slot is not in the result", position)
		}
		if count(resolved.Result()) != 8 {
			t.Errorf("%v: the lock cost the load items it did not ask for", position)
		}
	}
}

func TestTwoLocksOnOneTypeAreBothHonoured(t *testing.T) {
	resolved := resolve(t, eightCubes(t), lock(t, 0, "cube", "LWH", 0, 0, 0), lock(t, 0, "cube", "LWH", mm100, mm100, 0))
	if !resolved.Preserved() || count(resolved.Result()) != 8 {
		t.Fatalf("missing %v", resolved.Missing())
	}
}

// ---------------------------------------------------- the approved plan survives

func TestTheRequestIsNotMutated(t *testing.T) {
	request, before := eightCubes(t), eightCubes(t)
	resolve(t, request, lock(t, 0, "cube", "LWH", 0, 0, 0))
	if !reflect.DeepEqual(request, before) {
		t.Fatal("the request was mutated")
	}
}

// Property 1: the lock produces a candidate beside the approved plan, never over it.
func TestTheOriginalResultIsASeparateArtifact(t *testing.T) {
	request := eightCubes(t)
	approved := pack(t, request)
	snapshot := pack(t, request)
	resolved := resolve(t, request, lock(t, 0, "cube", "LWH", 0, 0, 0))
	if !reflect.DeepEqual(masked(approved), masked(snapshot)) {
		t.Fatal("the approved result changed")
	}
	if reflect.ValueOf(resolved.Result()).Pointer() == reflect.ValueOf(approved).Pointer() {
		t.Fatal("the locked result is the approved one")
	}
}

// masked drops the wall clock, the one field two identical solves may disagree on.
func masked(result map[string]interface{}) map[string]interface{} {
	copied := shallowCopy(result)
	delete(copied, "algorithm")
	return copied
}

// ------------------------------- a self-contradictory set is refused before any solve

func lockSetRefusal(t *testing.T, request map[string]interface{}, locks []PlacementLock, fragment string) {
	t.Helper()
	_, err := ResolveWithLocks(request, locks, nil)
	var refusal *LockSetError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("got %v, want a LockSetError mentioning %q", err, fragment)
	}
}

// Two overlapping boxes cannot both hold under any request; measured before the guard
// existed, the two reservations blocked every cell of a lattice that had packed eight cubes.
func TestTwoLocksClaimingTheSameSpaceAreRefusedWithoutSolving(t *testing.T) {
	lockSetRefusal(t, eightCubes(t), []PlacementLock{lock(t, 0, "cube", "LWH", 0, 0, 0), lock(t, 0, "cube", "LWH", 50*mm, 0, 0)},
		"two locks claim overlapping space in container 0: cube at LWH@(0, 0, 0) and cube at LWH@(800000, 0, 0)")
}

func TestTheSamePlacementLockedTwiceIsRefused(t *testing.T) {
	twice := lock(t, 0, "cube", "LWH", 0, 0, 0)
	lockSetRefusal(t, eightCubes(t), []PlacementLock{twice, twice}, "the same placement is locked twice: ('LWH', (0, 0, 0))")
}

// An operator naming an item that is not in the shipment is a typo, not an infeasibility.
func TestALockOnAnItemTheRequestDoesNotContainIsRefused(t *testing.T) {
	lockSetRefusal(t, eightCubes(t), []PlacementLock{lock(t, 0, "wedge", "LWH", 0, 0, 0)}, "no item type 'wedge'")
}

func TestLocksInDifferentContainersNeverContradictEachOther(t *testing.T) {
	request := eightCubes(t)
	request["containers"].([]interface{})[0].(map[string]interface{})["quantity"] = int64(2)
	if _, err := ResolveWithLocks(request, []PlacementLock{lock(t, 0, "cube", "LWH", 0, 0, 0), lock(t, 1, "cube", "LWH", 0, 0, 0)}, nil); err != nil {
		t.Fatal(err)
	}
}

// ------------------------------------------- an unsatisfiable lock is an answer

// Property 3: the reserved box straddles the centre of an exact tiling, so it meets every
// cell and no cube has anywhere legal to go; the ordinary search reports it.
func TestALockTheSolverCannotSatisfyReturnsAResultAndNotAnError(t *testing.T) {
	offGrid := [3]int64{50 * mm, 50 * mm, 50 * mm}
	resolved := resolve(t, eightCubes(t), lock(t, 0, "cube", "LWH", offGrid[0], offGrid[1], offGrid[2]))
	if resolved.Preserved() || resolved.Missing()[0].PositionTicks() != offGrid {
		t.Fatalf("preserved=%v missing=%v", resolved.Preserved(), resolved.Missing())
	}
	result := resolved.Result()
	if status := result["status"]; status != "best_found" && status != "infeasible" {
		t.Fatalf("status %v", status)
	}
	unpacked, _ := result["unpacked_items"].([]interface{})
	if len(unpacked) == 0 {
		t.Fatal("an unhonoured lock must leave a trail")
	}
	level := unpacked[0].(map[string]interface{})["proof"].(map[string]interface{})["level"]
	switch level {
	case "proven", "observed", "inferred", "unknown_due_to_limit":
	default:
		t.Fatalf("proof level %v", level)
	}
}

func TestAnUnhonouredLockIsNeverSoftenedIntoASuccess(t *testing.T) {
	resolved := resolve(t, eightCubes(t), lock(t, 0, "cube", "LWH", 50*mm, 50*mm, 50*mm))
	_, err := NewLockedResolve(resolved.Locks(), resolved.Result(), true, resolved.Missing())
	if err == nil || !strings.Contains(err.Error(), "cannot be missing a lock") {
		t.Fatalf("got %v", err)
	}
}

func TestAnUnpreservedResolveMustSayWhichLockFailed(t *testing.T) {
	_, err := NewLockedResolve(nil, map[string]interface{}{}, false, nil)
	if err == nil || !strings.Contains(err.Error(), "must name the locks") {
		t.Fatalf("got %v", err)
	}
}

// ------------------------------------- a lock outside the container is inert, not fatal

// A lock is offered to every container in the solve. Without the reach test, a lock whose box
// does not fit here would refuse every candidate and empty the container; it is reported
// missing instead and the load is untouched.
func TestALockThatCannotApplyToThisContainerDoesNotEmptyIt(t *testing.T) {
	resolved := resolve(t, eightCubes(t), lock(t, 0, "cube", "LWH", 2*mm100, 0, 0))
	if resolved.Preserved() || count(resolved.Result()) != 8 || resolved.Result()["status"] != "feasible" {
		t.Fatalf("preserved=%v, %d placed, %v", resolved.Preserved(), count(resolved.Result()), resolved.Result()["status"])
	}
}

// -------------------------------------------------------- the constraint itself

func cubeAndBox(t *testing.T) (*domain.Item, *domain.Container) {
	t.Helper()
	dimensions := domain.MustDimensions(mm100, mm100, mm100)
	cube, err := domain.NewItem(domain.NewItemSpec("cube", dimensions))
	if err != nil {
		t.Fatal(err)
	}
	box, err := domain.NewContainer(domain.ContainerSpec{ID: "box", InnerDimensions: domain.MustDimensions(2*mm100, 2*mm100, 2*mm100)})
	if err != nil {
		t.Fatal(err)
	}
	return cube, box
}

func context(t *testing.T, x, y, z int64, rotation domain.Rotation, placements ...domain.Placement) constraint.Context {
	t.Helper()
	cube, box := cubeAndBox(t)
	dimensions := cube.Dimensions().Rotated(rotation)
	return constraint.NewContext(box, placements, cube.Instances()[0], domain.MustPoint(x, y, z), rotation, dimensions, dimensions)
}

func atOrigin(t *testing.T) lockConstraint {
	return lockConstraint{[]PlacementLock{lock(t, 0, "cube", "LWH", 0, 0, 0)}}
}

// Property 4, as the cheapest possible test of it: a chain is an AND, so allowing says
// nothing and cannot overrule a refusal. Whatever this decides, it is never why a placement
// exists.
func TestTheConstraintCanOnlyEverRefuse(t *testing.T) {
	verdicts := map[bool]bool{}
	for _, x := range []int64{50 * mm, mm100} {
		verdicts[atOrigin(t).Evaluate(context(t, x, 0, 0, domain.LWH)).Allowed()] = true
	}
	if !verdicts[true] || !verdicts[false] {
		t.Fatalf("verdicts %v", verdicts)
	}
}

// What leaves the seven unlocked cubes free: a reservation is about one box.
func TestACandidateThatMissesTheReservedVolumeIsUntouched(t *testing.T) {
	for _, position := range [][3]int64{{mm100, 0, 0}, {0, mm100, 0}, {mm100, mm100, mm100}} {
		if !atOrigin(t).Evaluate(context(t, position[0], position[1], position[2], domain.LWH)).Allowed() {
			t.Errorf("%v refused", position)
		}
	}
}

func TestACandidateOverlappingTheReservedVolumeIsRefused(t *testing.T) {
	if atOrigin(t).Evaluate(context(t, 50*mm, 0, 0, domain.LWH)).Allowed() {
		t.Fatal("allowed")
	}
}

func TestTheCandidateThatIsTheLockIsAdmitted(t *testing.T) {
	if !atOrigin(t).Evaluate(context(t, 0, 0, 0, domain.LWH)).Allowed() {
		t.Fatal("refused")
	}
}

func TestTheRefusalNamesTheLockAndCarriesTheOperatorCode(t *testing.T) {
	verdict := atOrigin(t).Evaluate(context(t, 50*mm, 0, 0, domain.LWH))
	if verdict.Allowed() || verdict.Code() != LockViolated || !strings.Contains(verdict.Detail(), "LWH@(0, 0, 0)") {
		t.Fatalf("%v %q %q", verdict.Allowed(), verdict.Code(), verdict.Detail())
	}
	if verdict.Detail() != "cube is locked to LWH@(0, 0, 0)" {
		t.Fatalf("detail %q", verdict.Detail())
	}
}

func TestAnUnlockedItemTypeIsUntouched(t *testing.T) {
	wedge := lockConstraint{[]PlacementLock{lock(t, 0, "wedge", "LWH", 0, 0, 0)}}
	if !wedge.Evaluate(context(t, 0, 0, 0, domain.LWH)).Allowed() {
		t.Fatal("refused")
	}
}

// The reservation lasts exactly as long as the lock is outstanding; once the container holds
// the locked slot the ordinary overlap rule defends it, so a lock set does not accumulate cost
// as the container fills.
func TestASatisfiedLockStopsReserving(t *testing.T) {
	if atOrigin(t).Evaluate(context(t, 50*mm, 0, 0, domain.LWH)).Allowed() {
		t.Fatal("an outstanding lock allowed an overlapping candidate")
	}
	cube, _ := cubeAndBox(t)
	occupied := domain.NewPlacement(cube.Instances()[0], domain.MustPoint(0, 0, 0), domain.LWH, cube.Dimensions(),
		domain.MustPoint(0, 0, 0), cube.Dimensions(), 1)
	if !atOrigin(t).Evaluate(context(t, 50*mm, 0, 0, domain.LWH, occupied)).Allowed() {
		t.Fatal("a satisfied lock still reserves")
	}
}

// Same origin, different rotation: not the locked placement, and it takes the reserved
// volume, so it is refused rather than accepted as close enough.
func TestTheOrientationIsPartOfTheLockAndNotDecoration(t *testing.T) {
	if atOrigin(t).Evaluate(context(t, 0, 0, 0, domain.WLH)).Allowed() {
		t.Fatal("allowed")
	}
}

// ------------------------------------------------------------ addressing a lock

// The reference also refuses a position without three axes or with non-integer ticks; a
// [3]int64 cannot express either.
func TestAnAddressTheEngineCouldNotHaveEmittedIsRefused(t *testing.T) {
	cases := []struct {
		containerIndex        int
		itemType, orientation string
		position              [3]int64
		message               string
	}{
		{-1, "cube", "LWH", [3]int64{}, "a lock must name a container index"},
		{0, "", "LWH", [3]int64{}, "a lock must name an item type"},
		{0, "cube", "sideways", [3]int64{}, "'sideways' is not an orientation this engine emits"},
		{0, "cube", "LWH", [3]int64{0, 0, -1}, "a lock's position cannot be negative"},
	}
	for _, c := range cases {
		_, err := NewPlacementLock(c.containerIndex, c.itemType, c.orientation, c.position)
		if err == nil || err.Error() != c.message {
			t.Errorf("got %v, want %q", err, c.message)
		}
	}
}

func slots(locks []PlacementLock) map[string]bool {
	set := map[string]bool{}
	for _, each := range locks {
		set[each.Slot()] = true
	}
	return set
}

// The plan is where an operator sees a placement, so it is where they point at one.
func TestALockIsReadFromThePlansOwnReference(t *testing.T) {
	request := eightCubes(t)
	result := pack(t, request)
	locks, err := LocksFromPlan(plan(t, request, result), 0, []string{"cube"})
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 8 {
		t.Fatalf("%d locks", len(locks))
	}
	expected := map[string]bool{}
	for _, raw := range result["containers"].([]interface{})[0].(map[string]interface{})["placements"].([]interface{}) {
		placement := raw.(map[string]interface{})
		position, _ := ticksOf(placement["position"])
		made := lock(t, 0, "cube", placement["orientation"].(string), position[0], position[1], position[2])
		expected[made.Slot()] = true
	}
	if !reflect.DeepEqual(slots(locks), expected) {
		t.Fatalf("got %v, want %v", slots(locks), expected)
	}
}

// Every placement locked is the strongest form of the first property: the load the operator
// approved is the load that comes back.
func TestLockingAWholeContainerReproducesIt(t *testing.T) {
	request := eightCubes(t)
	approved := pack(t, request)
	locks, err := LocksFromPlan(plan(t, request, approved), 0, []string{"cube"})
	if err != nil {
		t.Fatal(err)
	}
	resolved := resolve(t, request, locks...)
	if !resolved.Preserved() || !reflect.DeepEqual(placed(resolved.Result()), placed(approved)) {
		t.Fatalf("missing %v", resolved.Missing())
	}
}

func TestALockReadFromAnotherContainerIndexIsNotRead(t *testing.T) {
	request := eightCubes(t)
	locks, err := LocksFromPlan(plan(t, request, pack(t, request)), 1, []string{"cube"})
	if err != nil || len(locks) != 0 {
		t.Fatalf("%v %v", locks, err)
	}
}

func TestOnlyTheNamedItemTypesBecomeLocks(t *testing.T) {
	request := eightCubes(t)
	locks, err := LocksFromPlan(plan(t, request, pack(t, request)), 0, []string{"wedge"})
	if err != nil || len(locks) != 0 {
		t.Fatalf("%v %v", locks, err)
	}
}

// ---------------------------------------------------------------- the registry

func TestAnEmptyLockSetReturnsTheCallersRegistryUnchanged(t *testing.T) {
	base := algorithm.NoExtensions()
	if LockRegistry(nil, base) != base {
		t.Fatal("a new registry was built for no locks")
	}
}

type refuses struct{}

func (refuses) Evaluate(constraint.Context) constraint.Result {
	panic("not evaluated in this test")
}

// An application that locks a placement must not lose its own rules by doing so.
func TestLockingDoesNotDropTheCallersOwnConstraints(t *testing.T) {
	mine := refuses{}
	registry := LockRegistry([]PlacementLock{lock(t, 0, "cube", "LWH", 0, 0, 0)},
		algorithm.ExtensionsWithConstraints([]constraint.PlacementConstraint{mine}))
	constraints := registry.PlacementConstraints()
	if len(constraints) != 2 || constraints[0] != mine {
		t.Fatalf("constraints %v", constraints)
	}
	if _, ok := constraints[1].(lockConstraint); !ok {
		t.Fatalf("second constraint is %T", constraints[1])
	}
}

// segregated carries a policy evaluated at a fixed instant: reading a clock instead would
// make the same request pack differently on different days.
const segregated = `{
  "configuration": {"solver_profile": "fast", "time_limit_ms": 300000},
  "policy": {"as_of": 1704067200000, "rules": [{"id": "hazmat-food-segregation", "version": 1,
    "effective_at": 1704067200000, "priority": 100, "separate_tags": {"tag": "hazmat", "from_tag": "food"}}]},
  "items": [
    {"id": "drum", "quantity": 1, "tags": ["hazmat"], "dimensions": {"length": "100", "width": "100", "height": "100"}},
    {"id": "crate", "quantity": 1, "tags": ["food"], "dimensions": {"length": "100", "width": "100", "height": "100"}}
  ],
  "containers": [{"id": "box", "quantity": 2, "inner_dimensions": {"length": "200", "width": "200", "height": "200"}}]
}`

// The request's policy compiles into placement constraints, and a lock arrives through the
// same door. Replacing that set instead of adding to it would make locking anything a way to
// switch a segregation rule off.
func TestLockingDoesNotSwitchOffThePolicyTheRequestCarries(t *testing.T) {
	request := decode(t, segregated)
	if len(pack(t, request)["containers"].([]interface{})) != 2 {
		t.Fatal("the rule no longer separates; the test is void")
	}
	resolved := resolve(t, request, lock(t, 0, "drum", "LWH", 0, 0, 0))
	containers := resolved.Result()["containers"].([]interface{})
	if len(containers) != 2 {
		t.Fatalf("%d containers", len(containers))
	}
	for _, container := range containers {
		types := map[string]bool{}
		for _, placement := range container.(map[string]interface{})["placements"].([]interface{}) {
			types[placement.(map[string]interface{})["item_type"].(string)] = true
		}
		if types["drum"] && types["crate"] {
			t.Fatal("the lock let the segregation rule lapse")
		}
	}
}

// ------------------------------------------------------- the dependency direction

// A solver that imported the lock layer could grow a lock-aware path, the special case
// property 4 forbids. Checked over the engine's packages' imports, not by grepping prose.
func TestTheLockLayerIsACallerOfTheEngineAndNotAPartOfIt(t *testing.T) {
	const self = "github.com/packvium/packvium-go/execution/locks"
	for _, directory := range []string{"algorithm", "constraint", "packer", "validation", "serialization", "policy"} {
		packages, err := parser.ParseDir(token.NewFileSet(), filepath.Join("..", "..", directory), func(info os.FileInfo) bool {
			return !strings.HasSuffix(info.Name(), "_test.go")
		}, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, parsed := range packages {
			for name, file := range parsed.Files {
				for _, spec := range file.Imports {
					if path, _ := strconv.Unquote(spec.Path.Value); path == self {
						t.Errorf("%s imports the lock layer", name)
					}
				}
			}
		}
	}
}

// Independent validation covers a locked solve by construction -- it is an ordinary solve --
// and this re-derives the cheapest of those guarantees from the placements alone.
func TestALockedResultIsStillAValidatedResult(t *testing.T) {
	resolved := resolve(t, eightCubes(t), lock(t, 0, "cube", "LWH", mm100, 0, 0))
	if resolved.Result()["feasibility"].(map[string]interface{})["code"] != "feasible" {
		t.Fatal("not feasible")
	}
	type box struct{ origin, size [3]int64 }
	var boxes []box
	for _, container := range resolved.Result()["containers"].([]interface{}) {
		for _, raw := range container.(map[string]interface{})["placements"].([]interface{}) {
			placement := raw.(map[string]interface{})
			origin, _ := ticksOf(placement["position"])
			dimensions := placement["dimensions"].(map[string]interface{})
			var size [3]int64
			for axis, name := range []string{"length", "width", "height"} {
				size[axis] = dimensions[name].(map[string]interface{})["ticks"].(int64)
			}
			boxes = append(boxes, box{origin, size})
		}
	}
	for index, first := range boxes {
		for _, second := range boxes[index+1:] {
			overlap := true
			for axis := 0; axis < 3; axis++ {
				overlap = overlap && first.origin[axis] < second.origin[axis]+second.size[axis] &&
					second.origin[axis] < first.origin[axis]+first.size[axis]
			}
			if overlap {
				t.Fatal("a locked solve returned overlapping placements")
			}
		}
	}
}
