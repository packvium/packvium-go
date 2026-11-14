// Package outcomes is an append-only ledger of first-party shipment outcomes and packing
// exceptions: which alternative was chosen, which carton was really used, measured dimensions
// and weight, a repack, damage, a return, an operator override.
//
// Closing the operating loop needs a ledger, not a mutable "decision" row a later event can
// quietly edit:
//
//   - every event names the decision it is about (DecisionID, expected to be the caller's own
//     idempotency key for the job) and is keyed by its own EventID, so a duplicate delivery of
//     the same event is idempotent;
//   - a correction never rewrites an earlier event: it is a new event naming the one it
//     supersedes, so EventsForDecision always has everything ever recorded, while
//     CurrentView folds corrections in without discarding what was believed before;
//   - every payload is checked against a closed, named field list per event type. A field the
//     ledger does not recognise -- a free-text note that might hold a customer's name or
//     address -- is refused at construction, never stored.
//
// The ledger has no method that reads, re-runs or overrides a packing decision's validation.
// It only appends structured facts about decisions another, already validated system made, so
// there is no path here that could bypass validation. It is an in-memory reference
// implementation of the contract; a production deployment backs it with durable,
// append-only storage.
package outcomes

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/packvium/packvium-go/internal/pyrepr"
)

// OutcomeError is the base of every outcome error this package returns that is not a plain
// argument error. errors.As matches the more specific errors below as one too.
type OutcomeError struct{ message string }

// Error is the message, in the reference's wording.
func (e *OutcomeError) Error() string { return e.message }

// UnsupportedOutcomeFieldError is a payload field outside the closed vocabulary of its event
// type, or an event type outside the closed set. It is the ledger's guard against personal
// data: a field the ledger does not explicitly know can never be silently stored.
type UnsupportedOutcomeFieldError struct{ message string }

// Error is the message, in the reference's wording.
func (e *UnsupportedOutcomeFieldError) Error() string { return e.message }

// Unwrap makes the error match *OutcomeError, its base in the reference.
func (e *UnsupportedOutcomeFieldError) Unwrap() error { return &OutcomeError{e.message} }

// DuplicateEventMismatchError is an event id recorded again with different content. A
// duplicate delivery repeats an event; it does not carry a different one wearing its id.
type DuplicateEventMismatchError struct{ message string }

// Error is the message, in the reference's wording.
func (e *DuplicateEventMismatchError) Error() string { return e.message }

// Unwrap makes the error match *OutcomeError, its base in the reference.
func (e *DuplicateEventMismatchError) Unwrap() error { return &OutcomeError{e.message} }

// OutcomeEventNotFoundError is a correction naming an event the ledger has never recorded.
type OutcomeEventNotFoundError struct{ message string }

// Error is the message, in the reference's wording.
func (e *OutcomeEventNotFoundError) Error() string { return e.message }

// Unwrap makes the error match *OutcomeError, its base in the reference.
func (e *OutcomeEventNotFoundError) Unwrap() error { return &OutcomeError{e.message} }

// OutcomeEventType is one kind of outcome fact.
type OutcomeEventType string

// The closed set of event types.
const (
	AlternativeChosen  OutcomeEventType = "alternative_chosen"
	ActualCarton       OutcomeEventType = "actual_carton"
	MeasuredDimensions OutcomeEventType = "measured_dimensions"
	MeasuredWeight     OutcomeEventType = "measured_weight"
	Repack             OutcomeEventType = "repack"
	Damage             OutcomeEventType = "damage"
	Return             OutcomeEventType = "return"
	OperatorOverride   OutcomeEventType = "operator_override"
)

