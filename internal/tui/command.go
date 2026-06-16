package tui

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucawalz/horizon/internal/core"
)

const helpColumnGap = 2

type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }

func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

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
	case "nudge":
		return m.parseNudge(args)
	case "burst":
		return m.parseBurst(args)
	case "cluster":
		return m.parseCluster(args)
	case "backup":
		return m.parseBackup(args)
	case "restore":
		return m.parseRestore(args)
	case "schedule":
		return m.parseSchedule(args)
	case "bsl":
		return m.parseBSL(args)
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
	nudge := fs.Bool("nudge", false, "")
	if err := parseFlags(fs, args); err != nil {
		return errResult("up: %v", err)
	}
	replicas := int32(1)
	if rest := fs.Args(); len(rest) > 0 {
		r, err := parseReplicas(rest[0], 1)
		if err != nil {
			return errResult("up: %v", err)
		}
		replicas = r
	}
	target, err := m.poolTargetFrom(*poolType, *namespace, *pool, replicas)
	if err != nil {
		return errResult("up: %v", err)
	}
	return commandResult{cmd: m.runScaleUp(target, *nudge)}
}

func (m model) parseDown(args []string) commandResult {
	fs := newFlagSet("down")
	poolType := fs.String("type", "", "")
	namespace := fs.String("namespace", "", "")
	pool := fs.String("pool", "", "")
	del := fs.Bool("delete", false, "")
	if err := parseFlags(fs, args); err != nil {
		return errResult("down: %v", err)
	}
	target, err := m.poolTargetFrom(*poolType, *namespace, *pool, 0)
	if err != nil {
		return errResult("down: %v", err)
	}
	res := commandResult{cmd: m.runScaleDown(target, *del)}
	if *del {
		res.confirm = fmt.Sprintf("delete pool %s/%s?", target.Namespace, target.Name)
	}
	return res
}

