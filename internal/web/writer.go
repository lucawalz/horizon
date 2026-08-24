package web

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

type ClusterReadWriter interface {
	client.Reader
	client.Writer
}

type shorteningRefused struct{ held time.Duration }

func (e shorteningRefused) Error() string {
	return fmt.Sprintf("web: the lease already runs for %s, and a lease duration may only be lengthened", e.held)
}

func LeaseWriterFor(api ClusterReadWriter) LeaseWriter {
	return clusterWriter{api: api}
}

type clusterWriter struct{ api ClusterReadWriter }

func (w clusterWriter) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return w.api.Create(ctx, obj, opts...)
}

func (w clusterWriter) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return w.api.Delete(ctx, obj, opts...)
}

func (w clusterWriter) Extend(ctx context.Context, name string, duration time.Duration) error {
	lease := &v1alpha1.CapacityLease{}
	if err := w.api.Get(ctx, client.ObjectKey{Name: name}, lease); err != nil {
		return err
	}
	if duration <= lease.Spec.Duration.Duration {
		return shorteningRefused{held: lease.Spec.Duration.Duration}
	}

	// the patch carries the resourceVersion this read saw, so a lease that moved in between is refused rather than overwritten with a value measured against a duration it no longer holds
	patch := client.MergeFromWithOptions(lease.DeepCopy(), client.MergeFromWithOptimisticLock{})
	lease.Spec.Duration = metav1.Duration{Duration: duration}
	return w.api.Patch(ctx, lease, patch)
}
