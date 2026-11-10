package algorithm

import (
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/toxakara/packvium-go/config"
	"github.com/toxakara/packvium-go/domain"
)

// ItemOrderStrategy is a caller-supplied item ordering the portfolio also tries.
type ItemOrderStrategy interface {
	Name() string
	Order(items []domain.ItemInstance, seed int64) []domain.ItemInstance
}

// NamedOrder is one distinct item ordering and the name its starts carry.
type NamedOrder struct {
	Name  string
	Order []domain.ItemInstance
}

// OrderingLead is the keys every built-in item ordering starts with. Priority leads so a
// caller can bias the search. A route is unloaded last-in-first-out, so later stops load
// first to end up underneath; items with no stop ride the whole route and go to the bottom
// (-Inf). Under maximum_value the most valuable item takes first refusal on the space, behind
// priority and ahead of the strategy's own key.
func OrderingLead(instance domain.ItemInstance, cfg *config.PackingConfig) SortKey {
	item := instance.Item()
	stop := math.Inf(-1)
	if index := item.StopIndex(); index != nil {
		stop = -float64(*index)
	}
	key := make(SortKey, 0, 8)
	key = append(key, Int(-item.Priority()), Float(stop))
	if cfg.Objective() == "maximum_value" {
		value := int64(0)
		if declared := item.Value(); declared != nil {
			value = *declared
		}
		key = append(key, Int(-value))
	}
	return key
}

func leadKey(instance domain.ItemInstance, cfg *config.PackingConfig, tail ...KeyPart) SortKey {
	return append(OrderingLead(instance, cfg), tail...)
}

func negated(value *big.Int) *big.Int { return new(big.Int).Neg(value) }

// sortInstances is a stable sort of items by key.
func sortInstances(items []domain.ItemInstance, key func(domain.ItemInstance) SortKey) []domain.ItemInstance {
	order := stableOrder(len(items), func(index int) SortKey { return key(items[index]) })
	out := make([]domain.ItemInstance, len(items))
	for position, index := range order {
		out[position] = items[index]
	}
	return out
}

type orderStrategy struct {
	name string
	key  func(domain.ItemInstance) SortKey
}

// builtInStrategies are keyed behind OrderingLead, which carries priority, route stop and
// value.
func builtInStrategies(cfg *config.PackingConfig) []orderStrategy {
	var strategies []orderStrategy
	if cfg.Profile() == config.Quality {
		strategies = append(strategies,
			orderStrategy{"small_edge", func(item domain.ItemInstance) SortKey {
				dimensions := item.Item().Dimensions()
				return leadKey(item, cfg, Int(dimensions.MaxEdge()), Big(dimensions.Volume()), Str(item.ID()))
			}},
			orderStrategy{"small_volume", func(item domain.ItemInstance) SortKey {
				dimensions := item.Item().Dimensions()
				return leadKey(item, cfg, Big(dimensions.Volume()), Int(dimensions.MaxEdge()), Str(item.ID()))
			}})
	}
	return append(strategies,
		orderStrategy{"volume", func(item domain.ItemInstance) SortKey {
			dimensions := item.Item().Dimensions()
			return leadKey(item, cfg, Big(negated(dimensions.Volume())), Int(-dimensions.MaxEdge()),
				Int(-item.Item().Weight().Ticks()), Str(item.ID()))
		}},
		orderStrategy{"base_area", func(item domain.ItemInstance) SortKey {
			dimensions := item.Item().Dimensions()
			return leadKey(item, cfg, Int(-dimensions.BaseArea()), Big(negated(dimensions.Volume())), Str(item.ID()))
		}},
		orderStrategy{"weight", func(item domain.ItemInstance) SortKey {
			return leadKey(item, cfg, Int(-item.Item().Weight().Ticks()),
				Big(negated(item.Item().Dimensions().Volume())), Str(item.ID()))
		}},
		orderStrategy{"constrained", func(item domain.ItemInstance) SortKey {
			return leadKey(item, cfg, Int(int64(len(item.Item().AllowedRotations()))),
				Float(-item.Item().MinimumSupportRatio()), Big(negated(item.Item().Dimensions().Volume())),
				Str(item.ID()))
		}})
}

// ItemOrders are the distinct item orderings a portfolio tries: built-in strategies first,
// then the caller's, then seeded shuffles. O(s * n log n) for s strategies, plus O(n) per
// shuffle attempt.
func ItemOrders(items []domain.ItemInstance, cfg *config.PackingConfig, custom []ItemOrderStrategy) []NamedOrder {
	limit := cfg.MultiStartOrders()
	if cfg.Profile() == config.Fast {
		limit = 1
	}
	var out []NamedOrder
	seen := map[string]bool{}
	offer := func(name string, order []domain.ItemInstance) bool {
		if signature := orderSignature(order); !seen[signature] {
			seen[signature] = true
			out = append(out, NamedOrder{name, order})
		}
		return int64(len(out)) >= limit
	}
	for _, strategy := range builtInStrategies(cfg) {
		if offer(strategy.name, sortInstances(items, strategy.key)) {
			return out
		}
	}
	for _, strategy := range custom {
		if offer(strategy.Name(), strategy.Order(items, cfg.Seed())) {
			return out
		}
	}
	random := NewDeterministicRandom(cfg.Seed())
	attempts, maximumAttempts := int64(0), limit*16
	if maximumAttempts < 32 {
		maximumAttempts = 32
	}
	for int64(len(out)) < limit && attempts < maximumAttempts {
		attempts++
		permutation := random.Permutation(len(items))
		shuffled := make([]domain.ItemInstance, len(items))
		for position, index := range permutation {
			shuffled[position] = items[index]
		}
		if signature := orderSignature(shuffled); !seen[signature] {
			seen[signature] = true
			out = append(out, NamedOrder{"seeded_" + strconv.Itoa(len(out)), shuffled})
		}
	}
	return out
}

func orderSignature(order []domain.ItemInstance) string {
	ids := make([]string, len(order))
	for index, item := range order {
		ids[index] = item.ID()
	}
	return strings.Join(ids, "|")
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
