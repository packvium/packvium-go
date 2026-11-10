package serialization

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/toxakara/packvium-go/internal/jsonvalue"
	"github.com/toxakara/packvium-go/units"
)

// The reference reads request values with its own built-in conversions (str, int, float,
// bool); these helpers reproduce them over decoded JSON values.

type object map[string]interface{}

func asObject(value interface{}, scope string) (object, error) {
	if value == nil {
		return object{}, nil
	}
	typed, ok := value.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be an object", scope)
	}
	return object(typed), nil
}

func asList(value interface{}, scope string) ([]interface{}, error) {
	if value == nil {
		return nil, nil
	}
	typed, ok := value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be an array", scope)
	}
	return typed, nil
}

// get treats an explicit null as absent, so a defaulted field reads the same whether it is
// omitted or null.
func (o object) get(key string) (interface{}, bool) {
	value, ok := o[key]
	return value, ok && value != nil
}

// toText is the reference's str() of a JSON scalar.
func toText(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case int64:
		return strconv.FormatInt(typed, 10)
	case *big.Int:
		return typed.String()
	case float64:
		return jsonvalue.FloatRepr(typed)
	case nil:
		return "None"
	}
	encoded, _ := jsonvalue.Canonical(value)
	return encoded
}

func (o object) text(key, fallback string) string {
	if value, ok := o.get(key); ok {
		return toText(value)
	}
	return fallback
}

// toInt is the reference's int(): integers as they are, floats truncated, numeric text
// parsed. Integers past int64 are refused, the same range PHP holds.
func toInt(value interface{}, name string) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case bool:
		if typed {
			return 1, nil
		}
		return 0, nil
	case *big.Int:
		return 0, fmt.Errorf("%s exceeds the 64-bit integer range", name)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, fmt.Errorf("cannot convert float %s to integer", jsonvalue.FloatRepr(typed))
		}
		truncated := math.Trunc(typed)
		if truncated >= 9.223372036854775807e18 || truncated < -9.223372036854775808e18 {
			return 0, fmt.Errorf("%s exceeds the 64-bit integer range", name)
		}
		return int64(truncated), nil
	case string:
		parsed, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(typed), "_", ""), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid literal for int() with base 10: '%s'", typed)
		}
		return parsed, nil
	}
	return 0, fmt.Errorf("%s must be an integer", name)
}

func (o object) integer(key string, fallback int64) (int64, error) {
	if value, ok := o.get(key); ok {
		return toInt(value, key)
	}
	return fallback, nil
}

func (o object) optionalInteger(key string) (*int64, error) {
	value, ok := o.get(key)
	if !ok {
		return nil, nil
	}
	parsed, err := toInt(value, key)
	return &parsed, err
}

// toFloat is the reference's float().
func toFloat(value interface{}, name string) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case int64:
		return float64(typed), nil
	case *big.Int:
		converted, _ := new(big.Float).SetInt(typed).Float64()
		return converted, nil
	case bool:
		if typed {
			return 1, nil
		}
		return 0, nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("could not convert string to float: '%s'", typed)
		}
		return parsed, nil
	}
	return 0, fmt.Errorf("%s must be a number", name)
}

func (o object) number(key string, fallback float64) (float64, error) {
	if value, ok := o.get(key); ok {
		return toFloat(value, key)
	}
	return fallback, nil
}

// truthy is the reference's bool().
func truthy(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case int64:
		return typed != 0
	case *big.Int:
		return typed.Sign() != 0
	case float64:
		return typed != 0
	case string:
		return typed != ""
	case []interface{}:
		return len(typed) > 0
	case map[string]interface{}:
		return len(typed) > 0
	}
	return true
}

func (o object) boolean(key string, fallback bool) bool {
	if value, ok := o.get(key); ok {
		return truthy(value)
	}
	return fallback
}

func (o object) strings(key string) ([]string, error) {
	value, _ := o.get(key)
	list, err := asList(value, key)
	if err != nil {
		return nil, err
	}
	texts := make([]string, len(list))
	for index, element := range list {
		texts[index] = toText(element)
	}
	return texts, nil
}

// quantity turns a JSON magnitude into a units.Quantity, keeping floats out of geometry.
func quantity(value interface{}, kind string) (units.Quantity, error) {
	switch typed := value.(type) {
	case int64:
		return units.IntQuantity(typed), nil
	case *big.Int:
		return units.BigQuantity(typed), nil
	case bool:
		if typed {
			return units.IntQuantity(1), nil
		}
		return units.IntQuantity(0), nil
	case string:
		return units.TextQuantity(typed), nil
	case float64:
		return units.Quantity{}, fmt.Errorf("float %s input is intentionally rejected; use a decimal string", kind)
	}
	return units.TextQuantity(toText(value)), nil
}

// parseLength is the reference's Length.parse over a wire value, including the
// {value, unit} object form.
func parseLength(value interface{}, defaultUnit string) (units.Length, error) {
	if typed, ok := value.(map[string]interface{}); ok {
		raw, present := typed["value"]
		if !present {
			return units.Length{}, errors.New("'value'")
		}
		magnitude, err := quantity(raw, "length")
		if err != nil {
			return units.Length{}, err
		}
		unit := defaultUnit
		if given, ok := typed["unit"]; ok {
			unit = toText(given)
		}
		return units.LengthOf(magnitude, unit, units.Nearest)
	}
	magnitude, err := quantity(value, "length")
	if err != nil {
		return units.Length{}, err
	}
	return units.ParseLength(magnitude, defaultUnit, units.Nearest)
}

func parseWeight(value interface{}, defaultUnit string) (units.Weight, error) {
	if typed, ok := value.(map[string]interface{}); ok {
		raw, present := typed["value"]
		if !present {
			return units.Weight{}, errors.New("'value'")
		}
		magnitude, err := quantity(raw, "weight")
		if err != nil {
			return units.Weight{}, err
		}
		unit := defaultUnit
		if given, ok := typed["unit"]; ok {
			unit = toText(given)
		}
		return units.WeightOf(magnitude, unit, units.Nearest)
	}
	magnitude, err := quantity(value, "weight")
	if err != nil {
		return units.Weight{}, err
	}
	return units.ParseWeight(magnitude, defaultUnit, units.Nearest)
}
