package serialization

import (
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/internal/pointshape"
	"github.com/packvium/packvium-go/packer"
)

// Every malformed request of the shared request-errors corpus is refused on the object path
// with the refusal the JSON path gives, after the reference's
// tests/test_request_objects_corpus.py. Each case is built as objects with the decoder's own
// builders, but without the JSON rules, and passed to Packer.PackWithFixedPlacements. A
// refusal while an object is built must be a *domain.ObjectRefusal with the JSON path's code
// and reason; a refusal from the packer must carry its field and message too. A case that has
// no object form, or whose difference is kept on purpose, is listed below with why; a listed
// case that starts matching fails, so it has to leave the list.

const corpusPath = "conformance/request_errors/corpus.json"

const (
	unknownKey       = "an object has no place for a key its type does not declare"
	coerced          = "the decoder coerces it, so the objects it builds are a different, valid request"
	constructorFirst = "two faults: a constructor refuses its own value as the object is built, " +
		"before the packer walks the request in the JSON order"
	negativePosition = "a Point cannot hold a negative coordinate; the measure it is read with refuses " +
		"it as negative_measure, where the JSON path names the placement"
	asLetters   = "the decoder reads text as the sequence of its letters, so the objects hold other values"
	outputUnits = "the output units are arguments of the encoder, not part of the objects packed"
)

// notObjects are the cases with no object form: the value cannot be held, or the decoder
// changes it. The reference's list, with the reasons worded for Go's types.
var notObjects = map[string]string{
	"request-not-object":                                   "a request that is not an object has no objects",
	"items-missing":                                        "the packer takes the items as an argument",
	"items-not-list":                                       "the packer takes the items as a slice of *Item",
	"item-not-object":                                      "an item that is not an object is not an Item",
	"containers-missing":                                   "the packer takes the containers as an argument",
	"configuration-not-object":                             "a configuration that is not an object is not a PackingConfig",
	"effort-not-object":                                    "an effort budget that is not an object is not an EffortBudget",
	"container-made-to-measure-not-object":                 "a machine that is not an object is not a MadeToMeasure",
	"container-deformable-boundary-not-object":             "the object holds the bag's kind, not a block",
	"container-deformable-boundary-kind-missing":           "the object holds the bag's kind, not a block",
	"item-id-missing":                                      "an Item requires an id",
	"item-dimensions-missing":                              "an Item requires dimensions",
	"item-side-missing":                                    "Dimensions requires all three sides",
	"container-made-to-measure-minimum-missing":            "a MadeToMeasure requires a minimum",
	"container-made-to-measure-increment-missing":          "a MadeToMeasure requires an increment",
	"rate-brackets-not-list":                               "a RateTable holds its brackets as a slice",
	"solver-profile-unknown":                               "SolverProfile has no such value",
	"configuration-key-unknown-before-bad-value":           "SolverProfile has no such value, and " + unknownKey,
	"configuration-key-unknown":                            unknownKey,
	"configuration-key-unknown-null":                       unknownKey,
	"configuration-key-unknown-escaped":                    unknownKey,
	"configuration-keys-unknown-first-in-code-point-order": unknownKey,
	"effort-key-unknown":                                   unknownKey,
	"container-deformable-boundary-key-unknown":            unknownKey,
	"container-made-to-measure-key-unknown":                unknownKey,
	"unknown-configuration-key-before-its-values":          unknownKey,
	"unsupported-before-units":                             "an unsupported field has no object form",
	"unsupported-before-an-item":                           "an unsupported field has no object form",
	"container-flat-bag-with-access-directions":            "an empty slice is how an object leaves the field out",
	"item-id-number":                                       coerced,
	"item-quantity-text":                                   coerced,
	"item-accepts-stacking-groups-not-list":                coerced,
	"container-access-directions-not-list":                 coerced,
	"fixed-position-unparsable":                            "a Point holds integer ticks, not text",
	"fixed_placements.position.x=\"-1\"":                   negativePosition,
	"fixed_placements.position.y=\"-1\"":                   negativePosition,
	"fixed_placements.position.z=\"-1\"":                   negativePosition,
	"fixed_placements.position.x=-1":                       negativePosition,
	"fixed_placements.position.y=-1":                       negativePosition,
	"fixed_placements.position.z=-1":                       negativePosition,
	"item-allowed-rotations-not-list":                      asLetters,
	"item-allowed-rotation-unknown":                        "Rotation has no such value",
	"item-allowed-rotation-lowercase":                      "Rotation has no such value",
	"item-shape-type-unknown":                              "ShapeType has no such value",
	"fixed-orientation-unknown":                            "Rotation has no such value",
	"configuration-solvers-not-list":                       asLetters,
	"output-not-object":                                    outputUnits,
	"output-length-unit-unknown":                           outputUnits,
	"output-weight-unit-unknown":                           outputUnits,
	"output-before-configuration":                          outputUnits,
}