// allowedFields is the payload vocabulary per event type: deliberately narrow and structured
// -- ids, numeric measurements, enumerated reason codes -- with nothing a caller could use to
// smuggle in personally identifiable data. Each list is sorted.
var allowedFields = map[OutcomeEventType][]string{
	AlternativeChosen:  {"alternative_index", "objective_score"},
	ActualCarton:       {"carton_id", "catalog_version"},
	MeasuredDimensions: {"height_mm", "length_mm", "width_mm"},
	MeasuredWeight:     {"weight_g"},
	Repack:             {"new_carton_id", "reason_code"},
	Damage:             {"reason_code", "severity"},
	Return:             {"reason_code"},
	OperatorOverride:   {"operator_id", "reason_code"},
}

// AllowedFields is the sorted payload vocabulary of an event type, and whether the type is
// one the ledger knows.
func AllowedFields(eventType OutcomeEventType) ([]string, bool) {
	fields, known := allowedFields[eventType]
	return append([]string(nil), fields...), known
}

func validatePayload(eventType OutcomeEventType, payload map[string]interface{}) error {
	allowed, known := allowedFields[eventType]
	if !known {
		return &UnsupportedOutcomeFieldError{fmt.Sprintf("unsupported event type %s", pyrepr.String(string(eventType)))}
	}
	var unsupported []string
	for field := range payload {
		if !contains(allowed, field) {
			unsupported = append(unsupported, field)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return &UnsupportedOutcomeFieldError{fmt.Sprintf("event type %s does not support field(s) %s; allowed: %s",
		pyrepr.String(string(eventType)), listRepr(unsupported), listRepr(allowed))}
}

func contains(words []string, wanted string) bool {
	for _, word := range words {
		if word == wanted {
			return true
		}
	}
	return false
}

// listRepr is the reference's repr of a list of strings.
func listRepr(words []string) string {
	text := "["
	for index, word := range words {
		if index > 0 {
			text += ", "
		}
		text += pyrepr.String(word)
	}
	return text + "]"
}

// OutcomeEventSpec is the caller's description of one fact. Supersedes is the event id this
// one corrects, or empty.
type OutcomeEventSpec struct {
	EventID    string
	DecisionID string
	EventType  OutcomeEventType
	Payload    map[string]interface{}
	RecordedAt int64
	Supersedes string
}

// OutcomeEvent is one immutable fact about one decision. A correction is a new event whose
// Supersedes names this one's EventID.
type OutcomeEvent struct{ spec OutcomeEventSpec }

// NewOutcomeEvent checks an event in the reference's order and keeps its own copy of the
// payload.
func NewOutcomeEvent(spec OutcomeEventSpec) (OutcomeEvent, error) {
	switch {
	case spec.EventID == "":
		return OutcomeEvent{}, fmt.Errorf("event_id is required")
	case spec.DecisionID == "":
		return OutcomeEvent{}, fmt.Errorf("decision_id is required")
	case spec.RecordedAt < 0:
		return OutcomeEvent{}, fmt.Errorf("recorded_at cannot be negative")
	case spec.Supersedes == spec.EventID:
		return OutcomeEvent{}, fmt.Errorf("an event cannot supersede itself")
	}
	if err := validatePayload(spec.EventType, spec.Payload); err != nil {
		return OutcomeEvent{}, err
	}
	frozen := spec
	frozen.Payload = copyPayload(spec.Payload)
	return OutcomeEvent{frozen}, nil
}

func copyPayload(payload map[string]interface{}) map[string]interface{} {
	copied := make(map[string]interface{}, len(payload))
	for field, value := range payload {
		copied[field] = value
	}
	return copied
}

// EventID keys the event itself.
func (e OutcomeEvent) EventID() string { return e.spec.EventID }

// DecisionID is the decision the event is about.
func (e OutcomeEvent) DecisionID() string { return e.spec.DecisionID }

// EventType is the kind of fact.
func (e OutcomeEvent) EventType() OutcomeEventType { return e.spec.EventType }

// Payload is a copy of the event's fields.
func (e OutcomeEvent) Payload() map[string]interface{} { return copyPayload(e.spec.Payload) }

// RecordedAt is when the fact was recorded.
func (e OutcomeEvent) RecordedAt() int64 { return e.spec.RecordedAt }

// Supersedes is the event this one corrects, or empty.
func (e OutcomeEvent) Supersedes() string { return e.spec.Supersedes }

// Equal is whether two events carry the same content, payload values compared as Go values.
func (e OutcomeEvent) Equal(other OutcomeEvent) bool { return reflect.DeepEqual(e.spec, other.spec) }

// OutcomeLedger is an append-only store of events, indexed by decision. The zero value is not
// usable; NewOutcomeLedger is.
type OutcomeLedger struct {
	byID       map[string]OutcomeEvent
	byDecision map[string][]string
}

// NewOutcomeLedger is an empty ledger.
func NewOutcomeLedger() *OutcomeLedger {
	return &OutcomeLedger{byID: map[string]OutcomeEvent{}, byDecision: map[string][]string{}}
}

// Record appends event, or returns the event already recorded under its id when the content
// is identical (an idempotent duplicate delivery). Different content under a recorded id is a
// *DuplicateEventMismatchError, and a correction of an unknown event an
// *OutcomeEventNotFoundError.
func (l *OutcomeLedger) Record(event OutcomeEvent) (OutcomeEvent, error) {
	if existing, recorded := l.byID[event.spec.EventID]; recorded {
		if !existing.Equal(event) {
			return OutcomeEvent{}, &DuplicateEventMismatchError{fmt.Sprintf(
				"event id %s was already recorded with different content", pyrepr.String(event.spec.EventID))}
		}
		return existing, nil
	}
	if supersedes := event.spec.Supersedes; supersedes != "" {
		if _, known := l.byID[supersedes]; !known {
			return OutcomeEvent{}, &OutcomeEventNotFoundError{fmt.Sprintf("event %s supersedes unknown event %s",
				pyrepr.String(event.spec.EventID), pyrepr.String(supersedes))}
		}
	}
	l.byID[event.spec.EventID] = event
	l.byDecision[event.spec.DecisionID] = append(l.byDecision[event.spec.DecisionID], event.spec.EventID)
	return event, nil
}

// EventsForDecision is the complete, unfolded history of one decision in recorded order,
// every superseded event included. An unknown decision has an empty history.
func (l *OutcomeLedger) EventsForDecision(decisionID string) []OutcomeEvent {
	ids := l.byDecision[decisionID]
	events := make([]OutcomeEvent, len(ids))
	for index, id := range ids {
		events[index] = l.byID[id]
	}
	return events
}

// ViewAsOf is what was believed about one decision at instant at: the events recorded
// strictly before it, folded with only the corrections also recorded by then.
//
// CurrentView cannot answer this. It folds every correction ever recorded, so using it to
// reconstruct a past belief pulls later knowledge backward through time: a holdout built on
// it would train on evidence that did not exist when the recommendation was formed. This is
// a narrower fold over the same immutable events, not a second store.
func (l *OutcomeLedger) ViewAsOf(decisionID string, at int64) ([]OutcomeEvent, error) {
	if at < 0 {
		return nil, fmt.Errorf("as-of time cannot be negative")
	}
	var known []OutcomeEvent
	for _, event := range l.EventsForDecision(decisionID) {
		if event.spec.RecordedAt < at {
			known = append(known, event)
		}
	}
	return unsuperseded(known), nil
}

// CurrentView is the current, corrected understanding of one decision: every recorded event
// except those a later correction supersedes. History is never discarded; this only filters
// which events currently apply.
func (l *OutcomeLedger) CurrentView(decisionID string) []OutcomeEvent {
	return unsuperseded(l.EventsForDecision(decisionID))
}

func unsuperseded(events []OutcomeEvent) []OutcomeEvent {
	superseded := map[string]bool{}
	for _, event := range events {
		if event.spec.Supersedes != "" {
			superseded[event.spec.Supersedes] = true
		}
	}
	var standing []OutcomeEvent
	for _, event := range events {
		if !superseded[event.spec.EventID] {
			standing = append(standing, event)
		}
	}
	return standing
}
