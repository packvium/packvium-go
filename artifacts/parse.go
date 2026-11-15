package artifacts

import (
	"unicode/utf8"

	"github.com/packvium/packvium-go/internal/canonicaljson"
	"github.com/packvium/packvium-go/internal/jsonvalue"
)

// Parse reads one JSON document into the decoded form Build and the exporters take, with
// integers kept exact and distinct from floats.
//
// Text that is not UTF-8 or not one JSON document is refused as invalid_json. Text escaping a
// lone UTF-16 surrogate ("\ud800") is refused as invalid_string: encoding/json would decode it
// as U+FFFD, a different string, and the artifact would then carry a value nobody sent.
func Parse(text []byte) (interface{}, error) {
	if !utf8.Valid(text) {
		return nil, refuse(CodeInvalidJSON, "not JSON text: the text is not UTF-8")
	}
	value, err := jsonvalue.DecodeBytes(text)
	if err != nil {
		return nil, refuse(CodeInvalidJSON, "not JSON text: %s", err.Error())
	}
	if canonicaljson.HasLoneSurrogate(text) {
		return nil, refuse(CodeInvalidString, "a string carries a lone UTF-16 surrogate")
	}
	return value, nil
}

// FromJSON is Build over a request and a result given as JSON text.
func FromJSON(request, result []byte, loadingOrders map[int][]int) (map[string]interface{}, error) {
	requestValue, err := Parse(request)
	if err != nil {
		return nil, err
	}
	resultValue, err := Parse(result)
	if err != nil {
		return nil, err
	}
	return Build(requestValue, resultValue, loadingOrders)
}