// typedOnly are the cases a typed object cannot hold at all, beyond the reference's list: Go
// holds a stacking group name as a string, and the decoder spells a number as its text.
var typedOnly = map[string]string{
	"item-stacking-group-number":                "a Go Item holds its stacking group as a string; " + coerced,
	"item-accepts-stacking-groups-entry-number": "a Go Item holds accepted groups as strings; " + coerced,
}

// differentOnPurpose are the cases the object path answers differently by design.
var differentOnPurpose = map[string]string{
	"configuration-objective-unknown":          "an unknown objective keeps UnknownObjectiveError",
	"configuration-solver-unknown":             "an unknown solver keeps UnknownSolverError",
	"container-access-direction-unknown":       "an unknown direction keeps its InvalidDirectionError",
	"item-accepts-stacking-groups-entry-empty": "a set's index counts in code point order",
	"an-item-id-before-its-quantity":           constructorFirst,
	"item-ids-before-containers":               constructorFirst,
	"container-ids-before-fixed-placements":    constructorFirst,
}

// skipped is pinned, so the skip set cannot grow without this line changing too: the
// reference's 57 and the two typedOnly.
const skipped = 59

type corpusCase struct {
	name    string
	request interface{}
}

func requestErrorCorpus(t *testing.T) []corpusCase {
	t.Helper()
	ref := os.Getenv("PACKVIUM_CONFORMANCE_REF")
	if ref == "" {
		ref = "v1.7.0"
	}
	content, err := exec.Command("git", "show", ref+":"+corpusPath).Output()
	if err != nil {
		t.Skipf("%s unavailable at %s: %v", corpusPath, ref, err)
	}
	document, err := jsonvalue.DecodeBytes(content)
	if err != nil {
		t.Fatal(err)
	}
	var cases []corpusCase
	for _, raw := range document.(map[string]interface{})["cases"].([]interface{}) {
		entry := raw.(map[string]interface{})
		cases = append(cases, corpusCase{entry["name"].(string), entry["request"]})
	}
	return cases
}

// objectOutcome is the object path's answer: the error and whether a constructor raised it.
type objectOutcome struct {
	err         error
	constructed bool
}

func objectRefusal(request interface{}) (outcome objectOutcome) {
	// The decoder's builders trust the JSON rules to have run; a value they cannot read at
	// all is a mistyped object, which no constructor refuses with a reason.
	defer func() {
		if recover() != nil {
			outcome = objectOutcome{errNoObjects, true}
		}
	}()
	fields, ok := request.(map[string]interface{})
	if !ok {
		return objectOutcome{errNoObjects, true}
	}
	data := object(fields)
	unitsBlock, _ := child(data, "units")
	unit := unitsBlock.text("length", "mm")
	configuration, err := child(data, "configuration")
	if err != nil {
		return objectOutcome{err, true}
	}
	cfg, err := decodeConfig(configuration, unit)
	if err != nil {
		return objectOutcome{err, true}
	}
	items, err := buildAll(data, "items", func(entry object) (interface{}, error) { return decodeItem(entry, unit) })
	if err != nil {
		return objectOutcome{err, true}
	}
	containers, err := buildAll(data, "containers", func(entry object) (interface{}, error) {
		return decodeContainer(entry, unit)
	})
	if err != nil {
		return objectOutcome{err, true}
	}
	fixed, err := fixedPlacementObjects(data, unit)
	if err != nil {
		return objectOutcome{err, true}
	}
	typedItems := make([]*domain.Item, len(items))
	for index, item := range items {
		typedItems[index] = item.(*domain.Item)
	}
	typedContainers := make([]*domain.Container, len(containers))
	for index, container := range containers {
		typedContainers[index] = container.(*domain.Container)
	}
	_, err = packer.New(cfg, nil).PackWithFixedPlacements(typedItems, typedContainers, fixed)
	return objectOutcome{err, false}
}

var errNoObjects = &domain.InvalidRequestError{Code: "none", Detail: "no objects"}

func buildAll(data object, key string, build func(object) (interface{}, error)) ([]interface{}, error) {
	entries, err := decodeAll(data, key)
	if err != nil {
		return nil, err
	}
	built := make([]interface{}, len(entries))
	for index, entry := range entries {
		if built[index], err = build(entry); err != nil {
			return nil, err
		}
	}
	return built, nil
}

