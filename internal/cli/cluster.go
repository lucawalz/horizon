package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/lucawalz/horizon/internal/capi"
	"github.com/spf13/cobra"
)

const (
	infrastructureGroup     = "infrastructure.cluster.x-k8s.io"
	bootstrapGroup          = "bootstrap.cluster.x-k8s.io"
	defaultInfraKind        = "HCloudMachineTemplate"
	defaultClusterInfraKind = "HetznerCluster"
	defaultBootstrapKind    = "KThreesConfigTemplate"
	defaultClusterVersion   = "v1.31.0+k3s1"
	defaultPodCIDR          = "10.42.0.0/16"
	defaultServiceCIDR      = "10.43.0.0/16"
)

func newClusterCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage CAPI-managed clusters with their own control plane",
	}
	cmd.AddCommand(newClusterCreateCmd(app))
	cmd.AddCommand(newClusterDeleteCmd(app))
	cmd.AddCommand(newClusterListCmd(app))
	return cmd
}

func newClusterCreateCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a CAPI-managed cluster; precedence: --dry-run (print only) > --write (write to bedrock tree) > live apply",
		Long: "Create a CAPI-managed cluster. The infrastructure and bootstrap objects referenced by name " +
			"(HetznerCluster, HCloudMachineTemplate, KThreesConfigTemplate) must already exist in the target namespace. " +
			"RenderCluster emits the Cluster, control plane, and worker pool that reference them. " +
			"Precedence: --dry-run (print only) > --write (write to bedrock tree) > live apply.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterCreate(cmd, app)
		},
	}
	cmd.Flags().String("name", "", "Cluster name (required)")
	cmd.Flags().String("namespace", "", "Cluster namespace (default from config pools)")
	cmd.Flags().String("version", defaultClusterVersion, "Kubernetes version")
	cmd.Flags().String("pod-cidr", defaultPodCIDR, "Pod network CIDR")
	cmd.Flags().String("service-cidr", defaultServiceCIDR, "Service network CIDR")
	cmd.Flags().Int32("replicas", 1, "Replica count for the control plane and worker pool")
	cmd.Flags().String("cluster-infra-kind", defaultClusterInfraKind, "Cluster infrastructure object kind")
	cmd.Flags().String("cluster-infra-name", "", "Cluster infrastructure object name (default <name>)")
	cmd.Flags().String("infra-kind", defaultInfraKind, "Worker infrastructure template kind")
	cmd.Flags().String("infra-name", "", "Worker infrastructure template name (default <name>-workers)")
	cmd.Flags().String("cp-infra-kind", defaultInfraKind, "Control plane infrastructure template kind")
	cmd.Flags().String("cp-infra-name", "", "Control plane infrastructure template name (default <name>-control-plane)")
	cmd.Flags().String("bootstrap-kind", defaultBootstrapKind, "Bootstrap config template kind")
	cmd.Flags().String("bootstrap-name", "", "Bootstrap config template name (default <name>)")
	cmd.Flags().Bool("write", false, "Write manifests into the bedrock GitOps tree instead of applying live")
	return cmd
}

func runClusterCreate(cmd *cobra.Command, app *App) error {
	spec, err := clusterSpecFromFlags(cmd, app)
	if err != nil {
		return err
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		out, err := capi.RenderCluster(spec)
		if err != nil {
			return fmt.Errorf("cluster create: %w", err)
		}
		fmt.Print(string(out))
		return nil
	}

	write, _ := cmd.Flags().GetBool("write")
	if write {
		return writeClusterManifests(app, spec)
	}

	if err := app.CapiClient.ApplyCluster(cmdContext(cmd), spec); err != nil {
		return fmt.Errorf("cluster create: %w", err)
	}
	fmt.Printf("applied cluster %s/%s\n", spec.Namespace, spec.ClusterName)
	return nil
}

