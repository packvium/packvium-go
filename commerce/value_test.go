package commerce

import (
	"math/big"
	"reflect"
	"testing"
)

func TestDecodeKeepsFirstKeyPositionAndLastValue(t *testing.T) {
	decoded, err := Decode([]byte(`{"b": 1, "a": 2, "b": 3}`))
	if err != nil {
		t.Fatal(err)
	}
	object := decoded.(*Object)
	if !reflect.DeepEqual(object.Keys(), []string{"b", "a"}) {
		t.Fatalf("keys %v", object.Keys())
	}
	if value, _ := object.Get("b"); value.(*big.Int).Int64() != 3 {
		t.Fatalf("b = %v", value)
	}
}

func TestDecodeSeparatesIntegersFromFloats(t *testing.T) {
	decoded, err := Decode([]byte(`[123456789012345678901234567890, -0, 1.0, 1e2, 1e400]`))
	if err != nil {
		t.Fatal(err)
	}
	values := decoded.([]interface{})
	if values[0].(*big.Int).String() != "123456789012345678901234567890" {
		t.Fatalf("big integer %v", values[0])
	}
	if values[1].(*big.Int).Sign() != 0 {
		t.Fatalf("-0 is the integer zero, got %v", values[1])
	}
	if values[2].(float64) != 1 || values[3].(float64) != 100 {
		t.Fatalf("floats %v %v", values[2], values[3])
	}
	if huge := values[4].(float64); huge <= 1e308 {
		t.Fatalf("1e400 must overflow to an infinity, got %v", huge)
	}
}

func TestDecodeRefusesTrailingData(t *testing.T) {
	for _, input := range []string{`{} {}`, `[1,]`, `{"a"}`, ``} {
		if _, err := Decode([]byte(input)); err == nil {
			t.Errorf("%q decoded", input)
		}
	}
}

func TestCanonicalJSONMatchesPythonDumps(t *testing.T) {
	document := map[string]interface{}{
		"😀":     1,
		"Ａ":     []string{"\"\\\n\r\t\b\f\x01\x1f\u007f é"},
		"a":     nil,
		"b":     true,
		"big":   new(big.Int).Lsh(big.NewInt(1), 100),
		"inner": NewObject(),
	}
	observed, err := CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"a":null,"b":true,"big":1267650600228229401496703205376,"inner":{},` +
		`"Ａ":["\"\\\n\r\t\b\f\u0001\u001f` + "\u007f é" + `"],"😀":1}`
	if observed != expected {
		t.Fatalf("\nexpected %s\nobserved %s", expected, observed)
	}
}

// The expected text is Python's json.dumps(value, indent=2, ensure_ascii=False) of the
// same document, decoded in the same member order.
func TestIndentedJSONMatchesPythonDumps(t *testing.T) {
	document := mustDecode(t, `{"z":1,"a":[],"m":{},"n":[["x",2],{"k":null,"b":true}],"é😀":"\"\n`+"\u2028"+`"}`)
	observed, err := IndentedJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	expected := "{\n  \"z\": 1,\n  \"a\": [],\n  \"m\": {},\n  \"n\": [\n    [\n      \"x\",\n      2\n    ],\n" +
		"    {\n      \"k\": null,\n      \"b\": true\n    }\n  ],\n  \"é😀\": \"\\\"\\n\u2028\"\n}"
	if observed != expected {
		t.Fatalf("\nexpected %s\nobserved %s", expected, observed)
	}
}

func TestResultKeepsReferenceMemberOrder(t *testing.T) {
	result, err := CatalogVersionInfo(map[string]interface{}{}, map[string]interface{}{"catalog_id": "c", "resolved_at": 0})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := IndentedJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	expected := "{\n  \"api_version\": 1,\n  \"status\": \"rejected\",\n  \"error\": {\n" +
		"    \"code\": \"catalog_not_found\",\n    \"fields\": {\n      \"catalog_id\": \"c\"\n    }\n  }\n}"
	if observed != expected {
		t.Fatalf("\nexpected %s\nobserved %s", expected, observed)
	}
}

func TestCanonicalJSONRefusesFloats(t *testing.T) {
	if _, err := CanonicalJSON(map[string]interface{}{"x": 1.5}); err == nil {
		t.Fatal("a float serialized")
	}
}

func TestNativeGoValuesAnswerLikeDecodedOnes(t *testing.T) {
	document := map[string]interface{}{"tariffs": []interface{}{map[string]interface{}{
		"carrier_id": "acme", "service_id": "ground", "versions": []interface{}{map[string]interface{}{
			"effective_at": 0, "dimensional_weight_divisor": int64(5000),
			"cost_per_dimensional_kg_minor": map[string]interface{}{"zone-a": 450},
		}},
	}}}
	request := map[string]interface{}{
		"carrier_id": "acme", "service_id": "ground", "tariff_version": 1,
		"zone": "zone-a", "actual_weight_g": 2000, "volume_mm3": 1000000,
	}
	result, err := Quote(document, request)
	if err != nil {
		t.Fatal(err)
	}
	quote, _ := result.Get("quote")
	total := quote.(*Object).lookup("total_minor").(*big.Int)
	if total.Int64() != 900 {
		t.Fatalf("total_minor %v", total)
	}
}

func TestUnsupportedGoValueIsAnInputError(t *testing.T) {
	_, err := Quote(map[string]interface{}{"tariffs": struct{}{}}, map[string]interface{}{})
	if _, isInput := err.(*InputError); !isInput {
		t.Fatalf("got %v (%T)", err, err)
	}
}
