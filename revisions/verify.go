package revisions

import (
	"fmt"

	"github.com/packvium/packvium-go/internal/canonicaljson"
)

// VerifyRevisionChain is every way the chain fails to be what its root and events derive,
// without stopping at the first. artifacts[k], when artifacts is not nil, is the artifact
// revision k names as approved; a nil entry is not checked. O(chain size) plus the replay of
// each revision's events.
func VerifyRevisionChain(revisions, artifacts interface{}) ([]Issue, error) {
	chain, ok := revisions.([]interface{})
	if !ok {
		return nil, refuse("invalid_revision", "a chain is a JSON array")
	}
	var approved []interface{}
	if artifacts != nil {
		if approved, ok = artifacts.([]interface{}); !ok {
			return nil, refuse("invalid_artifact", "artifacts is a JSON array")
		}
	}
	issues := []Issue{}
	last := int64(0)
	for position, value := range chain {
		at := int64(position)
		revision, err := requireRevision(value)
		if err != nil {
			return append(issues, Issue{"invalid_revision", at, err.Error()}), nil
		}
		if number, _ := canonicaljson.Integer(revision["revision"]); number != at {
			issues = append(issues, Issue{"revision_number", at, fmt.Sprintf("revision %d at position %d", number, at)})
		}
		var parent map[string]interface{}
		expected := interface{}(nil)
		if position > 0 {
			parent = chain[position-1].(map[string]interface{})
			digest, err := DocumentDigest(parent)
			if err != nil {
				return nil, err
			}
			expected = digest
		}
		if recorded := revision["parent"]; !sameDigest(recorded, expected) {
			issues = append(issues, Issue{"parent_mismatch", at,
				fmt.Sprintf("parent %s, expected %s", plain(recorded), plain(expected))})
		}
		last, issues = checkSequences(revision, at, last, issues)
		if parent == nil {
			continue
		}
		if issues, err = checkRequest(revision, parent, at, issues); err != nil {
			return nil, err
		}
		if position < len(approved) && approved[position] != nil {
			if issues, err = checkArtifact(revision, parent, approved[position], at, issues); err != nil {
				return nil, err
			}
		}
	}
	return issues, nil
}

// sameDigest compares a recorded parent with the expected one, nil for the root.
func sameDigest(recorded, expected interface{}) bool {
	if expected == nil {
		return recorded == nil
	}
	text, ok := recorded.(string)
	return ok && text == expected.(string)
}

// plain is a digest as itself, and anything a tampered chain puts in its place as JSON.
func plain(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	return canonicaljson.Spelling(value)
}

func checkSequences(revision map[string]interface{}, position, last int64, issues []Issue) (int64, []Issue) {
	events := revision["events"].([]interface{})
	if position == 0 && len(events) > 0 {
		issues = append(issues, Issue{"sequence_gap", 0, "the root revision records no events"})
	}
	if position > 0 && len(events) == 0 {
		issues = append(issues, Issue{"sequence_gap", position, "a revision records at least one event"})
	}
	for _, event := range events {
		sequence := eventSequence(event.(map[string]interface{}))
		if sequence != last+1 {
			issues = append(issues, Issue{"sequence_gap", position, fmt.Sprintf("event %d where %d was next", sequence,
				last+1)})
		}
		last = sequence
	}
	return last, issues
}

func checkRequest(revision, parent map[string]interface{}, position int64, issues []Issue) ([]Issue, error) {
	derived, err := ApplyEvents(parent["request"], revision["events"])
	if refusal, ok := err.(*Error); ok {
		return append(issues, Issue{"request_mismatch", position, "the events do not apply: " + refusal.Message}), nil
	}
	if err != nil {
		return nil, err
	}
	same, err := sameValue(derived, revision["request"])
	if err != nil {
		return nil, err
	}
	if !same {
		issues = append(issues, Issue{"request_mismatch", position,
			"the request is not what the parent's request and these events derive"})
	}
	return issues, nil
}

func checkArtifact(revision, parent map[string]interface{}, artifact interface{}, position int64,
	issues []Issue) ([]Issue, error) {
	approved, isObject := revision["approved"].(map[string]interface{})
	digest, err := DocumentDigest(artifact)
	if err != nil || !isObject || !sameDigest(approved["artifact"], digest) {
		return append(issues, Issue{"artifact_mismatch", position,
			"the artifact's digest is not the one this revision approved"}), nil
	}
	var provenance map[string]interface{}
	if fields, ok := artifact.(map[string]interface{}); ok {
		provenance, _ = fields["provenance"].(map[string]interface{})
	}
	if provenance == nil {
		return append(issues, Issue{"artifact_mismatch", position,
			"the artifact was built from a different request than the parent's"}), nil
	}
	same, err := sameValue(provenance["request"], parent["request"])
	if err != nil {
		return nil, err
	}
	if !same {
		return append(issues, Issue{"artifact_mismatch", position,
			"the artifact was built from a different request than the parent's"}), nil
	}
	same, err = sameValue(provenance["replay"], approved["replay"])
	if err != nil {
		return nil, err
	}
	if !same {
		issues = append(issues, Issue{"artifact_mismatch", position,
			"approved.replay is not the artifact's provenance.replay"})
	}
	return issues, nil
}
