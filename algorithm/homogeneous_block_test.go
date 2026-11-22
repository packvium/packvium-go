package algorithm

import (
	"testing"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
)

func blockTestContainer(t *testing.T) *domain.Container {
	t.Helper()
	quantity := int64(1)
	box, err := domain.NewContainer(domain.ContainerSpec{ID: "box",
		InnerDimensions: domain.MustDimensions(mm(100), mm(100), mm(200)), Quantity: &quantity})
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func blockTestConfig(t *testing.T, options config.Options) *config.PackingConfig {
	t.Helper()
	cfg, err := config.New(options)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func sponges(t *testing.T) []domain.ItemInstance {
	t.Helper()
	spec := domain.NewItemSpec("sponge", domain.MustDimensions(mm(100), mm(100), mm(100)))
	ratio, pressure := int64(200_000), int64(50)
	spec.Quantity, spec.ShapeType = 2, domain.Compressible
	spec.CompressionRatioPPM, spec.MaxCompressionPressureKPA = &ratio, &pressure
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	return item.Instances()
}

func cubes(t *testing.T) []domain.ItemInstance {
	t.Helper()
	spec := domain.NewItemSpec("cube", domain.MustDimensions(mm(100), mm(100), mm(100)))
	spec.Quantity = 2
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	return item.Instances()
}

func TestBlockSearchFallsBackForCompressibleItems(t *testing.T) {
	box, order := blockTestContainer(t), sponges(t)
	cfg := blockTestConfig(t, config.Default(config.Quality))
	solver := NewHomogeneousBlockSolver(nil)
	if solver.supports(box, order, cfg) {
		t.Fatal("compressible items were laid out as a rigid block")
	}
	solution := solver.PackOne(box, 1, order, cfg, &SearchStats{}, DeadlineOfMilliseconds(60_000))
	if len(solution.State.Placements) != 2 || len(solution.Unpacked) != 0 {
		t.Fatalf("placed %d, unpacked %d", len(solution.State.Placements), len(solution.Unpacked))
	}
}

func TestBlockSearchLeavesARequestedSupportRatioToThePerItemSearch(t *testing.T) {
	box, order := blockTestContainer(t), cubes(t)
	solver := NewHomogeneousBlockSolver(nil)
	if !solver.supports(box, order, blockTestConfig(t, config.Default(config.Quality))) {
		t.Fatal("plain cubes with no support rule are not a block")
	}
	options := config.Default(config.Quality)
	options.MinimumSupportRatio = 0.5
	if solver.supports(box, order, blockTestConfig(t, options)) {
		t.Fatal("a requested support ratio was left to the block solver")
	}
}
