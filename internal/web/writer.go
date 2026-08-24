package web

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

type durationPatch struct {
	Spec durationPatchSpec `json:"spec"`
}

type durationPatchSpec struct {
	Duration metav1.Duration `json:"duration"`
}

func LeaseWriterFor(api client.Client) LeaseWriter {
	return clusterWriter{api: api}
}

type clusterWriter struct{ api client.Client }

func (w clusterWriter) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return w.api.Create(ctx, obj, opts...)
}

func (w clusterWriter) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return w.api.Delete(ctx, obj, opts...)
}

func (w clusterWriter) Extend(ctx context.Context, name string, duration time.Duration) error {
	// the patch body is built here rather than accepted, so the one field this verb may move is the only field it names
	body, err := json.Marshal(durationPatch{Spec: durationPatchSpec{Duration: metav1.Duration{Duration: duration}}})
	if err != nil {
		return fmt.Errorf("build the duration patch for %s: %w", name, err)
	}

	lease := &v1alpha1.CapacityLease{ObjectMeta: metav1.ObjectMeta{Name: name}}
	return w.api.Patch(ctx, lease, client.RawPatch(types.MergePatchType, body))
}
