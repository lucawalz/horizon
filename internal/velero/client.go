package velero

import (
	"context"
	"fmt"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"
	crClient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	backupNamespace        = "velero"
	defaultStorageLocation = "default"
)

type Client struct {
	cl      crClient.Client
	restCfg *rest.Config
}

func NewClient(kubeconfigPath string) (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		rules.ExplicitPath = kubeconfigPath
	}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("velero: kubeconfig %q: %w", kubeconfigPath, err)
	}
	restCfg.WarningHandler = rest.NoWarnings{}
	restCfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	scheme := runtime.NewScheme()
	if err := velerov1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("velero: scheme: %w", err)
	}
	cl, err := crClient.New(restCfg, crClient.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("velero: client: %w", err)
	}
	return &Client{cl: cl, restCfg: restCfg}, nil
}

func NewClientWithCRClient(cl crClient.Client) *Client {
	return &Client{cl: cl}
}

func (c *Client) CreateBackup(ctx context.Context, spec velerov1.BackupSpec, name string) error {
	backup := &velerov1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: backupNamespace},
		Spec:       spec,
	}
	if err := c.cl.Create(ctx, backup); err != nil {
		return fmt.Errorf("velero: backup create %q: %w", name, err)
	}
	return nil
}

func (c *Client) TriggerBackup(ctx context.Context, spec velerov1.BackupSpec, name string, poll, timeout time.Duration) error {
	if err := c.CreateBackup(ctx, spec, name); err != nil {
		return err
	}
	return c.waitForPhase(ctx, "backup", name, poll, timeout, &velerov1.Backup{}, func(obj crClient.Object) (phaseOutcome, error) {
		b := obj.(*velerov1.Backup)
		return classifyBackup(name, b)
	})
}

func (c *Client) CreateRestore(ctx context.Context, spec velerov1.RestoreSpec, name string) error {
	restore := &velerov1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: backupNamespace},
		Spec:       spec,
	}
	if err := c.cl.Create(ctx, restore); err != nil {
		return fmt.Errorf("velero: restore create %q: %w", name, err)
	}
	return nil
}

func (c *Client) TriggerRestore(ctx context.Context, spec velerov1.RestoreSpec, name string, poll, timeout time.Duration) error {
	if err := c.CreateRestore(ctx, spec, name); err != nil {
		return err
	}
	return c.waitForPhase(ctx, "restore", name, poll, timeout, &velerov1.Restore{}, func(obj crClient.Object) (phaseOutcome, error) {
		r := obj.(*velerov1.Restore)
		return classifyRestore(name, r)
	})
}

func (c *Client) ListBackups(ctx context.Context) ([]velerov1.Backup, error) {
	var list velerov1.BackupList
	if err := c.cl.List(ctx, &list, crClient.InNamespace(backupNamespace)); err != nil {
		return nil, fmt.Errorf("velero: backup list: %w", err)
	}
	return list.Items, nil
}

func (c *Client) GetBackup(ctx context.Context, name string) (*velerov1.Backup, error) {
	var backup velerov1.Backup
	key := types.NamespacedName{Name: name, Namespace: backupNamespace}
	if err := c.cl.Get(ctx, key, &backup); err != nil {
		return nil, fmt.Errorf("velero: backup get %q: %w", name, err)
	}
	return &backup, nil
}

func (c *Client) DeleteBackup(ctx context.Context, name string) error {
	req := &velerov1.DeleteBackupRequest{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    backupNamespace,
			GenerateName: name + "-",
		},
		Spec: velerov1.DeleteBackupRequestSpec{BackupName: name},
	}
	if err := c.cl.Create(ctx, req); err != nil {
		return fmt.Errorf("velero: delete backup request %q: %w", name, err)
	}
	return nil
}

func (c *Client) CreateSchedule(ctx context.Context, spec velerov1.ScheduleSpec, name string) error {
	schedule := &velerov1.Schedule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: backupNamespace},
		Spec:       spec,
	}
	if err := c.cl.Create(ctx, schedule); err != nil {
		return fmt.Errorf("velero: schedule create %q: %w", name, err)
	}
	return nil
}

func (c *Client) ListSchedules(ctx context.Context) ([]velerov1.Schedule, error) {
	var list velerov1.ScheduleList
	if err := c.cl.List(ctx, &list, crClient.InNamespace(backupNamespace)); err != nil {
		return nil, fmt.Errorf("velero: schedule list: %w", err)
	}
	return list.Items, nil
}

