package serialization

import (
	"reflect"
	"testing"

	"github.com/packvium/packvium-go/internal/jsonvalue"
)

// TestTheQualityBeamNumbersEachContainerTypeFromOne: a plan that opens a crate and then a box
// says crate#1 and box#1, as every engine does since 1.4.0, not box#2.
func TestTheQualityBeamNumbersEachContainerTypeFromOne(t *testing.T) {
	request, err := jsonvalue.DecodeBytes([]byte(`{
	  "configuration": {"solver_profile": "quality", "effort_budget": {"max_search_nodes": 5000}, "time_limit_ms": 60000},
	  "items": [{"id": "cube", "quantity": 6, "dimensions": {"length": "100", "width": "100", "height": "100"}}],
	  "containers": [
	    {"id": "box", "quantity": 1, "cost_minor": 1, "inner_dimensions": {"length": "200", "width": "100", "height": "100"}},
	    {"id": "crate", "quantity": 2, "cost_minor": 5, "inner_dimensions": {"length": "200", "width": "100", "height": "200"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Pack(request)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, container := range result["containers"].([]interface{}) {
		ids = append(ids, container.(map[string]interface{})["id"].(string))
	}
	if !reflect.DeepEqual(ids, []string{"crate#1", "box#1"}) {
		t.Errorf("container ids %v", ids)
	}
}
