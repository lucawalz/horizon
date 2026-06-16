package core

import (
	"context"
	"fmt"
	"sort"

	"github.com/lucawalz/horizon/internal/capi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type Detected struct {
	Namespaces     []string
	PoolsNamespace string
	PoolTypes      map[string]string
	ClusterName    string
	Clusters       []string
}

func Detect(ctx context.Context, kube kubernetes.Interface, cc *capi.Client) (Detected, error) {
	detected := Detected{PoolTypes: make(map[string]string)}

	nsList, err := kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Detected{}, fmt.Errorf("detect: list namespaces: %w", err)
	}
	for i := range nsList.Items {
		detected.Namespaces = append(detected.Namespaces, nsList.Items[i].Name)
	}
	sort.Strings(detected.Namespaces)

	mds, err := cc.ListMachineDeploymentsByType(ctx, "")
	if err != nil {
		return Detected{}, fmt.Errorf("detect: list machine deployments: %w", err)
	}

	detected.PoolsNamespace = busiestNamespace(mds)
	for i := range mds {
		md := mds[i]
		if md.Namespace != detected.PoolsNamespace {
			continue
		}
		if t := capi.PoolType(&md); t != "" {
			detected.PoolTypes[t] = md.Name
		}
		if detected.ClusterName == "" {
			detected.ClusterName = md.Labels[clusterv1.ClusterNameLabel]
		}
	}

	clusters, err := cc.ListClusters(ctx, "")
	if err != nil {
		return Detected{}, fmt.Errorf("detect: list clusters: %w", err)
	}
	for i := range clusters {
		detected.Clusters = append(detected.Clusters, clusters[i].Name)
	}
	sort.Strings(detected.Clusters)

	return detected, nil
}

func busiestNamespace(mds []clusterv1.MachineDeployment) string {
	counts := make(map[string]int)
	for i := range mds {
		counts[mds[i].Namespace]++
	}
	busiest := ""
	best := 0
	for ns, n := range counts {
		if n > best || (n == best && (busiest == "" || ns < busiest)) {
			busiest = ns
			best = n
		}
	}
	return busiest
}
