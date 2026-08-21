package controller

import (
	"cmp"
	"slices"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

func selectInstanceType(offered []provider.InstanceType, required v1alpha1.SizeRequirements) (provider.InstanceType, bool) {
	candidates := make([]provider.InstanceType, 0, len(offered))
	for _, it := range offered {
		if satisfies(it, required) {
			candidates = append(candidates, it)
		}
	}
	if len(candidates) == 0 {
		return provider.InstanceType{}, false
	}
	return slices.MinFunc(candidates, preference(required.Strategy)), true
}

func satisfies(it provider.InstanceType, required v1alpha1.SizeRequirements) bool {
	switch {
	case !it.Available || it.Deprecated:
		return false
	case it.Architecture != string(required.Architecture):
		return false
	case it.CPUCores < int(required.MinCPU):
		return false
	case required.MinMemory != nil && it.MemoryBytes < required.MinMemory.Value():
		return false
	default:
		return required.CPUType == "" || it.CPUType == string(required.CPUType)
	}
}

// the name terminates the chain, so the comparator is a total order and selection cannot alternate between passes
func preference(strategy v1alpha1.SizingStrategy) func(a, b provider.InstanceType) int {
	key := strategyKey(strategy)
	return func(a, b provider.InstanceType) int {
		return cmp.Or(
			cmp.Compare(key(a), key(b)),
			cmp.Compare(a.HourlyRate.Amount, b.HourlyRate.Amount),
			cmp.Compare(b.CPUCores, a.CPUCores),
			cmp.Compare(b.MemoryBytes, a.MemoryBytes),
			cmp.Compare(a.Name, b.Name),
		)
	}
}

func strategyKey(strategy v1alpha1.SizingStrategy) func(provider.InstanceType) float64 {
	if strategy == v1alpha1.StrategyLowestPricePerCore {
		return func(it provider.InstanceType) float64 { return it.HourlyRate.Amount / float64(it.CPUCores) }
	}
	return func(it provider.InstanceType) float64 { return it.HourlyRate.Amount }
}