// fixedPlacementObjects is the decoder's fixed-placement builder without the JSON shape rules
// in front of it, as the reference's test builds them.
func fixedPlacementObjects(data object, unit string) ([]domain.FixedPlacement, error) {
	raw, _ := data.get("fixed_placements")
	list, _ := raw.([]interface{})
	var placements []domain.FixedPlacement
	for _, value := range list {
		entry, ok := value.(map[string]interface{})
		if !ok {
			return nil, errNoObjects
		}
		itemType, isText := entry["item_type"].(string)
		containerType, alsoText := entry["container_type"].(string)
		orientation, codeText := entry["orientation"].(string)
		if !isText || !alsoText || !codeText {
			return nil, errNoObjects
		}
		point, _ := positionOf(entry).(map[string]interface{})
		var ticks [3]int64
		for index, axis := range pointshape.Axes {
			if coordinate, present := point[axis]; present {
				length, err := parseLength(coordinate, unit)
				if err != nil {
					return nil, err
				}
				ticks[index] = length.Ticks()
			}
		}
		position, err := domain.NewPoint(ticks[0], ticks[1], ticks[2])
		if err != nil {
			return nil, err
		}
		rotation, err := domain.ParseRotation(orientation)
		if err != nil {
			return nil, err
		}
		instance := int64(1)
		if given, present := entry["container_instance"]; present {
			if instance, ok = given.(int64); !ok {
				return nil, errNoObjects
			}
		}
		placement, _ := domain.NewFixedPlacement(itemType, containerType, position, rotation, instance)
		placements = append(placements, placement)
	}
	return placements, nil
}

// sameRefusal compares the two paths: a constructor's refusal by code and reason, a
// packer's by code, reason, field and message.
func sameRefusal(fromJSON error, fromObjects objectOutcome) bool {
	want, ok := fromJSON.(*domain.InvalidRequestError)
	if !ok {
		return false
	}
	if fromObjects.constructed {
		refused, ok := fromObjects.err.(*domain.ObjectRefusal)
		return ok && want.Code == domain.InvalidRequestCode && refused.Reason == want.Reason
	}
	got, ok := fromObjects.err.(*domain.InvalidRequestError)
	return ok && *got == *want
}

func describe(outcome objectOutcome) string {
	stage := "pack"
	if outcome.constructed {
		stage = "constructor"
	}
	if outcome.err == nil {
		return stage + ": accepted"
	}
	if refused, ok := outcome.err.(*domain.ObjectRefusal); ok {
		return stage + ": ObjectRefusal(" + refused.Reason + ", " + refused.Detail + ")"
	}
	return stage + ": " + outcome.err.Error()
}

func TestTheSkipSetIsPinnedAndNamesOnlyCorpusCases(t *testing.T) {
	cases := requestErrorCorpus(t)
	names := map[string]bool{}
	for _, entry := range cases {
		names[entry.name] = true
	}
	for name := range notObjects {
		if differentOnPurpose[name] != "" {
			t.Errorf("%s is listed twice", name)
		}
	}
	var unknown []string
	for _, list := range []map[string]string{notObjects, differentOnPurpose, typedOnly} {
		for name := range list {
			if !names[name] {
				unknown = append(unknown, name)
			}
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		t.Errorf("not corpus cases: %s", strings.Join(unknown, ", "))
	}
	for name := range typedOnly {
		if notObjects[name] != "" || differentOnPurpose[name] != "" {
			t.Errorf("%s is listed twice", name)
		}
	}
	if got := len(notObjects) + len(differentOnPurpose) + len(typedOnly); got != skipped {
		t.Errorf("%d cases skipped, %d pinned", got, skipped)
	}
}

func TestTheObjectPathRefusesAsTheJSONPathDoes(t *testing.T) {
	covered, stillDiffer := 0, 0
	for _, entry := range requestErrorCorpus(t) {
		_, fromJSON := Pack(entry.request)
		fromObjects := objectRefusal(entry.request)
		same := sameRefusal(fromJSON, fromObjects)
		if notObjects[entry.name] != "" || differentOnPurpose[entry.name] != "" || typedOnly[entry.name] != "" {
			if same {
				t.Errorf("%s: skipped, but the two paths now agree; take it off its list", entry.name)
			}
			stillDiffer++
			continue
		}
		if !same {
			t.Errorf("%s: json: %v; objects: %s", entry.name, fromJSON, describe(fromObjects))
			continue
		}
		covered++
	}
	t.Logf("request errors on the object path: %d refused as the JSON path refuses them; %d skipped, each still different", covered, stillDiffer)
}
