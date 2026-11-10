package policy

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
)

// Every expected order and refusal was produced by the v1.0.0 reference engine.

// decode reads JSON the way the request decoder does: integers stay int64, every other
// number is a float64.
func decode(t *testing.T, text string) interface{} {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	decoder.UseNumber()
	var raw interface{}
	if err := decoder.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	return normalise(raw)
}

func normalise(value interface{}) interface{} {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := strconv.ParseInt(string(typed), 10, 64); err == nil {
			return integer
		}
		float, _ := strconv.ParseFloat(string(typed), 64)
		return float
	case []interface{}:
		for index := range typed {
			typed[index] = normalise(typed[index])
		}
	case map[string]interface{}:
		for key := range typed {
			typed[key] = normalise(typed[key])
		}
	}
	return value
}

const rule = `"id":"r","version":1,"effective_at":0,"priority":0`

var ruleSetCases = []struct{ json, expected string }{
	{`null`, "ok "},
	{`5`, "err policy must be an object"},
	{`[]`, "err policy must be an object"},
	{`{"x":1,"b":2,"a":3}`, "err policy has unknown keys: a, b, x"},
	{`{"rules":{}}`, "ok "},
	{`{"rules":5}`, "err policy.rules must be an array"},
	{`{"as_of":0,"rules":0}`, "ok "},
	{`{"rules":[{"id":"a"}]}`, "err policy.as_of is required whenever policy.rules is non-empty"},
	{`{"as_of":-1,"rules":[{}]}`, "err policy.as_of must be an integer >= 0"},
	{`{"as_of":true,"rules":[{}]}`, "err policy.as_of must be an integer >= 0"},
	{`{"as_of":1.0,"rules":[{}]}`, "err policy.as_of must be an integer >= 0"},
	{`{"as_of":0,"rules":[5]}`, "err policy.rules[0] must be an object"},
	{`{"as_of":0,"rules":[{"id":"a"}]}`, "err policy.rules[0] must name exactly one rule form (limit_tag_per_container, require_container_tag, separate_tags), not 0"},
	{`{"as_of":0,"rules":[{"separate_tags":{},"require_container_tag":{}}]}`, "err policy.rules[0] must name exactly one rule form (limit_tag_per_container, require_container_tag, separate_tags), not 2"},
	{`{"as_of":0,"rules":[{"separate_tags":{"tag":"a","from_tag":"b"}}]}`, "err policy.rules[0].id must be a non-empty string"},
	{`{"as_of":0,"rules":[{"id":"","separate_tags":{}}]}`, "err policy.rules[0].id must be a non-empty string"},
	{`{"as_of":0,"rules":[{` + rule + `,"separate_tags":{}}]}`, "err policy.rules[0].separate_tags is missing tag, from_tag"},
	{`{"as_of":0,"rules":[{"id":"r","version":0,"separate_tags":{}}]}`, "err policy.rules[0].version must be an integer >= 1"},
	{`{"as_of":0,"rules":[{` + rule + `,"separate_tags":null}]}`, "err policy.rules[0].separate_tags must be an object"},
	{`{"as_of":0,"rules":[{` + rule + `,"separate_tags":[]}]}`, "err policy.rules[0].separate_tags must be an object"},
	{`{"as_of":0,"rules":[{` + rule + `,"separate_tags":{"z":1,"10":2,"9":3,"tag":"a"}}]}`, "err policy.rules[0].separate_tags has unknown keys: 10, 9, z"},
	{`{"as_of":0,"rules":[{` + rule + `,"separate_tags":{"tag":"a"}}]}`, "err policy.rules[0].separate_tags is missing from_tag"},
	{`{"as_of":0,"rules":[{` + rule + `,"separate_tags":{"tag":"a","from_tag":""}}]}`, "err policy.rules[0].separate_tags.from_tag must be a non-empty string"},
	{`{"as_of":0,"rules":[{` + rule + `,"limit_tag_per_container":{"tag":"a","max":-1}}]}`, "err policy.rules[0].limit_tag_per_container.max must be an integer >= 0"},
	{`{"as_of":0,"rules":[{` + rule + `,"applies_to":{"zone":"a","customer":"x"},"limit_tag_per_container":{"tag":"a","max":1}}]}`, "err policy.rules[0].applies_to names unknown shipment facts: zone"},
	{`{"as_of":0,"rules":[{` + rule + `,"applies_to":{"customer":""},"limit_tag_per_container":{"tag":"a","max":1}}]}`, "err policy.rules[0].applies_to.customer must be a non-empty string"},
	{`{"as_of":0,"shipment":"x","rules":[{` + rule + `,"limit_tag_per_container":{"tag":"a","max":1}}]}`, "err policy.shipment must be an object"},
	{`{"as_of":100,"shipment":{"customer":"acme"},"rules":[
		{"id":"b","version":1,"effective_at":0,"priority":1,"separate_tags":{"tag":"x","from_tag":"y"}},
		{"id":"a","priority":1,"version":1,"effective_at":10,"require_container_tag":{"item_tag":"x","container_tag":"cold"}},
		{"id":"a","priority":1,"version":2,"effective_at":10,"require_container_tag":{"item_tag":"x","container_tag":"frozen"}},
		{"id":"a","priority":1,"version":3,"effective_at":200,"require_container_tag":{"item_tag":"x","container_tag":"never"}},
		{"id":"c","version":1,"effective_at":0,"priority":5,"applies_to":{"customer":"acme"},"limit_tag_per_container":{"tag":"x","max":2}},
		{"id":"d","version":1,"effective_at":0,"priority":9,"applies_to":{"customer":"other"},"limit_tag_per_container":{"tag":"x","max":2}},
		{"id":"10","version":1,"effective_at":0,"priority":0,"separate_tags":{"tag":"p","from_tag":"q"}},
		{"id":"9","version":1,"effective_at":0,"priority":0,"separate_tags":{"tag":"p","from_tag":"q"}}]}`,
		"ok c@1/LimitTagPerContainer,a@2/RequireContainerTag,b@1/SeparateTags,10@1/SeparateTags,9@1/SeparateTags"},
}

