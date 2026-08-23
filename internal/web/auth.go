package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	issuerSetting        = "oidc-issuer"
	audienceSetting      = "oidc-audience"
	headerSetting        = "auth-header"
	usernameClaimSetting = "username-claim"
	groupsClaimSetting   = "groups-claim"

	bearerPrefix       = "Bearer "
	credentialMissing  = "this interface requires a bearer token in "
	credentialRejected = "the bearer token could not be verified"
)

var errNoVerifier = errors.New("web: no token verifier is wired, so no credential can be verified")

type Identity struct {
	Username string
	Groups   []string
}

type TokenVerifier interface {
	VerifyToken(ctx context.Context, token string) (Identity, error)
}

type Authentication struct {
	Issuer         string
	Audience       string
	Header         string
	UsernameClaim  string
	GroupsClaim    string
	ExternalOrigin string

	Verifier TokenVerifier
}

func (a Authentication) Validate() error {
	var missing []string
	for _, setting := range []struct{ name, value string }{
		{issuerSetting, a.Issuer},
		{audienceSetting, a.Audience},
		{headerSetting, a.Header},
		{usernameClaimSetting, a.UsernameClaim},
		{groupsClaimSetting, a.GroupsClaim},
	} {
		if setting.value == "" {
			missing = append(missing, setting.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("an authenticated interface requires %s", strings.Join(missing, ", "))
}

type rejectingVerifier struct{}

func (rejectingVerifier) VerifyToken(context.Context, string) (Identity, error) {
	return Identity{}, errNoVerifier
}

// an unfilled seam must refuse rather than admit, so a build that never gains a verifier serves nobody
func (a Authentication) verifier() TokenVerifier {
	if a.Verifier == nil {
		return rejectingVerifier{}
	}
	return a.Verifier
}

type identityKey struct{}

func withIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

func IdentityFrom(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok
}

// a proxy may forward the credential bare, so the scheme is stripped when it is present rather than demanded
func bearerToken(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) >= len(bearerPrefix) && strings.EqualFold(value[:len(bearerPrefix)], bearerPrefix) {
		value = strings.TrimSpace(value[len(bearerPrefix):])
	}
	return value, value != ""
}

func (s *Server) authenticated(next http.Handler) http.Handler {
	verifier := s.auth.verifier()
	header := s.auth.Header
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, carried := bearerToken(r.Header.Get(header))
		if !carried {
			writeAPIError(w, http.StatusUnauthorized, credentialMissing+header)
			return
		}
		identity, err := verifier.VerifyToken(r.Context(), token)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, credentialRejected)
			return
		}
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), identity)))
	})
}

func (s *Server) handler() http.Handler {
	if s.auth == nil {
		return s.routes()
	}
	return s.authenticated(s.routes())
}
