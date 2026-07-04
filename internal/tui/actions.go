package tui

import (
	"context"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucawalz/horizon/internal/core"
	"github.com/lucawalz/horizon/internal/k8s"
)

const (
	actionTimeout = 15 * time.Minute
	drainTimeout  = 5 * time.Minute

	progressBuffer = 256
)

func (m model) runScaleUp(target core.PoolTarget) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		if target.PoolType == core.ElasticPoolType {
			return "", core.ElasticAutoscalerErr()
		}
		hc, spec, err := app.ReservedClient()
		if err != nil {
			return "", err
		}
		return "", core.ScaleUp(ctx, hc, spec, target, false, p)
	})
}

func (m model) runScaleDown(target core.PoolTarget) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		if target.PoolType == core.ElasticPoolType {
			return "", core.ElasticAutoscalerErr()
		}
		hc, spec, err := app.ReservedClient()
		if err != nil {
			return "", err
		}
		return "", core.ScaleDown(ctx, hc, spec, target, false, p)
	})
}

func (m model) runBurst(params core.BurstParams) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		if params.Target.PoolType == core.ElasticPoolType {
			return "", core.ElasticAutoscalerErr()
		}
		hc, spec, err := app.ReservedClient()
		if err != nil {
			return "", err
		}
		err = core.Burst(ctx, hc, spec, app.KubeClient, params, p)
		return "", err
	})
}

func (m model) runDrain(node string) tea.Cmd {
	app := m.app
	return streamCmd(m.debug, func(ctx context.Context, p core.Progress) (string, error) {
		var out io.Writer
		if m.debug {
			out = &lineWriter{sink: p.Debug}
		}
		if err := core.Drain(ctx, app.KubeClient, node, drainTimeout, out); err != nil {
			return "", err
		}
		return "0 non-DaemonSet pods remain on " + node, nil
	})
}

func validateNamespace(ns string) error {
	return k8s.ValidateNamespace(ns)
}
