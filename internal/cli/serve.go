package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/internal/manager"
	"github.com/lucawalz/horizon/internal/oidc"
	"github.com/lucawalz/horizon/internal/web"
)

const (
	defaultServePort     = 8082
	anyHost              = "0.0.0.0"
	defaultAuthHeader    = "Authorization"
	defaultUsernameClaim = "preferred_username"
	defaultGroupsClaim   = "groups"
)

type ServeOptions struct {
	BindAddress    string
	Authentication web.Authentication
}

func (o ServeOptions) Validate() error {
	if o.BindAddress == "" {
		return errors.New("a bind address is required")
	}
	return o.Authentication.Validate()
}

func NewServeCmdForTest() (*cobra.Command, *ServeOptions) { return newServeCmd() }

func RunServeForTest(ctx context.Context, out io.Writer, opts ServeOptions) error {
	return runServe(ctx, out, opts)
}

func newServeCmd() (*cobra.Command, *ServeOptions) {
	opts := &ServeOptions{}

	cmd := &cobra.Command{
		Use:          "serve",
		Short:        "Serve the web interface on a routable address behind an OIDC issuer",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		PreRunE: func(*cobra.Command, []string) error {
			return opts.Validate()
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(ctrl.SetupSignalHandler(), cmd.OutOrStdout(), *opts)
		},
	}

	auth := &opts.Authentication
	flags := cmd.Flags()
	flags.StringVar(&opts.BindAddress, "bind-address", net.JoinHostPort(anyHost, strconv.Itoa(defaultServePort)),
		"Address the interface binds to")
	flags.StringVar(&auth.Header, "auth-header", defaultAuthHeader,
		"Request header the bearer token arrives in")
	flags.StringVar(&auth.Issuer, "oidc-issuer", "",
		"Issuer whose tokens the interface accepts, and whose discovery document names its key set")
	flags.StringVar(&auth.Audience, "oidc-audience", "",
		"Audience a token must be issued for")
	flags.StringVar(&auth.UsernameClaim, "username-claim", defaultUsernameClaim,
		"Claim the username is read from")
	flags.StringVar(&auth.GroupsClaim, "groups-claim", defaultGroupsClaim,
		"Claim the group memberships are read from")
	flags.StringVar(&auth.ExternalOrigin, "external-origin", "",
		"Origin the interface is reached on from outside the cluster")

	return cmd, opts
}

func serveOptions(api client.Client, auth web.Authentication) web.Options {
	return web.Options{Client: api, Writer: api, Catalogue: web.AbsentCatalogue(), Authentication: &auth}
}

func runServe(ctx context.Context, out io.Writer, opts ServeOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	verifier, err := oidc.NewVerifier(ctx, opts.Authentication)
	if err != nil {
		return err
	}
	opts.Authentication.Verifier = verifier

	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load the kubeconfig: %w", err)
	}
	api, err := client.New(restConfig, client.Options{Scheme: manager.Scheme()})
	if err != nil {
		return fmt.Errorf("build the cluster client: %w", err)
	}
	server, err := web.New(serveOptions(api, opts.Authentication))
	if err != nil {
		return err
	}

	bind := web.ExplicitAddress(opts.BindAddress)
	if _, err := fmt.Fprintf(out, "serving the horizon interface on %s\n", bind); err != nil {
		return err
	}
	return server.ListenAndServe(ctx, bind)
}
