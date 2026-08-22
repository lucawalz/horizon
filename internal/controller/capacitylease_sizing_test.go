package controller

import (
	"slices"
	"testing"

	"github.com/lucawalz/horizon/api/v1alpha1"
	"github.com/lucawalz/horizon/internal/provider"
)

const (
	gibibyte       = 1 << 30
	mapWalkPasses  = 64
	permutationsOf = 6
)

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
	decision := selectInstanceType(offered, required)
	if !decision.hasWinner() {
		t.Fatalf("no instance type was chosen from %d candidates, want %q", len(offered), want)
	}
	if decision.Chosen.Name != want {
		t.Errorf("chose %q, want %q", decision.Chosen.Name, want)
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

func TestSelectionNamesTheFilterThatRejectedEachCandidate(t *testing.T) {
	tests := []struct {
		name  string
		spoil func(*provider.InstanceType)
		want  rejectionReason
	}{
		{"unavailable", func(it *provider.InstanceType) { it.Available = false }, rejectedUnavailable},
		{"deprecated", func(it *provider.InstanceType) { it.Deprecated = true }, rejectedDeprecated},
		{
			"another architecture",
			func(it *provider.InstanceType) { it.Architecture = string(v1alpha1.ArchitectureARM) },
			rejectedArchitecture,
		},
		{"too few cores", func(it *provider.InstanceType) { it.CPUCores = 1 }, rejectedCores},
		{"too little memory", func(it *provider.InstanceType) { it.MemoryBytes = gibibyte }, rejectedMemory},
		{
			"another cpu type",
			func(it *provider.InstanceType) { it.CPUType = string(v1alpha1.CPUTypeDedicated) },
			rejectedCPUType,
		},
	}

	required := requirements(func(r *v1alpha1.SizeRequirements) {
		r.MinMemory = quantity("4Gi")
		r.CPUType = v1alpha1.CPUTypeShared
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			offered := []provider.InstanceType{candidate("only", 2, 4, 0.01, tc.spoil)}
			decision := selectInstanceType(offered, required)
			if decision.hasWinner() {
				t.Fatalf("chose %q, want no candidate to survive the filter", decision.Chosen.Name)
			}
			if got := decision.Rejected[tc.want]; got != 1 {
				t.Errorf("the tally is %v, want one candidate rejected for %s", decision.Rejected, tc.want)
			}
		})
	}
}

func TestADeprecatedTypeIsRejectedForBeingDeprecatedNotForBeingUnavailable(t *testing.T) {
	offered := []provider.InstanceType{candidate("retiring", 2, 4, 0.01, func(it *provider.InstanceType) {
		it.Deprecated = true
	})}

	decision := selectInstanceType(offered, requirements())

	if decision.Rejected[rejectedUnavailable] != 0 {
		t.Errorf("the tally is %v, want deprecation counted apart from unavailability", decision.Rejected)
	}
}

func TestASelectionRecordsTheRunnerUpAndEveryCandidateItBeat(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("wide", 8, 16, 0.04),
		candidate("cheap", 2, 4, 0.01),
		candidate("middling", 4, 8, 0.03),
	}

	decision := selectInstanceType(offered, requirements())

	if decision.Chosen.Name != "cheap" {
		t.Errorf("chose %q, want %q", decision.Chosen.Name, "cheap")
	}
	if decision.RunnerUp == nil || decision.RunnerUp.Name != "middling" {
		t.Errorf("runner-up is %v, want %q", decision.RunnerUp, "middling")
	}
	if decision.Qualified != 3 {
		t.Errorf("%d candidates qualified, want %d", decision.Qualified, 3)
	}
	if decision.Offered != 3 {
		t.Errorf("the catalogue offered %d candidates, want %d", decision.Offered, 3)
	}
}

func TestTheOnlyQualifyingCandidateHasNoRunnerUp(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("only", 2, 4, 0.01),
		candidate("arm", 2, 4, 0.005, func(it *provider.InstanceType) {
			it.Architecture = string(v1alpha1.ArchitectureARM)
		}),
	}

	decision := selectInstanceType(offered, requirements())

	if decision.Chosen.Name != "only" {
		t.Errorf("chose %q, want %q", decision.Chosen.Name, "only")
	}
	if decision.RunnerUp != nil {
		t.Errorf("runner-up is %q, want none", decision.RunnerUp.Name)
	}
	if decision.Qualified != 1 {
		t.Errorf("%d candidates qualified, want %d", decision.Qualified, 1)
	}
	if decision.Offered != 2 {
		t.Errorf("the catalogue offered %d candidates, want %d", decision.Offered, 2)
	}
}

