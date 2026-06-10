package runner_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lucawalz/horizon/internal/runner"
)

func TestRunnerSuccess(t *testing.T) {
	var order []string
	r := &runner.Runner{}
	for _, name := range []string{"a", "b", "c"} {
		n := name
		r.Add(runner.Step{
			Name: n,
			Run: func(ctx context.Context) error {
				order = append(order, n)
				return nil
			},
		})
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("wrong order: %v", order)
	}
}

func TestRunnerRollback(t *testing.T) {
	var rolled []string
	errB := errors.New("b failed")
	r := &runner.Runner{}

	r.Add(runner.Step{
		Name: "step-a",
		Run:  func(ctx context.Context) error { return nil },
		Rollback: func(ctx context.Context) error {
			rolled = append(rolled, "step-a")
			return nil
		},
	})
	r.Add(runner.Step{
		Name: "step-b",
		Run:  func(ctx context.Context) error { return errB },
		Rollback: func(ctx context.Context) error {
			rolled = append(rolled, "step-b")
			return nil
		},
	})

	err := r.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errB) {
		t.Fatalf("error chain broken: %v", err)
	}
	if len(rolled) != 1 || rolled[0] != "step-a" {
		t.Fatalf("wrong rollbacks: %v", rolled)
	}
}

func TestRunnerRollbackOrder(t *testing.T) {
	var rolled []string
	errD := errors.New("d failed")
	r := &runner.Runner{}

	for _, name := range []string{"a", "b", "c"} {
		n := name
		r.Add(runner.Step{
			Name: n,
			Run:  func(ctx context.Context) error { return nil },
			Rollback: func(ctx context.Context) error {
				rolled = append(rolled, n)
				return nil
			},
		})
	}
	r.Add(runner.Step{
		Name: "d",
		Run:  func(ctx context.Context) error { return errD },
		Rollback: func(ctx context.Context) error {
			rolled = append(rolled, "d")
			return nil
		},
	})

	if err := r.Run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if len(rolled) != 3 || rolled[0] != "c" || rolled[1] != "b" || rolled[2] != "a" {
		t.Fatalf("wrong rollback order: %v", rolled)
	}
}

func TestRunnerNilRollback(t *testing.T) {
	errB := errors.New("b failed")
	r := &runner.Runner{}

	r.Add(runner.Step{
		Name:     "a",
		Run:      func(ctx context.Context) error { return nil },
		Rollback: nil,
	})
	r.Add(runner.Step{
		Name: "b",
		Run:  func(ctx context.Context) error { return errB },
	})

	if err := r.Run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRollbackRunsWithLiveContext(t *testing.T) {
	var rollbackErr error
	rollbackErr = errors.New("not run")
	failure := errors.New("boom")
	r := &runner.Runner{}

	r.Add(runner.Step{
		Name: "destroy-step",
		Run:  func(ctx context.Context) error { return nil },
		Rollback: func(ctx context.Context) error {
			rollbackErr = ctx.Err()
			return nil
		},
	})
	r.Add(runner.Step{
		Name: "fail-step",
		Run:  func(ctx context.Context) error { return failure },
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := r.Run(ctx); err == nil {
		t.Fatal("expected error")
	}
	if rollbackErr != nil {
		t.Fatalf("rollback ctx.Err() = %v, want nil despite cancelled parent", rollbackErr)
	}
}

func TestRunnerErrorWrapping(t *testing.T) {
	sentinel := errors.New("underlying")
	r := &runner.Runner{}
	r.Add(runner.Step{
		Name: "step-x",
		Run:  func(ctx context.Context) error { return sentinel },
	})

	err := r.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("sentinel not in chain: %v", err)
	}
	if err.Error()[:len("step-x:")] != "step-x:" {
		t.Fatalf("step name not prefix: %v", err.Error())
	}
}
