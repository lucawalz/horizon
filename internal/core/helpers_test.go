package core_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/lucawalz/horizon/internal/config"
	"github.com/lucawalz/horizon/internal/core"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testPoolDefaults() config.PoolDefaults {
	return config.PoolDefaults{
		Namespace:   "caph-system",
		Cluster:     "burst",
		DefaultType: "reserved",
		Version:     "v1.35.2+k3s1",
		Types:       map[string]string{"elastic": "elastic-workers", "reserved": "reserved-workers"},
	}
}

func newTestApp() *core.App {
	return &core.App{
		Cluster:       "burst",
		MetricsClient: metricsfake.NewSimpleClientset(),
		Config: &config.Config{
			Cluster: "burst",
			Pools:   testPoolDefaults(),
		},
	}
}

func poolTarget(namespace, name, cluster string, replicas int32) core.PoolTarget {
	return core.PoolTarget{Namespace: namespace, Name: name, Cluster: cluster, Replicas: replicas}
}

func nodeWithAllocatable(name, cpu, mem string) *corev1.Node {
	node := readyNode(name)
	node.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(mem),
	}
	return node
}

func nodeMetrics(name, cpu, mem string) *metricsv1beta1.NodeMetrics {
	return &metricsv1beta1.NodeMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Usage: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse(mem),
		},
	}
}

func metricsClient(t *testing.T, nm ...*metricsv1beta1.NodeMetrics) *metricsfake.Clientset {
	t.Helper()
	cs := metricsfake.NewSimpleClientset()
	gvr := metricsv1beta1.SchemeGroupVersion.WithResource("nodes")
	for _, m := range nm {
		if err := cs.Tracker().Create(gvr, m, ""); err != nil {
			t.Fatalf("seed node metrics %q: %v", m.Name, err)
		}
	}
	return cs
}

func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func capiScheme(t *testing.T) *crfake.ClientBuilder {
	t.Helper()
	s, err := capi.NewScheme()
	if err != nil {
		t.Fatalf("NewScheme: %v", err)
	}
	return crfake.NewClientBuilder().WithScheme(s)
}

func machineDeployment(namespace, name, cluster string, replicas int32) *clusterv1.MachineDeployment {
	return &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: clusterv1.MachineDeploymentSpec{
			ClusterName: cluster,
			Replicas:    &replicas,
		},
	}
}

func mdWithStatus(namespace, name, cluster string, desired, ready int32) *clusterv1.MachineDeployment {
	return mdWithType(namespace, name, cluster, "", desired, ready)
}

func mdWithType(namespace, name, cluster, poolType string, desired, ready int32) *clusterv1.MachineDeployment {
	md := machineDeployment(namespace, name, cluster, desired)
	md.Labels = map[string]string{
		"horizon.dev/managed-by":   "horizon",
		clusterv1.ClusterNameLabel: cluster,
	}
	if poolType != "" {
		md.Labels["horizon.dev/pool-type"] = poolType
	}
	md.Status.ReadyReplicas = &ready
	return md
}

func machineFor(namespace, pool, name, phase, node, providerID string) *clusterv1.Machine {
	m := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    map[string]string{clusterv1.MachineDeploymentNameLabel: pool},
		},
	}
	m.Spec.ProviderID = providerID
	m.Status.Phase = phase
	m.Status.NodeRef.Name = node
	return m
}

func initializedCluster(namespace, name string, initialized bool) *clusterv1.Cluster {
	c := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	}
	c.Status.Initialization.ControlPlaneInitialized = &initialized
	return c
}

func managedCluster(namespace, name, phase string, initialized bool) *clusterv1.Cluster {
	c := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    map[string]string{"horizon.dev/managed-by": "horizon"},
		},
	}
	c.Status.Phase = phase
	c.Status.Initialization.ControlPlaneInitialized = &initialized
	return c
}

func capiClient(t *testing.T, objs ...client.Object) *capi.Client {
	t.Helper()
	cl := capiScheme(t).WithObjects(objs...).WithStatusSubresource(&clusterv1.Cluster{}).Build()
	return capi.NewClientWithCRClient(cl)
}

func burstCapiClient(t *testing.T, objs ...client.Object) *capi.Client {
	t.Helper()
	cl := capiScheme(t).WithObjects(objs...).Build()
	return capi.NewClientWithCRClient(cl)
}

func collectProgress(msgs *[]string) core.Progress {
	return core.NewProgress(func(msg string) { *msgs = append(*msgs, msg) }, nil)
}

func fixedNow() time.Time {
	return time.Date(2026, 6, 12, 14, 30, 5, 0, time.UTC)
}