func (c *Client) GetSchedule(ctx context.Context, name string) (*velerov1.Schedule, error) {
	var schedule velerov1.Schedule
	key := types.NamespacedName{Name: name, Namespace: backupNamespace}
	if err := c.cl.Get(ctx, key, &schedule); err != nil {
		return nil, fmt.Errorf("velero: schedule get %q: %w", name, err)
	}
	return &schedule, nil
}

func (c *Client) DeleteSchedule(ctx context.Context, name string) error {
	schedule := &velerov1.Schedule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: backupNamespace},
	}
	if err := c.cl.Delete(ctx, schedule); err != nil {
		return fmt.Errorf("velero: schedule delete %q: %w", name, err)
	}
	return nil
}

func (c *Client) ListBackupStorageLocations(ctx context.Context) ([]velerov1.BackupStorageLocation, error) {
	var list velerov1.BackupStorageLocationList
	if err := c.cl.List(ctx, &list, crClient.InNamespace(backupNamespace)); err != nil {
		return nil, fmt.Errorf("velero: backup storage location list: %w", err)
	}
	return list.Items, nil
}

func (c *Client) CreateBackupStorageLocation(ctx context.Context, spec velerov1.BackupStorageLocationSpec, name string) error {
	bsl := &velerov1.BackupStorageLocation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: backupNamespace},
		Spec:       spec,
	}
	if err := c.cl.Create(ctx, bsl); err != nil {
		return fmt.Errorf("velero: backup storage location create %q: %w", name, err)
	}
	return nil
}

func (c *Client) ListRestores(ctx context.Context) ([]velerov1.Restore, error) {
	var list velerov1.RestoreList
	if err := c.cl.List(ctx, &list, crClient.InNamespace(backupNamespace)); err != nil {
		return nil, fmt.Errorf("velero: restore list: %w", err)
	}
	return list.Items, nil
}

func (c *Client) GetRestore(ctx context.Context, name string) (*velerov1.Restore, error) {
	var restore velerov1.Restore
	key := types.NamespacedName{Name: name, Namespace: backupNamespace}
	if err := c.cl.Get(ctx, key, &restore); err != nil {
		return nil, fmt.Errorf("velero: restore get %q: %w", name, err)
	}
	return &restore, nil
}

type phaseOutcome int

const (
	phasePending phaseOutcome = iota
	phaseSucceeded
	phaseFailed
)

func classifyBackup(name string, b *velerov1.Backup) (phaseOutcome, error) {
	switch b.Status.Phase {
	case velerov1.BackupPhaseCompleted:
		if b.Status.Errors != 0 {
			return phaseFailed, fmt.Errorf("velero: backup %q completed with %d errors", name, b.Status.Errors)
		}
		return phaseSucceeded, nil
	case velerov1.BackupPhaseFailed, velerov1.BackupPhasePartiallyFailed,
		velerov1.BackupPhaseFailedValidation:
		return phaseFailed, fmt.Errorf("velero: backup %q: phase %s", name, b.Status.Phase)
	}
	return phasePending, nil
}

func classifyRestore(name string, r *velerov1.Restore) (phaseOutcome, error) {
	switch r.Status.Phase {
	case velerov1.RestorePhaseCompleted:
		if r.Status.Errors != 0 {
			return phaseFailed, fmt.Errorf("velero: restore %q completed with %d errors", name, r.Status.Errors)
		}
		return phaseSucceeded, nil
	case velerov1.RestorePhaseFailed, velerov1.RestorePhasePartiallyFailed,
		velerov1.RestorePhaseFailedValidation:
		return phaseFailed, fmt.Errorf("velero: restore %q: phase %s", name, r.Status.Phase)
	}
	return phasePending, nil
}

func (c *Client) waitForPhase(ctx context.Context, kind, name string, poll, timeout time.Duration, obj crClient.Object, classify func(crClient.Object) (phaseOutcome, error)) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	key := types.NamespacedName{Name: name, Namespace: backupNamespace}
	for {
		if err := c.cl.Get(deadlineCtx, key, obj); err != nil {
			return fmt.Errorf("velero: %s get %q: %w", kind, name, err)
		}
		switch outcome, err := classify(obj); outcome {
		case phaseSucceeded:
			return nil
		case phaseFailed:
			return err
		}
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("velero: %s %q: timeout after %s", kind, name, timeout)
		case <-ticker.C:
		}
	}
}
