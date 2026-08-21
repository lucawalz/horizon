package controller

import (
	"slices"
	"testing"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const gibibyte = 1 << 30

func candidate(name string, cores int, memoryGiB int64, rate float64, mutators ...func(*provider.InstanceType)) provider.InstanceType {
	it := provider.InstanceType{
		Name:         name,
		Architecture: string(v1alpha1.ArchitectureX86),
		CPUType:      string(v1alpha1.CPUTypeShared),
		CPUCores:     cores,
		MemoryBytes:  memoryGiB * gibibyte,
		Region:       testRegion,
		Available:    true,
		HourlyRate:   provider.Rate{Amount: rate, Currency: "EUR"},
	}
	for _, mutate := range mutators {
		mutate(&it)
	}
	return it
}

func requirements(mutators ...func(*v1alpha1.SizeRequirements)) v1alpha1.SizeRequirements {
	required := v1alpha1.SizeRequirements{
		MinCPU:       2,
		Architecture: v1alpha1.ArchitectureX86,
		Strategy:     v1alpha1.StrategyLowestPrice,
	}
	for _, mutate := range mutators {
		mutate(&required)
	}
	return required
}

func assertChoice(t *testing.T, offered []provider.InstanceType, required v1alpha1.SizeRequirements, want string) {
	t.Helper()
	chosen, ok := selectInstanceType(offered, required)
	if !ok {
		t.Fatalf("no instance type was chosen from %d candidates, want %q", len(offered), want)
	}
	if chosen.Name != want {
		t.Errorf("chose %q, want %q", chosen.Name, want)
	}
}

func permutations(types []provider.InstanceType) [][]provider.InstanceType {
	if len(types) <= 1 {
		return [][]provider.InstanceType{slices.Clone(types)}
	}
	var out [][]provider.InstanceType
	for i := range types {
		rest := slices.Concat(types[:i:i], types[i+1:])
		for _, tail := range permutations(rest) {
			out = append(out, append([]provider.InstanceType{types[i]}, tail...))
		}
	}
	return out
}

func TestSelectionExcludesEveryTypeThatMissesARequirement(t *testing.T) {
	tests := []struct {
		name  string
		spoil func(*provider.InstanceType)
	}{
		{"unavailable", func(it *provider.InstanceType) { it.Available = false }},
		{"deprecated", func(it *provider.InstanceType) { it.Deprecated = true }},
		{"another architecture", func(it *provider.InstanceType) { it.Architecture = string(v1alpha1.ArchitectureARM) }},
		{"too few cores", func(it *provider.InstanceType) { it.CPUCores = 1 }},
		{"too little memory", func(it *provider.InstanceType) { it.MemoryBytes = gibibyte }},
		{"another cpu type", func(it *provider.InstanceType) { it.CPUType = string(v1alpha1.CPUTypeDedicated) }},
	}

	required := requirements(func(r *v1alpha1.SizeRequirements) {
		r.MinMemory = quantity("4Gi")
		r.CPUType = v1alpha1.CPUTypeShared
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			offered := []provider.InstanceType{candidate("only", 2, 4, 0.01, tc.spoil)}
			if chosen, ok := selectInstanceType(offered, required); ok {
				t.Errorf("chose %q, want no candidate to survive the filter", chosen.Name)
			}
		})
	}
}

func TestSelectionKeepsATypeThatMeetsEveryRequirementExactly(t *testing.T) {
	required := requirements(func(r *v1alpha1.SizeRequirements) {
		r.MinMemory = quantity("4Gi")
		r.CPUType = v1alpha1.CPUTypeShared
	})

	assertChoice(t, []provider.InstanceType{candidate("only", 2, 4, 0.01)}, required, "only")
}

func TestAnUnsetCPUTypeAdmitsEveryCPUType(t *testing.T) {
	dedicated := candidate("dedicated", 2, 4, 0.01, func(it *provider.InstanceType) {
		it.CPUType = string(v1alpha1.CPUTypeDedicated)
	})

	assertChoice(t, []provider.InstanceType{dedicated}, requirements(), "dedicated")
}

func TestAnUnsetMemoryFloorAdmitsATypeWithAlmostNoMemory(t *testing.T) {
	assertChoice(t, []provider.InstanceType{candidate("tiny", 2, 0, 0.01)}, requirements(), "tiny")
}

func TestLowestPriceChoosesTheCheapestMachine(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("wide", 8, 16, 0.04),
		candidate("cheap", 2, 4, 0.01),
		candidate("middling", 4, 8, 0.03),
	}

	assertChoice(t, offered, requirements(), "cheap")
}

func TestLowestPricePerCoreChoosesTheCheapestCoreNotTheCheapestMachine(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("wide", 8, 16, 0.04),
		candidate("cheap", 2, 4, 0.02),
	}
	perCore := requirements(func(r *v1alpha1.SizeRequirements) { r.Strategy = v1alpha1.StrategyLowestPricePerCore })

	assertChoice(t, offered, perCore, "wide")
	assertChoice(t, offered, requirements(), "cheap")
}

func TestATieOnStrategyKeyIsBrokenByTheHourlyRate(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("wide", 8, 8, 0.04),
		candidate("narrow", 2, 8, 0.01),
	}
	perCore := requirements(func(r *v1alpha1.SizeRequirements) { r.Strategy = v1alpha1.StrategyLowestPricePerCore })

	assertChoice(t, offered, perCore, "narrow")
}

func TestATieOnTheHourlyRateIsBrokenByCoresDescending(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("narrow", 2, 8, 0.02),
		candidate("wide", 4, 8, 0.02),
	}

	assertChoice(t, offered, requirements(), "wide")
}

func TestATieOnRateAndCoresIsBrokenByMemoryDescending(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("lean", 4, 8, 0.02),
		candidate("roomy", 4, 16, 0.02),
	}

	assertChoice(t, offered, requirements(), "roomy")
}

func TestIdenticalCandidatesAlwaysYieldTheSameMachine(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("beta", 4, 8, 0.02),
		candidate("alpha", 4, 8, 0.02),
		candidate("gamma", 4, 8, 0.02),
	}

	orders := permutations(offered)
	if len(orders) != 6 {
		t.Fatalf("the catalogue was shuffled %d ways, want every one of 6", len(orders))
	}
	for _, order := range orders {
		names := make([]string, 0, len(order))
		for _, it := range order {
			names = append(names, it.Name)
		}
		chosen, ok := selectInstanceType(order, requirements())
		if !ok {
			t.Fatalf("no instance type was chosen from %v", names)
		}
		if chosen.Name != "alpha" {
			t.Errorf("catalogue order %v chose %q, want %q", names, chosen.Name, "alpha")
		}
	}
}