func captureStdout(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

type fakeVeleroClient struct {
	backups   []velerov1.Backup
	restores  []velerov1.Restore
	schedules []velerov1.Schedule
	locations []velerov1.BackupStorageLocation

	createBackupErr   error
	triggerBackupErr  error
	createRestoreErr  error
	listBackupsErr    error
	getBackupErr      error
	deleteBackupErr   error
	listRestoresErr   error
	getRestoreErr     error
	createScheduleErr error
	listSchedulesErr  error
	getScheduleErr    error
	deleteScheduleErr error
	listBSLErr        error
	createBSLErr      error

	createdBackupSpec    velerov1.BackupSpec
	createdBackupName    string
	createdRestoreSpec   velerov1.RestoreSpec
	createdRestoreName   string
	triggeredBackupSpec  velerov1.BackupSpec
	triggeredRestoreSpec velerov1.RestoreSpec
	deletedBackup        string
	createdScheduleSpec  velerov1.ScheduleSpec
	createdScheduleName  string
	deletedSchedule      string
	createdBSLSpec       velerov1.BackupStorageLocationSpec
	createdBSLName       string
	waited               bool
}

func (f *fakeVeleroClient) CreateBackup(_ context.Context, spec velerov1.BackupSpec, name string) error {
	f.createdBackupSpec = spec
	f.createdBackupName = name
	return f.createBackupErr
}

func (f *fakeVeleroClient) TriggerBackup(_ context.Context, spec velerov1.BackupSpec, name string, _, _ time.Duration) error {
	f.triggeredBackupSpec = spec
	f.createdBackupName = name
	f.waited = true
	return f.triggerBackupErr
}

func (f *fakeVeleroClient) CreateRestore(_ context.Context, spec velerov1.RestoreSpec, name string) error {
	f.createdRestoreSpec = spec
	f.createdRestoreName = name
	return f.createRestoreErr
}

func (f *fakeVeleroClient) TriggerRestore(_ context.Context, spec velerov1.RestoreSpec, name string, _, _ time.Duration) error {
	f.triggeredRestoreSpec = spec
	f.createdRestoreName = name
	f.waited = true
	return nil
}

func (f *fakeVeleroClient) ListBackups(_ context.Context) ([]velerov1.Backup, error) {
	return f.backups, f.listBackupsErr
}

func (f *fakeVeleroClient) GetBackup(_ context.Context, name string) (*velerov1.Backup, error) {
	if f.getBackupErr != nil {
		return nil, f.getBackupErr
	}
	for i := range f.backups {
		if f.backups[i].Name == name {
			return &f.backups[i], nil
		}
	}
	return &velerov1.Backup{}, nil
}

func (f *fakeVeleroClient) DeleteBackup(_ context.Context, name string) error {
	f.deletedBackup = name
	return f.deleteBackupErr
}

func (f *fakeVeleroClient) ListRestores(_ context.Context) ([]velerov1.Restore, error) {
	return f.restores, f.listRestoresErr
}

func (f *fakeVeleroClient) GetRestore(_ context.Context, name string) (*velerov1.Restore, error) {
	if f.getRestoreErr != nil {
		return nil, f.getRestoreErr
	}
	for i := range f.restores {
		if f.restores[i].Name == name {
			return &f.restores[i], nil
		}
	}
	return &velerov1.Restore{}, nil
}

func (f *fakeVeleroClient) CreateSchedule(_ context.Context, spec velerov1.ScheduleSpec, name string) error {
	f.createdScheduleSpec = spec
	f.createdScheduleName = name
	return f.createScheduleErr
}

func (f *fakeVeleroClient) ListSchedules(_ context.Context) ([]velerov1.Schedule, error) {
	return f.schedules, f.listSchedulesErr
}

func (f *fakeVeleroClient) GetSchedule(_ context.Context, name string) (*velerov1.Schedule, error) {
	if f.getScheduleErr != nil {
		return nil, f.getScheduleErr
	}
	for i := range f.schedules {
		if f.schedules[i].Name == name {
			return &f.schedules[i], nil
		}
	}
	return &velerov1.Schedule{}, nil
}

func (f *fakeVeleroClient) DeleteSchedule(_ context.Context, name string) error {
	f.deletedSchedule = name
	return f.deleteScheduleErr
}

func (f *fakeVeleroClient) ListBackupStorageLocations(_ context.Context) ([]velerov1.BackupStorageLocation, error) {
	return f.locations, f.listBSLErr
}

func (f *fakeVeleroClient) CreateBackupStorageLocation(_ context.Context, spec velerov1.BackupStorageLocationSpec, name string) error {
	f.createdBSLSpec = spec
	f.createdBSLName = name
	return f.createBSLErr
}
