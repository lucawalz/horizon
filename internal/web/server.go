// Package web serves horizon's read-only interface over the live cluster state.
package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
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
	pollInterval      = 5 * time.Second
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
	pages     map[page]*template.Template
}

func New(opts Options) (*Server, error) {
	if opts.Client == nil {
		return nil, errors.New("web: a cluster client is required")
	}
	if opts.Catalogue == nil {
		return nil, errors.New("web: a catalogue reader is required")
	}
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	return &Server{client: opts.Client, catalogue: opts.Catalogue, pages: pages}, nil
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

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.leaseList(layoutTemplate))
	mux.HandleFunc("GET /fragments/leases", s.leaseList(leaseTableTemplate))
	mux.HandleFunc("GET /leases/{name}", s.leaseDetail(layoutTemplate))
	mux.HandleFunc("GET /fragments/leases/{name}", s.leaseDetail(leaseBodyTemplate))
	mux.HandleFunc("GET /machines", s.machines(layoutTemplate))
	mux.Handle("GET /assets/", http.FileServerFS(assetFS))
	return mux
}

func (s *Server) render(w http.ResponseWriter, name page, block string, status int, data any) {
	set, known := s.pages[name]
	if !known {
		http.Error(w, "the page is not registered", http.StatusInternalServerError)
		return
	}

	var body bytes.Buffer
	if err := set.ExecuteTemplate(&body, block, data); err != nil {
		slog.Error("render the interface", "page", name, "block", block, "error", err)
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = body.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, block string, status int, detail string) {
	if block != layoutTemplate {
		http.Error(w, detail, status)
		return
	}
	s.render(w, errorPage, layoutTemplate, status, newView(http.StatusText(status), failure{
		Heading: http.StatusText(status),
		Detail:  detail,
	}))
}

type failure struct {
	Heading string
	Detail  string
}

type view struct {
	Title string
	Poll  string
	Body  any
}

func newView(title string, body any) view {
	return view{Title: title, Poll: pollInterval.String(), Body: body}
}

// a fragment is swapped into a shell that already exists, so it renders the body on its own
func payload(block, title string, body any) any {
	if block == layoutTemplate {
		return newView(title, body)
	}
	return body
}
