package tui

import (
	"flag"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/core"
)

const helpColumnGap = 2

type builtinKind int

const (
	builtinNone builtinKind = iota
	builtinHelp
	builtinRefresh
	builtinClear
	builtinQuit
	builtinThemePicker
)

type commandResult struct {
	lines   []string
	builtin builtinKind
	cmd     tea.Cmd
	confirm string
}

func errResult(format string, args ...any) commandResult {
	return commandResult{lines: []string{errStyle.Render(fmt.Sprintf(format, args...))}}
}

func (m model) dispatch(input string) commandResult {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return commandResult{}
	}
	verb, args := fields[0], fields[1:]
	args, m.debug = stripDebugFlag(args)
	switch verb {
	case "help":
		return commandResult{builtin: builtinHelp}
	case "refresh":
		return commandResult{builtin: builtinRefresh}
	case "clear":
		return commandResult{builtin: builtinClear}
	case "quit", "exit":
		return commandResult{builtin: builtinQuit}
	case "up":
		return m.parseUp(args)
	case "down":
		return m.parseDown(args)
	case "burst":
		return m.parseBurst(args)
	case "drain":
		return m.parseDrain(args)
	case "theme":
		return m.parseTheme(args)
	default:
		return errResult("unknown command %q (try help)", verb)
	}
}

func stripDebugFlag(args []string) ([]string, bool) {
	out := args[:0:0]
	debug := false
	for _, a := range args {
		if a == "--debug" || a == "-debug" {
			debug = true
			continue
		}
		out = append(out, a)
	}
	return out, debug
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

type boolFlag interface{ IsBoolFlag() bool }

func parseFlags(fs *flag.FlagSet, args []string) error {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if !strings.HasPrefix(tok, "-") {
			positional = append(positional, tok)
			continue
		}
		flags = append(flags, tok)
		name := strings.TrimLeft(tok, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if def := fs.Lookup(name); def != nil {
			if b, ok := def.Value.(boolFlag); ok && b.IsBoolFlag() {
				continue
			}
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return err
	}
	return nil
}

func (m model) parseUp(args []string) commandResult {
	fs := newFlagSet("up")
	poolType := fs.String("type", "", "")
	namespace := fs.String("namespace", "", "")
	pool := fs.String("pool", "", "")
	replicas := fs.Int("replicas", 0, "")
	if err := parseFlags(fs, args); err != nil {
		return errResult("up: %v", err)
	}
	n, err := resolveUpReplicas(*replicas, fs.Args())
	if err != nil {
		return errResult("up: %v", err)
	}
	target, err := m.poolTargetFrom(*poolType, *namespace, *pool, n)
	if err != nil {
		return errResult("up: %v", err)
	}
	return commandResult{cmd: m.runScaleUp(target)}
}

func resolveUpReplicas(flag int, positional []string) (int32, error) {
	if flag > 0 {
		return int32(flag), nil
	}
	if len(positional) > 0 {
		return parseReplicas(positional[0], 1)
	}
	return 1, nil
}

func (m model) parseDown(args []string) commandResult {
	fs := newFlagSet("down")
	poolType := fs.String("type", "", "")
	namespace := fs.String("namespace", "", "")
	pool := fs.String("pool", "", "")
	fs.Bool("delete", false, "")
	if err := parseFlags(fs, args); err != nil {
		return errResult("down: %v", err)
	}
	target, err := m.poolTargetFrom(*poolType, *namespace, *pool, 0)
	if err != nil {
		return errResult("down: %v", err)
	}
	return commandResult{
		cmd:     m.runScaleDown(target),
		confirm: fmt.Sprintf("delete all %s servers?", target.PoolType),
	}
}

func (m model) parseBurst(args []string) commandResult {
	fs := newFlagSet("burst")
	poolType := fs.String("type", "", "")
	namespace := fs.String("namespace", "", "")
	pool := fs.String("pool", "", "")
	replicas := fs.Int("replicas", 1, "")
	if err := parseFlags(fs, args); err != nil {
		return errResult("burst: %v", err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errResult("burst: namespace argument is required")
	}
	workload := rest[0]
	if err := validateNamespace(workload); err != nil {
		return errResult("burst: %v", err)
	}
	n := int32(*replicas)
	if n < 1 {
		n = 1
	}
	target, err := m.poolTargetFrom(*poolType, *namespace, *pool, n)
	if err != nil {
		return errResult("burst: %v", err)
	}
	params := core.BurstParams{Target: target, Workload: workload, PoolNode: target.PoolType}
	return commandResult{cmd: m.runBurst(params)}
}

func (m model) parseDrain(args []string) commandResult {
	if len(args) == 0 {
		return errResult("drain: node argument is required")
	}
	node := args[0]
	return commandResult{
		cmd:     m.runDrain(node),
		confirm: fmt.Sprintf("drain node %q (cordon and evict pods)?", node),
	}
}

func (m model) parseTheme(args []string) commandResult {
	if len(args) == 0 {
		return commandResult{builtin: builtinThemePicker}
	}
	pref := args[0]
	if err := m.app.Config.SetTheme(pref); err != nil {
		return errResult("theme: %v", err)
	}
	applyThemePref(pref)
	if err := m.app.Config.Save(); err != nil {
		return commandResult{lines: []string{
			dimStyle.Render(fmt.Sprintf("theme set to %s (not persisted: %v)", pref, err)),
		}}
	}
	return commandResult{lines: []string{dimStyle.Render(fmt.Sprintf("theme set to %s", pref))}}
}

type helpEntry struct {
	command string
	desc    string
}

func helpLines() []helpEntry {
	return []helpEntry{
		{"up [--type elastic|reserved] [--replicas N] [<replicas>]", "scale a pool up"},
		{"down [--type ...] [--delete]", "scale a pool to zero or delete it"},
		{"burst <namespace> [--type ...] [--replicas n]", "scale and migrate a workload"},
		{"drain <node>", "cordon and evict a node"},
		{"theme [light|dark|auto]", "set or live-pick the theme"},
		{"<any command> --debug", "stream the underlying steps and API calls"},
		{"refresh · clear · help · quit", "session controls"},
		{"↑↓ · pgup/pgdn", "scroll the command log"},
	}
}

func renderHelp() string {
	entries := helpLines()
	width := 0
	for _, e := range entries {
		if n := lipgloss.Width(e.command); n > width {
			width = n
		}
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		pad := strings.Repeat(" ", width-lipgloss.Width(e.command)+helpColumnGap)
		lines = append(lines, helpCommandStyle.Render(e.command)+pad+dimStyle.Render(e.desc))
	}
	return strings.Join(lines, "\n")
}