func clusterSpecFromFlags(cmd *cobra.Command, app *App) (capi.ClusterSpec, error) {
	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		return capi.ClusterSpec{}, fmt.Errorf("cluster create: --name is required")
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	if namespace == "" {
		namespace = app.Config.Pools.Namespace
	}
	version, _ := cmd.Flags().GetString("version")
	if version == "" {
		return capi.ClusterSpec{}, fmt.Errorf("cluster create: --version is required")
	}
	podCIDR, _ := cmd.Flags().GetString("pod-cidr")
	serviceCIDR, _ := cmd.Flags().GetString("service-cidr")
	replicas, _ := cmd.Flags().GetInt32("replicas")

	clusterInfraKind, _ := cmd.Flags().GetString("cluster-infra-kind")
	clusterInfraName, _ := cmd.Flags().GetString("cluster-infra-name")
	if clusterInfraName == "" {
		clusterInfraName = name
	}
	infraKind, _ := cmd.Flags().GetString("infra-kind")
	infraName, _ := cmd.Flags().GetString("infra-name")
	if infraName == "" {
		infraName = name + "-workers"
	}
	cpInfraKind, _ := cmd.Flags().GetString("cp-infra-kind")
	cpInfraName, _ := cmd.Flags().GetString("cp-infra-name")
	if cpInfraName == "" {
		cpInfraName = name + "-control-plane"
	}
	bootstrapKind, _ := cmd.Flags().GetString("bootstrap-kind")
	bootstrapName, _ := cmd.Flags().GetString("bootstrap-name")
	if bootstrapName == "" {
		bootstrapName = name
	}

	return capi.ClusterSpec{
		Name:             name,
		Namespace:        namespace,
		ClusterName:      name,
		ControlPlaneMode: capi.Managed,
		PodCIDR:          podCIDR,
		ServiceCIDR:      serviceCIDR,
		Version:          version,
		Replicas:         replicas,
		ClusterInfrastructure: capi.TemplateRef{
			APIGroup: infrastructureGroup,
			Kind:     clusterInfraKind,
			Name:     clusterInfraName,
		},
		Infrastructure: capi.TemplateRef{
			APIGroup: infrastructureGroup,
			Kind:     infraKind,
			Name:     infraName,
		},
		ControlPlaneInfra: capi.TemplateRef{
			APIGroup: infrastructureGroup,
			Kind:     cpInfraKind,
			Name:     cpInfraName,
		},
		Bootstrap: capi.TemplateRef{
			APIGroup: bootstrapGroup,
			Kind:     bootstrapKind,
			Name:     bootstrapName,
		},
	}, nil
}

func writeClusterManifests(app *App, spec capi.ClusterSpec) error {
	if app.Config.BedrockPath == "" {
		return fmt.Errorf("cluster create: --write requires bedrock_path in config")
	}
	data, err := capi.RenderCluster(spec)
	if err != nil {
		return fmt.Errorf("cluster create: %w", err)
	}
	repo, err := capi.OpenRepo(app.Config.BedrockPath)
	if err != nil {
		return fmt.Errorf("cluster create: %w", err)
	}
	path, err := repo.WriteCluster(spec.ClusterName, spec.Name, data)
	if err != nil {
		return fmt.Errorf("cluster create: %w", err)
	}
	fmt.Println(path)
	return nil
}

func newClusterDeleteCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a CAPI-managed cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterDelete(cmd, app)
		},
	}
	cmd.Flags().String("name", "", "Cluster name (required)")
	cmd.Flags().String("namespace", "", "Cluster namespace (default from config pools)")
	return cmd
}

func runClusterDelete(cmd *cobra.Command, app *App) error {
	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		return fmt.Errorf("cluster delete: --name is required")
	}
	namespace, _ := cmd.Flags().GetString("namespace")
	if namespace == "" {
		namespace = app.Config.Pools.Namespace
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		fmt.Printf("[dry-run] would delete cluster %s/%s\n", namespace, name)
		return nil
	}

	if err := app.CapiClient.DeleteCluster(cmdContext(cmd), namespace, name); err != nil {
		return fmt.Errorf("cluster delete: %w", err)
	}
	fmt.Printf("deleted cluster %s/%s\n", namespace, name)
	return nil
}

func newClusterListCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List CAPI-managed clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterList(cmd, app)
		},
	}
	cmd.Flags().String("namespace", "", "Cluster namespace (default from config pools)")
	return cmd
}

func runClusterList(cmd *cobra.Command, app *App) error {
	namespace, _ := cmd.Flags().GetString("namespace")
	if namespace == "" {
		namespace = app.Config.Pools.Namespace
	}

	clusters, err := app.CapiClient.ListClusters(cmdContext(cmd), namespace)
	if err != nil {
		return fmt.Errorf("cluster list: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()
	fmt.Fprintln(w, "NAME\tPHASE\tCONTROL-PLANE-INITIALIZED")
	for i := range clusters {
		c := &clusters[i]
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			c.Name, phaseOrDash(c.Status.Phase), boolOrDash(c.Status.Initialization.ControlPlaneInitialized))
	}
	return nil
}

func boolOrDash(b *bool) string {
	if b == nil {
		return "-"
	}
	return fmt.Sprintf("%t", *b)
}

func NewClusterCmdForTest(app *App) *cobra.Command { return newClusterCmd(app) }
