// Package web serves horizon's read-only interface over the live cluster state.
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

type Options struct {
	Client    client.Reader
	Catalogue catalogue.Reader
}

type Server struct {
	client    client.Reader
	catalogue catalogue.Reader
}

func New(opts Options) (*Server, error) {
	if opts.Client == nil {
		return nil, errors.New("web: a cluster client is required")
	}
	if opts.Catalogue == nil {
		return nil, errors.New("web: a catalogue reader is required")
	}
	return &Server{client: opts.Client, catalogue: opts.Catalogue}, nil
}

// the address is built here rather than taken from a caller, so no flag or option can widen the binding
func listenLoopback(port uint16) (net.Listener, error) {
	address := net.JoinHostPort(loopbackHost, strconv.Itoa(int(port)))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("web: listen on %s: %w", address, err)
	}
	return listener, nil
}

func (s *Server) ListenAndServe(ctx context.Context, port uint16) error {
	if port == 0 {
		return errors.New("web: a port is required")
	}
	listener, err := listenLoopback(port)
	if err != nil {
		return err
	}

	server := &http.Server{Handler: s.routes(), ReadHeaderTimeout: readHeaderTimeout}
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
