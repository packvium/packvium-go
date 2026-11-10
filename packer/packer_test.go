package packer

import (
	"testing"

	"github.com/toxakara/packvium-go/algorithm"
	"github.com/toxakara/packvium-go/config"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/result"
	"github.com/toxakara/packvium-go/units"
)

func cube(t *testing.T, id string, sideMM, quantity int64) *domain.Item {
	t.Helper()
	side := sideMM * units.TicksPerMM
	spec := domain.NewItemSpec(id, domain.MustDimensions(side, side, side))
	spec.Quantity = quantity
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func box(t *testing.T, id string, sideMM int64) *domain.Container {
	t.Helper()
	side := sideMM * units.TicksPerMM
	container, err := domain.NewContainer(domain.ContainerSpec{ID: id, InnerDimensions: domain.MustDimensions(side, side, side)})
	if err != nil {
		t.Fatal(err)
	}
	return container
}

func configOf(t *testing.T, options config.Options) *config.PackingConfig {
	t.Helper()
	cfg, err := config.New(options)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPacksEveryCubeIntoOneContainer(t *testing.T) {
	packed, err := New(configOf(t, config.Default(config.Balanced)), nil).
		Pack([]*domain.Item{cube(t, "cube", 50, 8)}, []*domain.Container{box(t, "carton", 100)})
	if err != nil {
		t.Fatal(err)
	}
	if !packed.Complete() || len(packed.Containers) != 1 || packed.PackedItemCount() != 8 {
		t.Fatalf("status %s, %d containers, %d packed", packed.Status, len(packed.Containers), packed.PackedItemCount())
	}
	if packed.Status == result.InvalidResult {
		t.Fatal(packed.Warnings)
	}
}

func TestReportsAnItemTooLargeForEveryContainerAsProven(t *testing.T) {
	packed, err := New(configOf(t, config.Default(config.Fast)), algorithm.NoExtensions()).
		Pack([]*domain.Item{cube(t, "ladder", 200, 1)}, []*domain.Container{box(t, "carton", 100)})
	if err != nil {
		t.Fatal(err)
	}
	if len(packed.Unpacked) != 1 || packed.Unpacked[0].Reason != "no_compatible_container_dimensions" ||
		packed.Unpacked[0].Proof.Level != "proven" {
		t.Fatalf("%+v", packed.Unpacked)
	}
}

func TestReturnsRefusalsAsErrors(t *testing.T) {
	options := config.Default(config.Balanced)
	options.Solvers = []string{"exact_small"}
	options.ExactItemLimit = 2
	_, err := New(configOf(t, options), nil).Pack([]*domain.Item{cube(t, "cube", 10, 3)},
		[]*domain.Container{box(t, "carton", 100)})
	if err == nil || err.Error() != "exact-small item limit exceeded" {
		t.Fatalf("got %v", err)
	}
	options = config.Default(config.Balanced)
	options.Solvers = []string{"nope"}
	_, err = New(configOf(t, options), nil).Pack([]*domain.Item{cube(t, "cube", 10, 1)},
		[]*domain.Container{box(t, "carton", 100)})
	want := "unknown solver name(s) ['nope']; expected one of ['exact_small', 'extreme_points', 'grid', " +
		"'homogeneous_blocks', 'layer', 'maximal_spaces']"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	options = config.Default(config.Balanced)
	options.Objective = "shipping_cost"
	_, err = New(configOf(t, options), nil).Pack([]*domain.Item{cube(t, "cube", 10, 1)},
		[]*domain.Container{box(t, "carton", 100)})
	if err == nil || err.Error() != "the shipping_cost objective requires configuration.dimensional_weight_divisor" {
		t.Fatalf("got %v", err)
	}
}
