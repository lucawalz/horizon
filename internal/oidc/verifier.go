// Package oidc verifies bearer tokens against the key set the configured issuer publishes about itself.
package oidc

import (
	"context"
	"encoding/json"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	"github.com/lucawalz/horizon/internal/web"
)

// a symmetric signature makes the published key set a signing key, so only algorithms the issuer alone can sign with are accepted
var asymmetricAlgorithms = []string{
	gooidc.RS256, gooidc.RS384, gooidc.RS512,
	gooidc.ES256, gooidc.ES384, gooidc.ES512,
	gooidc.PS256, gooidc.PS384, gooidc.PS512,
	gooidc.EdDSA,
}

type Verifier struct {
	tokens        *gooidc.IDTokenVerifier
	usernameClaim string
	groupsClaim   string
}

var _ web.TokenVerifier = (*Verifier)(nil)

func NewVerifier(ctx context.Context, auth web.Authentication) (*Verifier, error) {
	if err := auth.Validate(); err != nil {
		return nil, err
	}
	provider, err := gooidc.NewProvider(ctx, auth.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover the oidc issuer %s: %w", auth.Issuer, err)
	}
	return &Verifier{
		tokens: provider.Verifier(&gooidc.Config{
			ClientID:             auth.Audience,
			SupportedSigningAlgs: asymmetricAlgorithms,
		}),
		usernameClaim: auth.UsernameClaim,
		groupsClaim:   auth.GroupsClaim,
	}, nil
}

func (v *Verifier) VerifyToken(ctx context.Context, token string) (web.Identity, error) {
	verified, err := v.tokens.Verify(ctx, token)
	if err != nil {
		return web.Identity{}, fmt.Errorf("verify the bearer token: %w", err)
	}
	var claims map[string]json.RawMessage
	if err := verified.Claims(&claims); err != nil {
		return web.Identity{}, fmt.Errorf("read the claims of a verified token: %w", err)
	}
	username, err := name(claims, v.usernameClaim)
	if err != nil {
		return web.Identity{}, err
	}
	groups, err := memberships(claims, v.groupsClaim)
	if err != nil {
		return web.Identity{}, err
	}
	return web.Identity{Username: username, Groups: groups}, nil
}

// an empty username names nobody and would read as anonymous access, so an absent claim is a rejection
func name(claims map[string]json.RawMessage, claim string) (string, error) {
	raw, present := claims[claim]
	if !present {
		return "", fmt.Errorf("the token carries no %s claim to name its holder", claim)
	}
	var username string
	if err := json.Unmarshal(raw, &username); err != nil {
		return "", fmt.Errorf("read the %s claim as a name: %w", claim, err)
	}
	if username == "" {
		return "", fmt.Errorf("the %s claim is empty, so the token names nobody", claim)
	}
	return username, nil
}

func memberships(claims map[string]json.RawMessage, claim string) ([]string, error) {
	raw, present := claims[claim]
	if !present {
		return nil, nil
	}
	var groups []string
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("read the %s claim as a list of memberships: %w", claim, err)
	}
	return groups, nil
}
