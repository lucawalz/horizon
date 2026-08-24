package web

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

type recordingCluster struct {
	ClusterReadWriter
	held     time.Duration
	revision string
	getErr   error
	patched  []byte
}

func (c *recordingCluster) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if c.getErr != nil {
		return c.getErr
	}
	lease, isLease := obj.(*v1alpha1.CapacityLease)
	if !isLease {
		return errors.New("the writer read something other than a capacity lease")
	}
	lease.Name = key.Name
	lease.ResourceVersion = c.revision
	lease.Spec.Duration = metav1.Duration{Duration: c.held}
	return nil
}

func (c *recordingCluster) Patch(_ context.Context, obj client.Object, patch client.Patch, _ ...client.PatchOption) error {
	body, err := patch.Data(obj)
	if err != nil {
		return err
	}
	c.patched = body
	return nil
}

func newRecordingCluster(held time.Duration) *recordingCluster {
	return &recordingCluster{held: held, revision: "417"}
}

func TestExtendPatchesOnlyTheDurationAndTheRevisionItRead(t *testing.T) {
	cluster := newRecordingCluster(2 * time.Hour)

	if err := LeaseWriterFor(cluster).Extend(t.Context(), "patched-run", 3*time.Hour); err != nil {
		t.Fatalf("extend: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(cluster.patched, &body); err != nil {
		t.Fatalf("decode the patch %s: %v", cluster.patched, err)
	}
	if keys := sortedKeys(body); !slices.Equal(keys, []string{"metadata", "spec"}) {
		t.Fatalf("the patch names %v, want metadata and spec alone: %s", keys, cluster.patched)
	}

	spec, isObject := body["spec"].(map[string]any)
	if !isObject {
		t.Fatalf("the patch spec is %T, want an object: %s", body["spec"], cluster.patched)
	}
	if keys := sortedKeys(spec); !slices.Equal(keys, []string{"duration"}) {
		t.Errorf("the patch moves %v, want the duration alone: %s", keys, cluster.patched)
	}
	if spec["duration"] != "3h0m0s" {
		t.Errorf("the patched duration is %v, want 3h0m0s", spec["duration"])
	}

	metadata, isObject := body["metadata"].(map[string]any)
	if !isObject {
		t.Fatalf("the patch metadata is %T, want an object: %s", body["metadata"], cluster.patched)
	}
	if keys := sortedKeys(metadata); !slices.Equal(keys, []string{"resourceVersion"}) {
		t.Errorf("the patch metadata names %v, want the resourceVersion alone: %s", keys, cluster.patched)
	}
	if metadata["resourceVersion"] != cluster.revision {
		t.Errorf("the patch guards revision %v, want %q", metadata["resourceVersion"], cluster.revision)
	}
}

func TestExtendRefusesADurationNoLongerThanTheStoredOneWithoutWriting(t *testing.T) {
	for name, requested := range map[string]time.Duration{
		"shorter": time.Hour,
		"equal":   2 * time.Hour,
	} {
		t.Run(name, func(t *testing.T) {
			cluster := newRecordingCluster(2 * time.Hour)

			err := LeaseWriterFor(cluster).Extend(t.Context(), "unshortened-run", requested)

			var refused shorteningRefused
			if !errors.As(err, &refused) {
				t.Fatalf("extend answered %v, want a refusal naming the stored duration", err)
			}
			if refused.held != 2*time.Hour {
				t.Errorf("the refusal names %s, want 2h", refused.held)
			}
			if cluster.patched != nil {
				t.Errorf("the cluster was patched with %s, want nothing written", cluster.patched)
			}
		})
	}
}

func TestExtendSurfacesTheFailureToReadTheLease(t *testing.T) {
	cluster := newRecordingCluster(2 * time.Hour)
	cluster.getErr = errors.New("the api server is unreachable")

	err := LeaseWriterFor(cluster).Extend(t.Context(), "unreadable-run", 3*time.Hour)

	if !errors.Is(err, cluster.getErr) {
		t.Errorf("extend answered %v, want the read failure", err)
	}
	if cluster.patched != nil {
		t.Errorf("the cluster was patched with %s, want nothing written", cluster.patched)
	}
}

func sortedKeys(body map[string]any) []string {
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