func (m model) parseNudge(args []string) commandResult {
	fs := newFlagSet("nudge")
	namespace := fs.String("namespace", "", "")
	cluster := fs.String("cluster", "", "")
	if err := parseFlags(fs, args); err != nil {
		return errResult("nudge: %v", err)
	}
	target := core.PoolTarget{
		Namespace: orDefault(*namespace, m.app.Config.Pools.Namespace),
		Cluster:   orDefault(*cluster, m.app.Cluster),
	}
	return commandResult{cmd: m.runNudge(target)}
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

func (m model) parseCluster(args []string) commandResult {
	if len(args) == 0 {
		return errResult("cluster: want create|delete|list")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return commandResult{cmd: m.loadClusters()}
	case "create":
		return m.parseClusterCreate(rest)
	case "delete":
		if len(rest) == 0 {
			return errResult("cluster delete: name argument is required")
		}
		ns := m.app.Config.Pools.Namespace
		return commandResult{
			cmd:     m.runClusterDelete(ns, rest[0]),
			confirm: fmt.Sprintf("delete cluster %s/%s?", ns, rest[0]),
		}
	default:
		return errResult("cluster: unknown subcommand %q", sub)
	}
}

type clusterCreateInput struct {
	name                 string
	namespace            string
	class                string
	workerClass          string
	version              string
	replicas             int32
	controlPlaneReplicas int32
	sets                 []string
}

func (m model) parseClusterCreate(args []string) commandResult {
	fs := newFlagSet("cluster create")
	namespace := fs.String("namespace", "", "")
	class := fs.String("class", "", "")
	workerClass := fs.String("worker-class", "", "")
	flavor := fs.String("flavor", "", "")
	version := fs.String("version", "", "")
	replicas := fs.Int("replicas", 1, "")
	controlPlaneReplicas := fs.Int("control-plane-replicas", 1, "")
	var sets stringSlice
	fs.Var(&sets, "set", "")
	fs.Bool("preview", false, "")
	write := fs.Bool("write", false, "")
	apply := fs.Bool("apply", false, "")
	if err := parseFlags(fs, args); err != nil {
		return errResult("cluster create: %v", err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errResult("cluster create: name argument is required")
	}
	mode := createMode{apply: *apply, write: *write}

	if strings.TrimSpace(*flavor) != "" {
		if strings.TrimSpace(*class) != "" {
			return errResult("cluster create: --class and --flavor are mutually exclusive")
		}
		return m.dispatchFlavorCreate(rest[0], *flavor, sets, mode)
	}
	if strings.TrimSpace(*class) == "" && m.app.Config.ClusterCreate.Class == "" {
		return errResult("cluster create: one of --class or --flavor is required")
	}

	spec, err := m.clusterSpecFrom(clusterCreateInput{
		name:                 rest[0],
		namespace:            *namespace,
		class:                *class,
		workerClass:          *workerClass,
		version:              *version,
		replicas:             int32(*replicas),
		controlPlaneReplicas: int32(*controlPlaneReplicas),
		sets:                 sets,
	})
	if err != nil {
		return errResult("cluster create: %v", err)
	}
	switch {
	case mode.apply:
		return commandResult{cmd: m.runClusterApply(spec)}
	case mode.write:
		if m.app.Config.RepoPath == "" {
			return errResult("cluster create: --write disabled, repo_path unset in config")
		}
		return commandResult{cmd: m.runClusterWrite(spec)}
	default:
		return commandResult{cmd: m.renderClusterPreview(spec)}
	}
}

type createMode struct {
	apply bool
	write bool
}

func (m model) dispatchFlavorCreate(name, flavorPath string, sets stringSlice, mode createMode) commandResult {
	template, err := os.ReadFile(flavorPath)
	if err != nil {
		return errResult("cluster create: read flavor %q: %v", flavorPath, err)
	}
	vars, err := setVarMap(sets)
	if err != nil {
		return errResult("cluster create: %v", err)
	}
	req := flavorRequest{name: name, template: template, vars: vars}
	switch {
	case mode.apply:
		return commandResult{cmd: m.runFlavorApply(req)}
	case mode.write:
		if m.app.Config.RepoPath == "" {
			return errResult("cluster create: --write disabled, repo_path unset in config")
		}
		return commandResult{cmd: m.runFlavorWrite(req)}
	default:
		return commandResult{cmd: m.renderFlavorPreview(req)}
	}
}

func (m model) parseBackup(args []string) commandResult {
	if len(args) == 0 {
		return errResult("backup: want create|list|describe|delete")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return commandResult{cmd: m.loadBackups()}
	case "describe":
		if len(rest) == 0 {
			return errResult("backup describe: name argument is required")
		}
		return commandResult{cmd: m.describeBackupCmd(rest[0])}
	case "delete":
		if len(rest) == 0 {
			return errResult("backup delete: name argument is required")
		}
		return commandResult{
			cmd:     m.runBackupDelete(rest[0]),
			confirm: fmt.Sprintf("delete backup %q?", rest[0]),
		}
	case "create":
		return m.parseBackupCreate(rest)
	default:
		return errResult("backup: unknown subcommand %q", sub)
	}
}

func (m model) parseBackupCreate(args []string) commandResult {
	fs := newFlagSet("backup create")
	values := map[string]*string{
		"include-namespaces": fs.String("include-namespaces", "", ""),
		"exclude-namespaces": fs.String("exclude-namespaces", "", ""),
		"include-resources":  fs.String("include-resources", "", ""),
		"selector":           fs.String("selector", "", ""),
		"storage-location":   fs.String("storage-location", core.DefaultStorageLocation, ""),
		"ttl":                fs.String("ttl", core.DefaultBackupTTL.String(), ""),
		"snapshot-volumes":   fs.String("snapshot-volumes", "true", ""),
	}
	name := fs.String("name", "", "")
	wait := fs.Bool("wait", false, "")
	if err := parseFlags(fs, args); err != nil {
		return errResult("backup create: %v", err)
	}
	flat := flatten(values)
	spec, err := buildBackupSpecFromValues(flat)
	if err != nil {
		return errResult("backup create: %v", err)
	}
	bname := *name
	if bname == "" {
		bname = core.DefaultBackupName(spec.IncludedNamespaces, time.Now())
	}
	return commandResult{cmd: m.runBackupCreate(spec, bname, *wait)}
}

func (m model) parseRestore(args []string) commandResult {
	if len(args) == 0 {
		return errResult("restore: want create|list|describe")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return commandResult{cmd: m.loadRestores()}
	case "describe":
		if len(rest) == 0 {
			return errResult("restore describe: name argument is required")
		}
		return commandResult{cmd: m.describeRestoreCmd(rest[0])}
	case "create":
		return m.parseRestoreCreate(rest)
	default:
		return errResult("restore: unknown subcommand %q", sub)
	}
}

func (m model) parseRestoreCreate(args []string) commandResult {
	fs := newFlagSet("restore create")
	fromBackup := fs.String("from-backup", "", "")
	values := map[string]*string{
		"include-namespaces": fs.String("include-namespaces", "", ""),
		"namespace-mappings": fs.String("namespace-mappings", "", ""),
	}
	name := fs.String("name", "", "")
	wait := fs.Bool("wait", false, "")
	if err := parseFlags(fs, args); err != nil {
		return errResult("restore create: %v", err)
	}
	if strings.TrimSpace(*fromBackup) == "" {
		return errResult("restore create: --from-backup is required")
	}
	spec, err := buildRestoreSpecFromValues(*fromBackup, flatten(values))
	if err != nil {
		return errResult("restore create: %v", err)
	}
	rname := *name
	if rname == "" {
		rname = core.DefaultRestoreName(spec.BackupName, time.Now())
	}
	return commandResult{
		cmd:     m.runRestoreCreate(spec, rname, *wait),
		confirm: fmt.Sprintf("restore from backup %q as %q?", spec.BackupName, rname),
	}
}

func (m model) parseSchedule(args []string) commandResult {
	if len(args) == 0 {
		return errResult("schedule: want create|list|describe|delete")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return commandResult{cmd: m.loadSchedules()}
	case "describe":
		if len(rest) == 0 {
			return errResult("schedule describe: name argument is required")
		}
		return commandResult{cmd: m.describeScheduleCmd(rest[0])}
	case "delete":
		if len(rest) == 0 {
			return errResult("schedule delete: name argument is required")
		}
		return commandResult{
			cmd:     m.runScheduleDelete(rest[0]),
			confirm: fmt.Sprintf("delete schedule %q?", rest[0]),
		}
	case "create":
		return m.parseScheduleCreate(rest)
	default:
		return errResult("schedule: unknown subcommand %q", sub)
	}
}

func (m model) parseScheduleCreate(args []string) commandResult {
	fs := newFlagSet("schedule create")
	cron := fs.String("schedule", "", "")
	values := map[string]*string{
		"include-namespaces": fs.String("include-namespaces", "", ""),
		"exclude-namespaces": fs.String("exclude-namespaces", "", ""),
		"include-resources":  fs.String("include-resources", "", ""),
		"selector":           fs.String("selector", "", ""),
		"storage-location":   fs.String("storage-location", core.DefaultStorageLocation, ""),
		"ttl":                fs.String("ttl", core.DefaultBackupTTL.String(), ""),
		"snapshot-volumes":   fs.String("snapshot-volumes", "true", ""),
	}
	if err := parseFlags(fs, args); err != nil {
		return errResult("schedule create: %v", err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errResult("schedule create: name argument is required")
	}
	spec, err := buildScheduleSpecFromValues(*cron, flatten(values))
	if err != nil {
		return errResult("schedule create: %v", err)
	}
	return commandResult{cmd: m.runScheduleCreate(spec, rest[0])}
}

func (m model) parseBSL(args []string) commandResult {
	if len(args) == 0 {
		return errResult("bsl: want create|list")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return commandResult{cmd: m.loadStorageLocations()}
	case "create":
		return m.parseBSLCreate(rest)
	default:
		return errResult("bsl: unknown subcommand %q", sub)
	}
}

func (m model) parseBSLCreate(args []string) commandResult {
	fs := newFlagSet("bsl create")
	values := map[string]*string{
		"provider":   fs.String("provider", "", ""),
		"bucket":     fs.String("bucket", "", ""),
		"prefix":     fs.String("prefix", "", ""),
		"credential": fs.String("credential", "", ""),
	}
	if err := parseFlags(fs, args); err != nil {
		return errResult("bsl create: %v", err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errResult("bsl create: name argument is required")
	}
	spec, err := buildBSLSpecFromValues(flatten(values))
	if err != nil {
		return errResult("bsl create: %v", err)
	}
	return commandResult{cmd: m.runBSLCreate(spec, rest[0])}
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

func flatten(values map[string]*string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = *v
	}
	return out
}

type helpEntry struct {
	command string
	desc    string
}

func helpLines() []helpEntry {
	return []helpEntry{
		{"up [--type elastic|reserved] [--nudge] [<replicas>]", "scale a pool up"},
		{"down [--type ...] [--delete]", "scale a pool to zero or delete it"},
		{"nudge [--namespace ns] [--cluster name]", "latch control-plane-initialized"},
		{"burst <namespace> [--type ...] [--replicas n]", "back up, scale, migrate a workload"},
		{"cluster create <name> --class <cc> [--set k=v] · or --flavor <file>", "render, write, or apply a cluster"},
		{"cluster delete <name> · cluster list", "manage CAPI-managed clusters"},
		{"backup create [--include-namespaces ...] [--wait]", "create a velero backup"},
		{"backup list · describe <name> · delete <name>", "inspect velero backups"},
		{"restore create --from-backup <name> [--wait]", "restore from a backup"},
		{"restore list · restore describe <name>", "inspect velero restores"},
		{"schedule create <name> --schedule \"<cron>\" [--include-namespaces ...]", "create a recurring backup schedule"},
		{"schedule list · describe <name> · delete <name>", "inspect velero schedules"},
		{"bsl create <name> --provider <p> --bucket <b>", "point velero at an existing bucket"},
		{"bsl list", "inspect backup storage locations"},
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
