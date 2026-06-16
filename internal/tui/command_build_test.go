package tui

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildBackupSpecFromValues(t *testing.T) {
	spec, err := buildBackupSpecFromValues(map[string]string{
		"include-namespaces": "a,b",
		"include-resources":  "deployments",
		"ttl":                "168h",
		"selector":           "app=web",
		"snapshot-volumes":   "true",
	})
	if err != nil {
		t.Fatalf("buildBackupSpecFromValues: %v", err)
	}
	if len(spec.IncludedNamespaces) != 2 || spec.IncludedNamespaces[0] != "a" {
		t.Errorf("IncludedNamespaces = %v", spec.IncludedNamespaces)
	}
	if len(spec.IncludedResources) != 1 || spec.IncludedResources[0] != "deployments" {
		t.Errorf("IncludedResources = %v", spec.IncludedResources)
	}
	if spec.TTL != (metav1.Duration{Duration: 168 * time.Hour}) {
		t.Errorf("TTL = %v, want 168h", spec.TTL)
	}
	if spec.SnapshotVolumes == nil || !*spec.SnapshotVolumes {
		t.Errorf("SnapshotVolumes = %v, want &true", spec.SnapshotVolumes)
	}
	if spec.LabelSelector == nil || spec.LabelSelector.MatchLabels["app"] != "web" {
		t.Errorf("LabelSelector = %v, want app=web", spec.LabelSelector)
	}
}

func TestBuildBackupSpecFromValuesBadSelector(t *testing.T) {
	if _, err := buildBackupSpecFromValues(map[string]string{"selector": "=="}); err == nil {
		t.Fatal("expected error from malformed selector")
	}
}

func TestBuildScheduleSpecFromValues(t *testing.T) {
	spec, err := buildScheduleSpecFromValues("0 3 * * *", map[string]string{
		"include-namespaces": "app",
		"include-resources":  "deployments",
		"ttl":                "168h",
		"snapshot-volumes":   "true",
	})
	if err != nil {
		t.Fatalf("buildScheduleSpecFromValues: %v", err)
	}
	if spec.Schedule != "0 3 * * *" {
		t.Errorf("Schedule = %q, want 0 3 * * *", spec.Schedule)
	}
	if len(spec.Template.IncludedNamespaces) != 1 || spec.Template.IncludedNamespaces[0] != "app" {
		t.Errorf("template namespaces = %v, want [app]", spec.Template.IncludedNamespaces)
	}
	if spec.Template.TTL != (metav1.Duration{Duration: 168 * time.Hour}) {
		t.Errorf("template TTL = %v, want 168h", spec.Template.TTL)
	}
}

func TestBuildScheduleSpecFromValuesRequiresCron(t *testing.T) {
	if _, err := buildScheduleSpecFromValues("  ", map[string]string{"include-namespaces": "app"}); err == nil {
		t.Fatal("expected error for empty cron")
	}
}

func TestBuildBSLSpecFromValues(t *testing.T) {
	spec, err := buildBSLSpecFromValues(map[string]string{
		"provider":   "aws",
		"bucket":     "horizon-backups",
		"prefix":     "cluster-a",
		"credential": "velero-creds/cloud",
	})
	if err != nil {
		t.Fatalf("buildBSLSpecFromValues: %v", err)
	}
	if spec.Provider != "aws" {
		t.Errorf("Provider = %q, want aws", spec.Provider)
	}
	if spec.ObjectStorage == nil || spec.ObjectStorage.Bucket != "horizon-backups" || spec.ObjectStorage.Prefix != "cluster-a" {
		t.Errorf("unexpected ObjectStorage %+v", spec.ObjectStorage)
	}
	if spec.Credential == nil || spec.Credential.Name != "velero-creds" || spec.Credential.Key != "cloud" {
		t.Errorf("unexpected Credential %+v", spec.Credential)
	}
}

func TestBuildBSLSpecFromValuesMissingProvider(t *testing.T) {
	if _, err := buildBSLSpecFromValues(map[string]string{"bucket": "b"}); err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestBuildBSLSpecFromValuesMissingBucket(t *testing.T) {
	if _, err := buildBSLSpecFromValues(map[string]string{"provider": "aws"}); err == nil {
		t.Fatal("expected error for missing bucket")
	}
}

func TestBuildBSLSpecFromValuesBadCredential(t *testing.T) {
	if _, err := buildBSLSpecFromValues(map[string]string{"provider": "aws", "bucket": "b", "credential": "noslash"}); err == nil {
		t.Fatal("expected error for malformed credential")
	}
}

func TestBuildRestoreSpecFromValues(t *testing.T) {
	spec, err := buildRestoreSpecFromValues("bk1", map[string]string{
		"include-namespaces": "ns1",
		"namespace-mappings": "old:new",
	})
	if err != nil {
		t.Fatalf("buildRestoreSpecFromValues: %v", err)
	}
	if spec.BackupName != "bk1" {
		t.Errorf("BackupName = %q", spec.BackupName)
	}
	if spec.NamespaceMapping["old"] != "new" {
		t.Errorf("NamespaceMapping = %v", spec.NamespaceMapping)
	}
}

func TestBuildRestoreSpecFromValuesBadMapping(t *testing.T) {
	if _, err := buildRestoreSpecFromValues("bk", map[string]string{"namespace-mappings": "noseparator"}); err == nil {
		t.Fatal("expected error from malformed mapping")
	}
}

func TestParseList(t *testing.T) {
	if got := parseList(" a , ,b "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("parseList = %v, want [a b]", got)
	}
	if got := parseList(""); got != nil {
		t.Errorf("parseList empty = %v, want nil", got)
	}
}

func TestParseReplicas(t *testing.T) {
	if n, err := parseReplicas("", 1); err != nil || n != 1 {
		t.Errorf("empty replicas = %d,%v, want 1,nil", n, err)
	}
	if n, err := parseReplicas("3", 1); err != nil || n != 3 {
		t.Errorf("replicas = %d,%v, want 3,nil", n, err)
	}
	if _, err := parseReplicas("xyz", 1); err == nil {
		t.Error("expected error for non-numeric replicas")
	}
}

func TestParseBool(t *testing.T) {
	if !parseBool("true") {
		t.Error("parseBool(true) = false")
	}
	if parseBool("nonsense") {
		t.Error("parseBool(nonsense) = true")
	}
}
