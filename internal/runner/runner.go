package runner

import (
	"context"
	"fmt"
	"os"
)

type Step struct {
	Name     string
	Run      func(ctx context.Context) error
	Rollback func(ctx context.Context) error
}

type Runner struct {
	steps []Step
	done  []int
}

func (r *Runner) Add(s Step) { r.steps = append(r.steps, s) }

func (r *Runner) Run(ctx context.Context) error {
	for i, s := range r.steps {
		fmt.Fprintf(os.Stderr, "==> %s\n", s.Name)
		if err := s.Run(ctx); err != nil {
			r.rollback(ctx)
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		r.done = append(r.done, i)
	}
	return nil
}

func (r *Runner) rollback(ctx context.Context) {
	for i := len(r.done) - 1; i >= 0; i-- {
		s := r.steps[r.done[i]]
		if s.Rollback != nil {
			if rbErr := s.Rollback(ctx); rbErr != nil {
				fmt.Fprintf(os.Stderr, "rollback %s: %v\n", s.Name, rbErr)
			}
		}
	}
}
