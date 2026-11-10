package domain

import "sort"

// TagSet is an immutable set of tags. It is held sorted so that any walk over it is
// deterministic; the reference engine's sets are unordered and nothing may depend on
// their iteration order.
type TagSet struct{ tags []string }

// NewTagSet deduplicates and sorts its input.
func NewTagSet(tags []string) TagSet {
	unique := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		unique[tag] = struct{}{}
	}
	sorted := make([]string, 0, len(unique))
	for tag := range unique {
		sorted = append(sorted, tag)
	}
	sort.Strings(sorted)
	return TagSet{sorted}
}

// Has reports membership in O(log n).
func (s TagSet) Has(tag string) bool {
	index := sort.SearchStrings(s.tags, tag)
	return index < len(s.tags) && s.tags[index] == tag
}

// Len is the number of distinct tags.
func (s TagSet) Len() int { return len(s.tags) }

// Sorted returns the tags in ascending order.
func (s TagSet) Sorted() []string { return append([]string(nil), s.tags...) }

// Intersects reports whether the sets share a tag, in O(n + m).
func (s TagSet) Intersects(other TagSet) bool {
	i, j := 0, 0
	for i < len(s.tags) && j < len(other.tags) {
		switch {
		case s.tags[i] == other.tags[j]:
			return true
		case s.tags[i] < other.tags[j]:
			i++
		default:
			j++
		}
	}
	return false
}
