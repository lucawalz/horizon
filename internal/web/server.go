// Package web serves horizon's interface over the live cluster state, reading always and mutating only where a writer is supplied.
package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lucawalz/horizon/internal/catalogue"
)

const (
	loopbackHost      = "127.0.0.1"
	readHeaderTimeout = 10 * time.Second
	shutdownGrace     = 5 * time.Second
)

type LeaseWriter interface {
	Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
	Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
}

type Options struct {
	Client client.Reader

	// the read-only guarantee is the type of this field, so an embedder that supplies none cannot be made to write
	Writer LeaseWriter

	Catalogue catalogue.Reader

	// a binding wider than loopback is reachable by anyone who can route to it, so it only serves once this stands in front of it
	Authentication *Authentication
}

type Server struct {
	client    client.Reader
	writer    LeaseWriter
	catalogue catalogue.Reader
	auth      *Authentication
	port      string
}

func New(opts Options) (*Server, error) {
	if opts.Client == nil {
		return nil, errors.New("web: a cluster client is required")
	}
	if opts.Catalogue == nil {
		return nil, errors.New("web: a catalogue reader is required")
	}
	if opts.Authentication != nil {
		if err := opts.Authentication.Validate(); err != nil {
			return nil, err
		}
	}
	return &Server{
		client:    opts.Client,
		writer:    opts.Writer,
		catalogue: opts.Catalogue,
		auth:      opts.Authentication,
	}, nil
}

// a request cannot vouch for its own Host header, so the guard is anchored to the address the listener actually bound
func (s *Server) anchorTo(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("web: read the bound port from %s: %w", address, err)
	}
	s.port = port
	return nil
}

// the address is unreachable except through a constructor that names its reach, so a caller cannot widen the binding without saying so
type BindAddress struct {
	address string
}

func LoopbackAddress(port uint16) BindAddress {
	if port == 0 {
		return BindAddress{}
	}
	return BindAddress{address: net.JoinHostPort(loopbackHost, strconv.Itoa(int(port)))}
}

func ExplicitAddress(address string) BindAddress {
	return BindAddress{address: address}
}

func (b BindAddress) String() string { return b.address }

func (b BindAddress) listen() (net.Listener, error) {
	if b.address == "" {
		return nil, errors.New("web: a bind address is required")
	}
	listener, err := net.Listen("tcp", b.address)
	if err != nil {
		return nil, fmt.Errorf("web: listen on %s: %w", b.address, err)
	}
	return listener, nil
}

func (s *Server) ListenAndServe(ctx context.Context, bind BindAddress) error {
	listener, err := bind.listen()
	if err != nil {
		return err
	}
	if err := s.anchorTo(listener.Addr().String()); err != nil {
		return errors.Join(err, listener.Close())
	}

	server := &http.Server{Handler: s.handler(), ReadHeaderTimeout: readHeaderTimeout}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(grace); err != nil {
			slog.Error("shut the interface down", "error", err)
		}
	}()

	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("web: serve: %w", err)
	}
	<-stopped
	return nil
}
