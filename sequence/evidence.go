package sequence

import "sort"

// SequenceStep is one step of a generated order with its evidence: which placements it
// structurally depended on, and which direction its accessibility check used.
type SequenceStep struct {
	index     int
	direction string
	dependsOn []int
}

// NewSequenceStep records one step; dependsOn is held sorted.
func NewSequenceStep(index int, direction string, dependsOn []int) SequenceStep {
	return SequenceStep{index, direction, sortedCopy(dependsOn)}
}

func (s SequenceStep) Index() int        { return s.index }
func (s SequenceStep) Direction() string { return s.direction }
func (s SequenceStep) DependsOn() []int  { return append([]int(nil), s.dependsOn...) }

// ToMap is the wire form, depends_on ascending.
func (s SequenceStep) ToMap() map[string]interface{} {
	return map[string]interface{}{"index": int64(s.index), "direction": s.direction, "depends_on": wireIndexes(s.dependsOn)}
}

// SequenceWarning is a non-fatal, localisation-ready instruction warning.
type SequenceWarning struct {
	code       string
	index      int64
	messageKey string
	arguments  map[string]string
}

// NewSequenceWarning copies its arguments.
func NewSequenceWarning(code string, index int64, messageKey string, arguments map[string]string) SequenceWarning {
	copied := make(map[string]string, len(arguments))
	for key, value := range arguments {
		copied[key] = value
	}
	return SequenceWarning{code, index, messageKey, copied}
}

func (w SequenceWarning) Code() string       { return w.code }
func (w SequenceWarning) Index() int64       { return w.index }
func (w SequenceWarning) MessageKey() string { return w.messageKey }

// ArgumentKeys is the argument names in code point order, the order they render in.
func (w SequenceWarning) ArgumentKeys() []string {
	keys := make([]string, 0, len(w.arguments))
	for key := range w.arguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Argument is one argument's value.
func (w SequenceWarning) Argument(key string) (string, bool) {
	value, found := w.arguments[key]
	return value, found
}

// ToMap is the wire form. The encoder sorts object keys, which is the reference's own
// argument order.
func (w SequenceWarning) ToMap() map[string]interface{} {
	arguments := make(map[string]interface{}, len(w.arguments))
	for key, value := range w.arguments {
		arguments[key] = value
	}
	return map[string]interface{}{"code": w.code, "index": w.index, "message_key": w.messageKey, "arguments": arguments}
}

// Reachability is whether a placement could be reached and removed right now, given the
// full scene as it stands, its contact-graph dependents and the declared route: a snapshot,
// "if the door opened now, what could come out first". Reachable is true only when none of
// the three blocks it; BlockedByNeighbors is populated only when every allowed direction is
// blocked, since one clear direction is enough to leave. Every set is held ascending.
type Reachability struct {
	index              int
	reachable          bool
	blockedBySupport   []int
	blockedByNeighbors []int
	blockedByRoute     []int
}

func (r Reachability) Index() int                { return r.index }
func (r Reachability) Reachable() bool           { return r.reachable }
func (r Reachability) BlockedBySupport() []int   { return append([]int(nil), r.blockedBySupport...) }
func (r Reachability) BlockedByNeighbors() []int { return append([]int(nil), r.blockedByNeighbors...) }
func (r Reachability) BlockedByRoute() []int     { return append([]int(nil), r.blockedByRoute...) }

// ToMap is the wire form.
func (r Reachability) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"index": int64(r.index), "reachable": r.reachable,
		"blocked_by_support": wireIndexes(r.blockedBySupport), "blocked_by_neighbors": wireIndexes(r.blockedByNeighbors),
		"blocked_by_route": wireIndexes(r.blockedByRoute),
	}
}