func TestEachStrategyRecordsItselfAlongsideTheMachineItChose(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("wide", 8, 16, 0.04),
		candidate("cheap", 2, 4, 0.02),
	}
	tests := []struct {
		strategy v1alpha1.SizingStrategy
		want     string
	}{
		{v1alpha1.StrategyLowestPrice, "cheap"},
		{v1alpha1.StrategyLowestPricePerCore, "wide"},
	}

	for _, tc := range tests {
		t.Run(string(tc.strategy), func(t *testing.T) {
			decision := selectInstanceType(offered, requirements(func(r *v1alpha1.SizeRequirements) {
				r.Strategy = tc.strategy
			}))

			if decision.Chosen.Name != tc.want {
				t.Errorf("chose %q, want %q", decision.Chosen.Name, tc.want)
			}
			if decision.Strategy != tc.strategy {
				t.Errorf("recorded strategy %q, want %q", decision.Strategy, tc.strategy)
			}
		})
	}
}

func TestAnUnsetStrategyIsRecordedAsTheOneItFellBackOn(t *testing.T) {
	decision := selectInstanceType([]provider.InstanceType{candidate("only", 2, 4, 0.01)},
		requirements(func(r *v1alpha1.SizeRequirements) { r.Strategy = "" }))

	if decision.Strategy != v1alpha1.StrategyLowestPrice {
		t.Errorf("recorded strategy %q, want %q", decision.Strategy, v1alpha1.StrategyLowestPrice)
	}
}

func TestTheRejectedTallyIsOrderedByReasonHoweverTheMapIsWalked(t *testing.T) {
	tally := map[rejectionReason]int{
		rejectedMemory:       1,
		rejectedCores:        2,
		rejectedArchitecture: 3,
		rejectedCPUType:      4,
		rejectedDeprecated:   5,
		rejectedUnavailable:  6,
	}
	want := []string{"Architecture", "CPUType", "Cores", "Deprecated", "Memory", "Unavailable"}

	for pass := range mapWalkPasses {
		var got []string
		for _, entry := range rejectedCounts(tally) {
			got = append(got, entry.Reason)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("pass %d ordered the tally %v, want %v", pass, got, want)
		}
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
	if len(orders) != permutationsOf {
		t.Fatalf("the catalogue was shuffled %d ways, want every one of %d", len(orders), permutationsOf)
	}
	for _, order := range orders {
		names := make([]string, 0, len(order))
		for _, it := range order {
			names = append(names, it.Name)
		}
		decision := selectInstanceType(order, requirements())
		if !decision.hasWinner() {
			t.Fatalf("no instance type was chosen from %v", names)
		}
		if decision.Chosen.Name != "alpha" {
			t.Errorf("catalogue order %v chose %q, want %q", names, decision.Chosen.Name, "alpha")
		}
		if decision.RunnerUp == nil || decision.RunnerUp.Name != "beta" {
			t.Errorf("catalogue order %v ranked %v second, want %q", names, decision.RunnerUp, "beta")
		}
	}
}

func TestTheMarginOverTheRunnerUpIsMeasuredInTheStrategysOwnUnits(t *testing.T) {
	offered := []provider.InstanceType{
		candidate("wide", 8, 16, 0.5),
		candidate("narrow", 2, 4, 0.25),
	}
	tests := []struct {
		strategy v1alpha1.SizingStrategy
		want     float64
	}{
		{v1alpha1.StrategyLowestPrice, 0.25},
		{v1alpha1.StrategyLowestPricePerCore, 0.0625},
	}

	for _, tc := range tests {
		t.Run(string(tc.strategy), func(t *testing.T) {
			decision := selectInstanceType(offered, requirements(func(r *v1alpha1.SizeRequirements) {
				r.Strategy = tc.strategy
			}))

			if got := decision.margin(); got != tc.want {
				t.Errorf("the margin over %q is %v, want %v", decision.runnerUpName(), got, tc.want)
			}
		})
	}
}

func TestALoneCandidateBeatsNothingSoItsMarginIsZero(t *testing.T) {
	decision := selectInstanceType([]provider.InstanceType{candidate("only", 2, 4, 0.25)}, requirements())

	if got := decision.margin(); got != 0 {
		t.Errorf("the margin over no runner-up is %v, want 0", got)
	}
}
