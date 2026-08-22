package controller

import (
	"cmp"
	"slices"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const rateRoundTripPrecision = -1

type rejectionReason string

const (
	rejectedUnavailable  rejectionReason = "Unavailable"
	rejectedDeprecated   rejectionReason = "Deprecated"
	rejectedArchitecture rejectionReason = "Architecture"
	rejectedCores        rejectionReason = "Cores"
	rejectedMemory       rejectionReason = "Memory"
	rejectedCPUType      rejectionReason = "CPUType"
)

type selectionDecision struct {
	Chosen    provider.InstanceType
	Strategy  v1alpha1.SizingStrategy
	RunnerUp  *provider.InstanceType
	Offered   int
	Qualified int
	Rejected  map[rejectionReason]int
}

func (d selectionDecision) hasWinner() bool {
	return d.Qualified > 0
}

func (d selectionDecision) runnerUpName() string {
	if d.RunnerUp == nil {
		return ""
	}
	return d.RunnerUp.Name
}

func (d selectionDecision) margin() float64 {
	if d.RunnerUp == nil {
		return 0
	}
	key := strategyKey(d.Strategy)
	return key(*d.RunnerUp) - key(d.Chosen)
}

func selectInstanceType(offered []provider.InstanceType, required v1alpha1.SizeRequirements) selectionDecision {
	decision := selectionDecision{
		Strategy: effectiveStrategy(required.Strategy),
		Offered:  len(offered),
		Rejected: map[rejectionReason]int{},
	}

	candidates := make([]provider.InstanceType, 0, len(offered))
	for _, it := range offered {
		if reason := rejectionFor(it, required); reason != "" {
			decision.Rejected[reason]++
			continue
		}
		candidates = append(candidates, it)
	}

	decision.Qualified = len(candidates)
	if len(candidates) == 0 {
		return decision
	}

	slices.SortFunc(candidates, preference(decision.Strategy))
	decision.Chosen = candidates[0]
	if len(candidates) > 1 {
		decision.RunnerUp = &candidates[1]
	}
	return decision
}

func rejectionFor(it provider.InstanceType, required v1alpha1.SizeRequirements) rejectionReason {
	switch {
	case !it.Available:
		return rejectedUnavailable
	case it.Deprecated:
		return rejectedDeprecated
	case it.Architecture != string(required.Architecture):
		return rejectedArchitecture
	case it.CPUCores < int(required.MinCPU):
		return rejectedCores
	case required.MinMemory != nil && it.MemoryBytes < required.MinMemory.Value():
		return rejectedMemory
	case required.CPUType != "" && it.CPUType != string(required.CPUType):
		return rejectedCPUType
	default:
		return ""
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

func effectiveStrategy(strategy v1alpha1.SizingStrategy) v1alpha1.SizingStrategy {
	if strategy == v1alpha1.StrategyLowestPricePerCore {
		return v1alpha1.StrategyLowestPricePerCore
	}
	return v1alpha1.StrategyLowestPrice
}

func strategyKey(strategy v1alpha1.SizingStrategy) func(provider.InstanceType) float64 {
	if effectiveStrategy(strategy) == v1alpha1.StrategyLowestPricePerCore {
		return func(it provider.InstanceType) float64 { return it.HourlyRate.Amount / float64(it.CPUCores) }
	}
	return func(it provider.InstanceType) float64 { return it.HourlyRate.Amount }
}

func selectionStatus(decision selectionDecision, decided time.Time) *v1alpha1.SelectionStatus {
	return &v1alpha1.SelectionStatus{
		Strategy:   decision.Strategy,
		Chosen:     decision.Chosen.Name,
		HourlyRate: strconv.FormatFloat(decision.Chosen.HourlyRate.Amount, 'f', rateRoundTripPrecision, 64),
		Currency:   decision.Chosen.HourlyRate.Currency,
		RunnerUp:   decision.runnerUpName(),
		Offered:    int32(decision.Offered),
		Qualified:  int32(decision.Qualified),
		Rejected:   rejectedCounts(decision.Rejected),
		DecidedAt:  metav1.Time{Time: decided},
	}
}

func rejectedCounts(tally map[rejectionReason]int) []v1alpha1.RejectedCandidates {
	if len(tally) == 0 {
		return nil
	}
	counts := make([]v1alpha1.RejectedCandidates, 0, len(tally))
	for reason, count := range tally {
		counts = append(counts, v1alpha1.RejectedCandidates{Reason: string(reason), Count: int32(count)})
	}
	slices.SortFunc(counts, func(a, b v1alpha1.RejectedCandidates) int { return cmp.Compare(a.Reason, b.Reason) })
	return counts
}
