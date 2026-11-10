package algorithm

import "github.com/packvium/packvium-go/domain"

// Batches groups items for atomic offering: items in a group share a container, so a batch
// that does not fit is rejected whole and leaves the rest of the order untouched. Each group
// sits where its first member does. O(n).
func Batches(items []domain.ItemInstance) [][]domain.ItemInstance {
	groups := map[string][]domain.ItemInstance{}
	for _, item := range items {
		if group := item.Item().Group(); group != nil {
			groups[*group] = append(groups[*group], item)
		}
	}
	emitted := map[string]bool{}
	var batches [][]domain.ItemInstance
	for _, item := range items {
		group := item.Item().Group()
		if group == nil {
			batches = append(batches, []domain.ItemInstance{item})
			continue
		}
		if !emitted[*group] {
			emitted[*group] = true
			batches = append(batches, groups[*group])
		}
	}
	return batches
}

// Flatten concatenates batches in order.
func Flatten(batches [][]domain.ItemInstance) []domain.ItemInstance {
	var out []domain.ItemInstance
	for _, batch := range batches {
		out = append(out, batch...)
	}
	return out
}

func concatInstances(left, right []domain.ItemInstance) []domain.ItemInstance {
	joined := make([]domain.ItemInstance, 0, len(left)+len(right))
	joined = append(joined, left...)
	return append(joined, right...)
}

// Space is an empty maximal box inside a container.
type Space struct {
	Origin     domain.Point
	Dimensions domain.Dimensions
}

func (s Space) box() domain.Box { return domain.NewBox(s.Origin, s.Dimensions) }
