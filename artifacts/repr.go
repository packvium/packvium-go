package artifacts

import (
	"fmt"
	"math/big"

	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/internal/pyrepr"
)

// repr quotes a decoded value in a message the way the reference's repr() does for scalars.
func repr(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case string:
		return pyrepr.String(typed)
	case int, int64, *big.Int:
		return fmt.Sprint(typed)
	case float64:
		return jsonvalue.FloatRepr(typed)
	}
	return fmt.Sprintf("%v", value)
}

// typeName is the reference's type(value).__name__ for a decoded value.
func typeName(value interface{}) string {
	switch value.(type) {
	case bool:
		return "bool"
	case float64:
		return "float"
	case map[string]interface{}:
		return "dict"
	case []interface{}:
		return "list"
	}
	return fmt.Sprintf("%T", value)
}
