// Package revisions is packvium.revisions: plan revisions, an append-only chain of exceptions
// against approved plans (docs/PLAN-REVISIONS.md).
//
// A revision records what differed from an approved plan (a missing item, a substituted
// carton, a lock, a verified placement) and derives the request the next plan solves. It is a
// pure function of its parent, the approved artifact and its events: it calls no solver,
// validator or clock, so every builder emits the same bytes. A replan is then an ordinary
// solve of the derived request; locks and verified placements become fixed_placements.
//
// Documents are decoded JSON values (internal/jsonvalue's form): nil, bool, string, int64,
// *big.Int, float64, []interface{} and map[string]interface{}. Nothing passed in is changed.
package revisions

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/packvium/packvium-go/artifacts"
	"github.com/packvium/packvium-go/internal/canonicaljson"
)

// Format is the document format this package writes and reads.
const Format = "packvium-plan-revision/v1"

// EventTypes are the events a revision can record, in the order the contract lists them.
var EventTypes = []string{"item_missing", "container_substituted", "placement_locked", "placement_verified"}

// Error is a revision that could not be built. Code is one of a closed set shared by every
// engine: invalid_revision, invalid_event, event_conflict, invalid_artifact, and the
// canonical-form codes number_out_of_range, invalid_string and invalid_value. A message
// quotes a value by its canonical JSON, so every engine writes the same text.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func refuse(code, format string, arguments ...interface{}) *Error {
	return &Error{code, fmt.Sprintf(format, arguments...)}
}

// Issue is one way a chain fails its audit, anchored on the revision where it shows.
type Issue struct {
	Code     string
	Revision int64
	Detail   string
}

// CanonicalJSON is the RFC 8785 canonical form, the bytes a digest and every engine are
// compared on.
func CanonicalJSON(document interface{}) (string, error) {
	text, err := canonicaljson.Marshal(document)
	if refusal, ok := err.(*canonicaljson.Error); ok {
		return "", &Error{refusal.Code, refusal.Message}
	}
	return text, err
}

// DocumentDigest is "sha256:" and the hex SHA-256 of a document's canonical bytes.
func DocumentDigest(document interface{}) (string, error) {
	text, err := CanonicalJSON(document)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// RootRevision is revision 0: the request as first approved, with nothing recorded against it.
func RootRevision(request interface{}) (map[string]interface{}, error) {
	fields, ok := request.(map[string]interface{})
	if !ok {
		return nil, refuse("invalid_revision", "a request is a JSON object")
	}
	document := newDocument(0, nil, nil, []interface{}{}, copyObject(fields))
	if _, err := CanonicalJSON(document); err != nil {
		return nil, err
	}
	return document, nil
}

// DeriveRevision is the next revision: events, observed against approvedArtifact, applied to
// parent. It copies and hashes the request and the artifact in their serialized size;
// placement events replay through one canonical key per fixed placement, while missing-item
// and carton events still scan their request lists.
func DeriveRevision(parent, approvedArtifact, events interface{}) (map[string]interface{}, error) {
	document, err := requireRevision(parent)
	if err != nil {
		return nil, err
	}
	artifact, err := requireArtifact(approvedArtifact, document["request"])
	if err != nil {
		return nil, err
	}
	list, ok := events.([]interface{})
	if !ok || len(list) == 0 {
		return nil, refuse("invalid_event", "a revision records at least one event")
	}
	last, err := lastSequence(document)
	if err != nil {
		return nil, err
	}
	recorded := make([]interface{}, len(list))
	for offset, raw := range list {
		if recorded[offset], err = recordEvent(raw, last+1+int64(offset)); err != nil {
			return nil, err
		}
	}
	request, err := ApplyEvents(document["request"], recorded)
	if err != nil {
		return nil, err
	}
	artifactDigest, err := DocumentDigest(approvedArtifact)
	if err != nil {
		return nil, err
	}
	replay := artifact["provenance"].(map[string]interface{})["replay"]
	approved := map[string]interface{}{"artifact": artifactDigest, "replay": deepCopy(replay)}
	parentDigest, err := DocumentDigest(parent)
	if err != nil {
		return nil, err
	}
	number, _ := canonicaljson.Integer(document["revision"])
	derived := newDocument(number+1, parentDigest, approved, recorded, request)
	if _, err := CanonicalJSON(derived); err != nil {
		return nil, err
	}
	return derived, nil
}

func newDocument(number int64, parent interface{}, approved interface{}, events []interface{},
	request map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"format": Format, "suite_version": artifacts.SuiteVersion, "revision": number, "parent": parent,
		"approved": approved, "events": events, "request": request,
	}
}

