package outcomes

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The distinction worth reading twice is ViewAsOf against CurrentView. Both fold the same
// immutable events; only one is safe to reconstruct a past belief with.

func shipped(t *testing.T, eventID, decisionID string, at int64, carton, supersedes string) OutcomeEvent {
	t.Helper()
	event, err := NewOutcomeEvent(OutcomeEventSpec{EventID: eventID, DecisionID: decisionID, EventType: ActualCarton,
		Payload: map[string]interface{}{"carton_id": carton, "catalog_version": 1}, RecordedAt: at, Supersedes: supersedes})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func record(t *testing.T, ledger *OutcomeLedger, events ...OutcomeEvent) {
	t.Helper()
	for _, event := range events {
		if _, err := ledger.Record(event); err != nil {
			t.Fatal(err)
		}
	}
}

func ids(events []OutcomeEvent) []string {
	names := []string{}
	for _, event := range events {
		names = append(names, event.EventID())
	}
	return names
}

func expectIDs(t *testing.T, events []OutcomeEvent, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := ids(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// ------------------------------------------- recording is append-only and idempotent

func TestARecordedEventComesBackForItsDecision(t *testing.T) {
	ledger := NewOutcomeLedger()
	record(t, ledger, shipped(t, "e1", "d1", 100, "box-a", ""))
	expectIDs(t, ledger.EventsForDecision("d1"), "e1")
}

func TestAnUnknownDecisionHasAnEmptyHistoryRatherThanAnError(t *testing.T) {
	expectIDs(t, NewOutcomeLedger().EventsForDecision("never-shipped"))
}

// Duplicate delivery is a fact of every queue: repeating an event must neither double it nor
// be an error.
func TestRecordingTheIdenticalEventTwiceIsANoOp(t *testing.T) {
	ledger := NewOutcomeLedger()
	first, err := ledger.Record(shipped(t, "e1", "d1", 100, "box-a", ""))
	if err != nil {
		t.Fatal(err)
	}
	again, err := ledger.Record(shipped(t, "e1", "d1", 100, "box-a", ""))
	if err != nil || !again.Equal(first) || len(ledger.EventsForDecision("d1")) != 1 {
		t.Fatalf("%v %v", again, err)
	}
}

// Accepting different content under one id would make the ledger's history unreadable.
func TestReusingAnEventIDForDifferentContentIsRefused(t *testing.T) {
	ledger := NewOutcomeLedger()
	record(t, ledger, shipped(t, "e1", "d1", 100, "box-a", ""))
	_, err := ledger.Record(shipped(t, "e1", "d1", 100, "box-b", ""))
	var mismatch *DuplicateEventMismatchError
	var base *OutcomeError
	if !errors.As(err, &mismatch) || !errors.As(err, &base) || err.Error() != "event id 'e1' was already recorded with different content" {
		t.Fatalf("got %v", err)
	}
}

func TestEventsComeBackInRecordedOrder(t *testing.T) {
	ledger := NewOutcomeLedger()
	record(t, ledger, shipped(t, "e1", "d1", 300, "box-a", ""), shipped(t, "e2", "d1", 100, "box-a", ""),
		shipped(t, "e3", "d1", 200, "box-a", ""))
	expectIDs(t, ledger.EventsForDecision("d1"), "e1", "e2", "e3")
}

func TestDecisionsAreKeptApart(t *testing.T) {
	ledger := NewOutcomeLedger()
	record(t, ledger, shipped(t, "e1", "d1", 100, "box-a", ""), shipped(t, "e2", "d2", 100, "box-a", ""))
	expectIDs(t, ledger.EventsForDecision("d1"), "e1")
	expectIDs(t, ledger.EventsForDecision("d2"), "e2")
}

// ----------------------------------------- a correction supersedes rather than overwrites

func corrected(t *testing.T) *OutcomeLedger {
	ledger := NewOutcomeLedger()
	record(t, ledger, shipped(t, "e1", "d1", 100, "box-a", ""), shipped(t, "e2", "d1", 200, "box-b", "e1"))
	return ledger
}

func TestTheCorrectionReplacesTheOriginalInTheCurrentView(t *testing.T) {
	expectIDs(t, corrected(t).CurrentView("d1"), "e2")
}

// "Append-only" is the whole claim; a correction that erased what it corrected would make the
// ledger a mutable store wearing an immutable name.
func TestTheOriginalIsStillInTheUnfoldedHistory(t *testing.T) {
	expectIDs(t, corrected(t).EventsForDecision("d1"), "e1", "e2")
}

func TestSupersedingAnEventTheLedgerHasNeverSeenIsRefused(t *testing.T) {
	_, err := NewOutcomeLedger().Record(shipped(t, "e2", "d1", 200, "box-a", "ghost"))
	var missing *OutcomeEventNotFoundError
	if !errors.As(err, &missing) || err.Error() != "event 'e2' supersedes unknown event 'ghost'" {
		t.Fatalf("got %v", err)
	}
}

func TestAnEventCannotSupersedeItself(t *testing.T) {
	_, err := NewOutcomeEvent(OutcomeEventSpec{EventID: "e1", DecisionID: "d1", EventType: ActualCarton, RecordedAt: 100, Supersedes: "e1"})
	if err == nil || !strings.Contains(err.Error(), "cannot supersede itself") {
		t.Fatalf("got %v", err)
	}
}

// ---------------------------------------------- time travel is narrower than the current view

func history(t *testing.T) *OutcomeLedger {
	ledger := NewOutcomeLedger()
	record(t, ledger, shipped(t, "e1", "d1", 100, "box-a", ""), shipped(t, "e2", "d1", 300, "box-b", "e1"))
	return ledger
}

func viewAsOf(t *testing.T, ledger *OutcomeLedger, at int64) []OutcomeEvent {
	t.Helper()
	view, err := ledger.ViewAsOf("d1", at)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestBeforeAnythingWasRecordedTheViewIsEmpty(t *testing.T) {
	expectIDs(t, viewAsOf(t, history(t), 50))
}

func TestBetweenTheEventAndItsCorrectionTheOriginalStillStands(t *testing.T) {
	view := viewAsOf(t, history(t), 200)
	expectIDs(t, view, "e1")
	if view[0].Payload()["carton_id"] != "box-a" {
		t.Fatalf("%v", view[0].Payload())
	}
}

func TestAfterTheCorrectionTheCorrectionApplies(t *testing.T) {
	expectIDs(t, viewAsOf(t, history(t), 400), "e2")
}

// at is the instant the question is asked; an event recorded at that instant is not yet part
// of what was believed.
func TestTheBoundaryIsStrictlyBefore(t *testing.T) {
	expectIDs(t, viewAsOf(t, history(t), 100))
}

func TestTheCurrentViewDisagreesWithThePastOneOnPurpose(t *testing.T) {
	ledger := history(t)
	expectIDs(t, viewAsOf(t, ledger, 200), "e1")
	expectIDs(t, ledger.CurrentView("d1"), "e2")
}

func TestANegativeInstantIsRefused(t *testing.T) {
	if _, err := history(t).ViewAsOf("d1", -1); err == nil || err.Error() != "as-of time cannot be negative" {
		t.Fatalf("got %v", err)
	}
}

// -------------------------------------------- several event kinds coexist on one decision

func TestAShipmentAndItsDamageBothStand(t *testing.T) {
	ledger := NewOutcomeLedger()
	damage, err := NewOutcomeEvent(OutcomeEventSpec{EventID: "e2", DecisionID: "d1", EventType: Damage,
		Payload: map[string]interface{}{"reason_code": "crushed_corner", "severity": "minor"}, RecordedAt: 200})
	if err != nil {
		t.Fatal(err)
	}
	record(t, ledger, shipped(t, "e1", "d1", 100, "box-a", ""), damage)
	var kinds []string
	for _, event := range ledger.CurrentView("d1") {
		kinds = append(kinds, string(event.EventType()))
	}
	if !reflect.DeepEqual(kinds, []string{"actual_carton", "damage"}) {
		t.Fatalf("%v", kinds)
	}
}

// ---------------------------------------------- an event must name its decision

// The decision id is the only index the ledger has; an event without one could never be read
// back.
func TestAnEventWithoutADecisionIDIsRefused(t *testing.T) {
	_, err := NewOutcomeEvent(OutcomeEventSpec{EventID: "e1", EventType: ActualCarton,
		Payload: map[string]interface{}{"carton_id": "box-a", "catalog_version": 1}, RecordedAt: 100})
	if err == nil || !strings.Contains(err.Error(), "decision_id is required") {
		t.Fatalf("got %v", err)
	}
}

// ---------------------------------------------- the closed payload vocabulary

// A field the ledger does not know -- a free-text note that might hold a name -- is refused,
// never silently stored or dropped.
func TestAFieldOutsideTheVocabularyIsRefusedByName(t *testing.T) {
	_, err := NewOutcomeEvent(OutcomeEventSpec{EventID: "e1", DecisionID: "d1", EventType: Damage,
		Payload: map[string]interface{}{"reason_code": "crushed_corner", "note": "Ms Smith, 4 High St"}, RecordedAt: 100})
	var unsupported *UnsupportedOutcomeFieldError
	if !errors.As(err, &unsupported) ||
		err.Error() != "event type 'damage' does not support field(s) ['note']; allowed: ['reason_code', 'severity']" {
		t.Fatalf("got %v", err)
	}
	_, err = NewOutcomeEvent(OutcomeEventSpec{EventID: "e1", DecisionID: "d1", EventType: "weather", RecordedAt: 100})
	if !errors.As(err, &unsupported) || err.Error() != "unsupported event type 'weather'" {
		t.Fatalf("got %v", err)
	}
}

func TestAnEventGuardsItsOwnFields(t *testing.T) {
	for _, c := range []struct {
		spec    OutcomeEventSpec
		message string
	}{
		{OutcomeEventSpec{DecisionID: "d1", EventType: Return}, "event_id is required"},
		{OutcomeEventSpec{EventID: "e1", DecisionID: "d1", EventType: Return, RecordedAt: -1}, "recorded_at cannot be negative"},
	} {
		if _, err := NewOutcomeEvent(c.spec); err == nil || err.Error() != c.message {
			t.Errorf("got %v, want %q", err, c.message)
		}
	}
}

// An event keeps its own copy of its payload, so a recorded fact cannot change afterwards.
func TestARecordedFactCannotBeChangedFromOutside(t *testing.T) {
	payload := map[string]interface{}{"reason_code": "crushed_corner"}
	event, err := NewOutcomeEvent(OutcomeEventSpec{EventID: "e1", DecisionID: "d1", EventType: Return, Payload: payload, RecordedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	payload["reason_code"] = "changed"
	event.Payload()["reason_code"] = "changed"
	if event.Payload()["reason_code"] != "crushed_corner" {
		t.Fatal("the event changed")
	}
	if fields, known := AllowedFields(MeasuredDimensions); !known || !reflect.DeepEqual(fields, []string{"height_mm", "length_mm", "width_mm"}) {
		t.Fatalf("%v", fields)
	}
}