func formName(form RuleForm) string {
	switch form.(type) {
	case SeparateTags:
		return "SeparateTags"
	case RequireContainerTag:
		return "RequireContainerTag"
	}
	return "LimitTagPerContainer"
}

func TestParsesAndResolvesLikeTheReference(t *testing.T) {
	for _, test := range ruleSetCases {
		set, err := ParseRuleSet(decode(t, test.json))
		var actual string
		if err != nil {
			if _, typed := err.(*PolicyError); !typed {
				t.Fatalf("%s: untyped refusal %v", test.json, err)
			}
			actual = "err " + err.Error()
		} else {
			var parts []string
			for _, rule := range set.Rules() {
				parts = append(parts, rule.Citation()+"/"+formName(rule.Form()))
			}
			actual = "ok " + strings.Join(parts, ",")
		}
		if actual != test.expected {
			t.Errorf("%s:\n expected %q\n      got %q", test.json, test.expected, actual)
		}
	}
}

func instance(t *testing.T, tags []string, id string) domain.ItemInstance {
	t.Helper()
	spec := domain.NewItemSpec(id, domain.MustDimensions(160000, 160000, 160000))
	spec.Tags = tags
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	return item.Instances()[0]
}

func dryContainer(t *testing.T) *domain.Container {
	t.Helper()
	container, err := domain.NewContainer(domain.ContainerSpec{ID: "K",
		InnerDimensions: domain.MustDimensions(1600000, 1600000, 1600000), Tags: []string{"dry"}})
	if err != nil {
		t.Fatal(err)
	}
	return container
}