func requireRevision(value interface{}) (map[string]interface{}, error) {
	document, ok := value.(map[string]interface{})
	if !ok || document["format"] != Format {
		return nil, refuse("invalid_revision", "not a %s document", Format)
	}
	if number, ok := canonicaljson.Integer(document["revision"]); !ok || number < 0 {
		return nil, refuse("invalid_revision", "revision is a non-negative integer")
	}
	if _, ok := document["request"].(map[string]interface{}); !ok {
		return nil, refuse("invalid_revision", "a revision carries its request")
	}
	events, ok := document["events"].([]interface{})
	if !ok {
		return nil, refuse("invalid_revision", "a revision carries its events")
	}
	for _, event := range events {
		record, ok := event.(map[string]interface{})
		if !ok {
			return nil, refuse("invalid_revision", "a revision's events each carry an integer sequence")
		}
		if _, ok := canonicaljson.Integer(record["sequence"]); !ok {
			return nil, refuse("invalid_revision", "a revision's events each carry an integer sequence")
		}
	}
	return document, nil
}

func requireArtifact(value interface{}, request interface{}) (map[string]interface{}, error) {
	artifact, ok := value.(map[string]interface{})
	if !ok || artifact["format"] != artifacts.Format {
		return nil, refuse("invalid_artifact", "not a %s document", artifacts.Format)
	}
	provenance, ok := artifact["provenance"].(map[string]interface{})
	if !ok {
		return nil, refuse("invalid_artifact", "the artifact carries no provenance.replay")
	}
	if _, ok := provenance["replay"].(map[string]interface{}); !ok {
		return nil, refuse("invalid_artifact", "the artifact carries no provenance.replay")
	}
	same, err := sameValue(provenance["request"], request)
	if err != nil {
		return nil, err
	}
	if !same {
		return nil, refuse("invalid_artifact", "the artifact was built from a different request than the parent's")
	}
	return artifact, nil
}

func lastSequence(document map[string]interface{}) (int64, error) {
	events := document["events"].([]interface{})
	if len(events) > 0 {
		sequence, _ := canonicaljson.Integer(events[len(events)-1].(map[string]interface{})["sequence"])
		return sequence, nil
	}
	if number, _ := canonicaljson.Integer(document["revision"]); number != 0 {
		return 0, refuse("invalid_revision", "only the root revision records no events")
	}
	return 0, nil
}

func recordEvent(raw interface{}, sequence int64) (interface{}, error) {
	event, err := requireShape(raw)
	if err != nil {
		return nil, err
	}
	if given, ok := canonicaljson.Integer(event["sequence"]); !ok || given != sequence {
		return nil, refuse("invalid_event", "event sequence %s does not continue the chain at %d",
			canonicaljson.Spelling(event["sequence"]), sequence)
	}
	return deepCopy(event), nil
}

// sameValue is equality as the canonical form sees it, so 1.0 and 1 are one value, as in
// every engine.
func sameValue(left, right interface{}) (bool, error) {
	leftText, err := CanonicalJSON(left)
	if err != nil {
		return false, err
	}
	rightText, err := CanonicalJSON(right)
	if err != nil {
		return false, err
	}
	return leftText == rightText, nil
}

// deepCopy copies the containers of a decoded JSON value; scalars are immutable.
func deepCopy(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return copyObject(typed)
	case []interface{}:
		copied := make([]interface{}, len(typed))
		for index, element := range typed {
			copied[index] = deepCopy(element)
		}
		return copied
	}
	return value
}

func copyObject(object map[string]interface{}) map[string]interface{} {
	copied := make(map[string]interface{}, len(object))
	for key, element := range object {
		copied[key] = deepCopy(element)
	}
	return copied
}
