package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
)

const signatureUse = "sig"

type keySetLocation struct {
	URL string `json:"jwks_uri"`
}

func assertKeySetCanVerify(ctx context.Context, provider *gooidc.Provider, issuer string) error {
	var location keySetLocation
	if err := provider.Claims(&location); err != nil {
		return fmt.Errorf("read the discovery document of the oidc issuer %s: %w", issuer, err)
	}
	if location.URL == "" {
		return fmt.Errorf("the oidc issuer %s names no key set in its discovery document", issuer)
	}
	keys, err := fetchKeySet(ctx, location.URL)
	if err != nil {
		return fmt.Errorf("fetch the key set %s of the oidc issuer %s: %w", location.URL, issuer, err)
	}
	if !slices.ContainsFunc(keys, verifiesAsymmetricSignatures) {
		return fmt.Errorf("the key set %s of the oidc issuer %s publishes no usable signing key, so every token would be rejected",
			location.URL, issuer)
	}
	return nil
}

func fetchKeySet(ctx context.Context, url string) ([]jose.JSONWebKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the key set answered %s", response.Status)
	}
	var published jose.JSONWebKeySet
	if err := json.NewDecoder(response.Body).Decode(&published); err != nil {
		return nil, fmt.Errorf("read the key set as a jwk set: %w", err)
	}
	return published.Keys, nil
}

func verifiesAsymmetricSignatures(key jose.JSONWebKey) bool {
	if !key.Valid() || !key.IsPublic() {
		return false
	}
	if key.Use != "" && key.Use != signatureUse {
		return false
	}
	return key.Algorithm == "" || slices.Contains(asymmetricAlgorithms, key.Algorithm)
}
