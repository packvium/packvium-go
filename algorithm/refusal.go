package algorithm

import (
	"strings"

	"github.com/toxakara/packvium-go/internal/ints"
)

// Refusal carries an error out of the search as a panic value, where the reference throws:
// an unknown objective or solver, an exact-small request over its limit, a bound past its
// ceiling. RecoverRefusal turns it back into an ordinary error at the API boundary, so a
// caller is always handed an error, never a panic.
type Refusal struct{ Err error }

func refuse(err error) { panic(Refusal{err}) }

// RecoverRefusal is deferred at an API boundary: a Refusal or an ints.Overflow becomes *target;
// any other panic is an engine bug and keeps unwinding.
func RecoverRefusal(target *error) {
	recovered := recover()
	if recovered == nil {
		return
	}
	switch value := recovered.(type) {
	case Refusal:
		*target = value.Err
	case ints.Overflow:
		*target = value
	default:
		panic(recovered)
	}
}

// UnknownObjectiveError is an objective name or input this library does not implement.
type UnknownObjectiveError struct{ Message string }

func (e *UnknownObjectiveError) Error() string { return e.Message }

// UnknownSolverError is a solver name this library does not implement.
type UnknownSolverError struct{ Message string }

func (e *UnknownSolverError) Error() string { return e.Message }

// ExactSmallLimitError is an exact_small solve asked to search more items than its limit.
type ExactSmallLimitError struct{}

func (ExactSmallLimitError) Error() string { return "exact-small item limit exceeded" }

// BoundOverflowError is a bound-path sum past the declared ceiling. It is not an invalid
// request: the bound declines to answer rather than carry a number every engine cannot hold
// exactly.
type BoundOverflowError struct{ Message string }

func (e *BoundOverflowError) Error() string { return e.Message }

// pythonList renders names as the reference's str(list) does, e.g. ['a', 'b'].
func pythonList(names []string) string {
	quoted := make([]string, len(names))
	for index, name := range names {
		quoted[index] = pythonRepr(name)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// pythonRepr is repr() of a string: single quotes unless the text holds one and no double
// quote, backslashes and the chosen quote escaped.
func pythonRepr(text string) string {
	quote := "'"
	if strings.Contains(text, "'") && !strings.Contains(text, "\"") {
		quote = "\""
	}
	var out strings.Builder
	out.WriteString(quote)
	for _, char := range text {
		switch {
		case char == '\\':
			out.WriteString("\\\\")
		case string(char) == quote:
			out.WriteString("\\" + quote)
		case char == '\n':
			out.WriteString("\\n")
		case char == '\r':
			out.WriteString("\\r")
		case char == '\t':
			out.WriteString("\\t")
		default:
			out.WriteRune(char)
		}
	}
	out.WriteString(quote)
	return out.String()
}