func testConstraint(t *testing.T) *PolicyConstraint {
	set, err := ParseRuleSet(decode(t, `{"as_of":0,"rules":[
		{"id":"cold","version":2,"effective_at":0,"priority":3,"require_container_tag":{"item_tag":"frozen","container_tag":"reefer"}},
		{"id":"sep","version":1,"effective_at":0,"priority":2,"separate_tags":{"tag":"acid","from_tag":"base"}},
		{"id":"cap","version":1,"effective_at":0,"priority":1,"limit_tag_per_container":{"tag":"x","max":2}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return set.Constraints()[0].(*PolicyConstraint)
}

func evaluate(t *testing.T, tags []string, present ...[]string) string {
	var placements []domain.Placement
	for index, others := range present {
		other := instance(t, others, "P"+strconv.Itoa(index))
		at := domain.MustPoint(int64(index)*10*16000, 0, 0)
		size := other.Item().Dimensions()
		placements = append(placements, domain.NewPlacement(other, at, domain.LWH, size, at, size, 1))
	}
	item := instance(t, tags, "Q")
	size := item.Item().Dimensions()
	result := testConstraint(t).Evaluate(constraint.NewContext(dryContainer(t), placements, item,
		domain.MustPoint(0, 50*16000, 0), domain.LWH, size, size))
	verdict := "reject"
	if result.Allowed() {
		verdict = "allow"
	}
	return verdict + "|" + result.Code() + "|" + result.Detail()
}

func TestRejectsWithTheCitedRule(t *testing.T) {
	cases := []struct {
		actual, expected string
	}{
		{evaluate(t, []string{"frozen"}), "reject|policy_rule|cold@2: requires a container tagged 'reefer'"},
		{evaluate(t, []string{"acid"}, []string{"base"}), "reject|policy_rule|sep@1: 'acid' may not share a container with 'base'"},
		{evaluate(t, []string{"base"}, []string{"acid"}), "reject|policy_rule|sep@1: 'base' may not share a container with 'acid'"},
		{evaluate(t, []string{"x"}, []string{"x"}, []string{"x"}), "reject|policy_rule|cap@1: at most 2 item(s) tagged 'x' per container"},
		{evaluate(t, []string{"x"}, []string{"x"}), "allow|allowed|"},
		{evaluate(t, []string{"acid", "frozen"}, []string{"base"}), "reject|policy_rule|cold@2: requires a container tagged 'reefer'"},
	}
	for _, test := range cases {
		if test.actual != test.expected {
			t.Errorf("expected %q, got %q", test.expected, test.actual)
		}
	}
}

func TestProvesOnlyWhatTheRequestAloneDecides(t *testing.T) {
	policy := testConstraint(t)
	containers := []*domain.Container{dryContainer(t)}
	proof := policy.ProvesUnplaceable(instance(t, []string{"frozen"}, "F"), containers)
	expected := "cold@2: requires a container tagged 'reefer', which none of the containers offered carries"
	if proof == nil || proof.Code != "policy_rule" || proof.Detail != expected {
		t.Fatalf("unexpected proof %#v", proof)
	}
	if proof := policy.ProvesUnplaceable(instance(t, []string{"acid"}, "F"), containers); proof != nil {
		t.Fatalf("segregation proves nothing, got %#v", proof)
	}
}

func TestQuotesTagsAsTheReferenceReprDoes(t *testing.T) {
	set, err := ParseRuleSet(decode(t, `{"as_of":0,"rules":[{`+rule+`,"require_container_tag":{"item_tag":"a","container_tag":"it's"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	proof := set.Constraints()[0].(*PolicyConstraint).ProvesUnplaceable(instance(t, []string{"a"}, "F"),
		[]*domain.Container{dryContainer(t)})
	if proof == nil || !strings.HasPrefix(proof.Detail, `r@1: requires a container tagged "it's", which`) {
		t.Fatalf("unexpected proof %#v", proof)
	}
}
