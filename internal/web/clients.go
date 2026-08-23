package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	unidentifiedRequest = "this interface answers only for a verified identity, and this request carries none"
	clientUnbuildable   = "no cluster client could be built for this request"
	refusedAs           = "the cluster refused this request as "
)

var errUnidentifiedRequest = errors.New("web: a request reached the cluster with no verified identity")

// the reader and the writer are built separately, so a process handed no writer factory holds nothing a request could mutate through
type ReaderFactory interface {
	ReaderFor(identity Identity) (client.Reader, error)
}

type WriterFactory interface {
	WriterFor(identity Identity) (LeaseWriter, error)
}

// each request reaches the cluster as the identity it carries rather than as this process
type Impersonation struct {
	Client ReaderFactory

	Writer WriterFactory
}

type source[T any] interface {
	get(ctx context.Context) (T, error)
}

type constructed[T any] struct{ held T }

func (c constructed[T]) get(context.Context) (T, error) { return c.held, nil }

type perIdentity[T any] struct {
	build func(Identity) (T, error)
}

// falling through to this process's own credentials would hand every unidentified caller the permissions it holds
func (p perIdentity[T]) get(ctx context.Context) (T, error) {
	identity, verified := IdentityFrom(ctx)
	if !verified {
		var none T
		return none, errUnidentifiedRequest
	}
	return p.build(identity)
}

func readerSource(opts Options) (source[client.Reader], error) {
	if opts.Impersonation == nil {
		if opts.Client == nil {
			return nil, errors.New("web: a cluster client is required")
		}
		return constructed[client.Reader]{held: opts.Client}, nil
	}
	if opts.Client != nil || opts.Writer != nil {
		return nil, errors.New("web: a cluster client and impersonated clients are alternatives, not a pair")
	}
	if opts.Authentication == nil {
		return nil, errors.New("web: impersonated clients require an authenticated interface, since only a verified identity can be impersonated")
	}
	if opts.Impersonation.Client == nil {
		return nil, errors.New("web: a cluster client factory is required")
	}
	return perIdentity[client.Reader]{build: opts.Impersonation.Client.ReaderFor}, nil
}

// an absent writer leaves the server holding no writer at all, so a mutation is refused before any client is built for it
func writerSource(opts Options) source[LeaseWriter] {
	if opts.Impersonation != nil {
		if opts.Impersonation.Writer == nil {
			return nil
		}
		return perIdentity[LeaseWriter]{build: opts.Impersonation.Writer.WriterFor}
	}
	if opts.Writer == nil {
		return nil
	}
	return constructed[LeaseWriter]{held: opts.Writer}
}

func requestClient[T any](w http.ResponseWriter, r *http.Request, from source[T]) (T, bool) {
	held, err := from.get(r.Context())
	if err == nil {
		return held, true
	}

	if errors.Is(err, errUnidentifiedRequest) {
		writeAPIError(w, http.StatusUnauthorized, unidentifiedRequest)
	} else {
		slog.Error("build a cluster client for the request", "error", err)
		writeAPIError(w, http.StatusInternalServerError, clientUnbuildable)
	}
	var none T
	return none, false
}

// the adopter's RBAC decides what an impersonated caller may do, so a denial reads as an authorisation failure rather than a fault
func refusedByAuthorisation(w http.ResponseWriter, r *http.Request, err error) bool {
	if !apierrors.IsForbidden(err) {
		return false
	}
	writeAPIError(w, http.StatusForbidden, authorisationDetail(r.Context(), err))
	return true
}

func authorisationDetail(ctx context.Context, err error) string {
	identity, verified := IdentityFrom(ctx)
	if !verified {
		return err.Error()
	}
	return refusedAs + identity.Username + ": " + err.Error()
}
