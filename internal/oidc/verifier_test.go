package oidc_test

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/oidc"
	"github.com/lucawalz/horizon/internal/web"
)

const (
	testAudience  = "horizon"
	signingKeyID  = "horizon-signing"
	hmacKeyID     = "horizon-hmac"
	testUsername  = "ada"
	tokenLifetime = time.Hour
	keySize       = 2048
)

var testGroups = []string{"platform", "operators"}

type issuer struct {
	url     string
	signing *rsa.PrivateKey
	secret  []byte
}

func startIssuer(t *testing.T) *issuer {
	t.Helper()
	signing, err := rsa.GenerateKey(rand.Reader, keySize)
	if err != nil {
		t.Fatalf("generate a signing key: %v", err)
	}
	published := &issuer{signing: signing, secret: []byte("a-shared-secret-nobody-should-trust")}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"issuer":                                published.url,
			"jwks_uri":                              published.url + "/keys",
			"authorization_endpoint":                published.url + "/auth",
			"token_endpoint":                        published.url + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256", "HS256"},
		})
	})
	// the symmetric key is published without an alg hint so an accepted HS256 token would verify, making its refusal a decision
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"keys": []any{
			map[string]any{
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"kid": signingKeyID,
				"n":   encodeSegment(signing.N.Bytes()),
				"e":   encodeSegment(big.NewInt(int64(signing.E)).Bytes()),
			},
			map[string]any{
				"kty": "oct",
				"use": "sig",
				"kid": hmacKeyID,
				"k":   encodeSegment(published.secret),
			},
		}})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	published.url = server.URL
	return published
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("write the discovery response: %v", err)
	}
}

func encodeSegment(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (i *issuer) claims() map[string]any {
	return map[string]any{
		"iss":                i.url,
		"aud":                testAudience,
		"sub":                testUsername,
		"iat":                time.Now().Unix(),
		"exp":                time.Now().Add(tokenLifetime).Unix(),
		"preferred_username": testUsername,
		"groups":             testGroups,
	}
}

func (i *issuer) authentication() web.Authentication {
	return web.Authentication{
		Issuer:        i.url,
		Audience:      testAudience,
		Header:        "Authorization",
		UsernameClaim: "preferred_username",
		GroupsClaim:   "groups",
	}
}

func (i *issuer) verifier(t *testing.T) *oidc.Verifier {
	t.Helper()
	verifier, err := oidc.NewVerifier(t.Context(), i.authentication())
	if err != nil {
		t.Fatalf("build the verifier: %v", err)
	}
	return verifier
}

func (i *issuer) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	return signRS256(t, i.signing, claims)
}

func signRS256(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	return assemble(t, map[string]any{"alg": "RS256", "typ": "JWT", "kid": signingKeyID}, claims,
		func(signed []byte) ([]byte, error) {
			digest := sha256.Sum256(signed)
			return rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		})
}

func signHS256(t *testing.T, secret []byte, claims map[string]any) string {
	t.Helper()
	return assemble(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": hmacKeyID}, claims,
		func(signed []byte) ([]byte, error) {
			mac := hmac.New(sha256.New, secret)
			mac.Write(signed)
			return mac.Sum(nil), nil
		})
}

func assemble(t *testing.T, header, claims map[string]any, sign func([]byte) ([]byte, error)) string {
	t.Helper()
	encode := func(part map[string]any) string {
		raw, err := json.Marshal(part)
		if err != nil {
			t.Fatalf("encode a token part: %v", err)
		}
		return encodeSegment(raw)
	}
	signed := encode(header) + "." + encode(claims)
	signature, err := sign([]byte(signed))
	if err != nil {
		t.Fatalf("sign the token: %v", err)
	}
	return signed + "." + encodeSegment(signature)
}

func TestVerifierAcceptsATokenFromTheIssuer(t *testing.T) {
	published := startIssuer(t)

	identity, err := published.verifier(t).VerifyToken(t.Context(), published.sign(t, published.claims()))
	if err != nil {
		t.Fatalf("verify a sound token: %v", err)
	}
	if identity.Username != testUsername {
		t.Errorf("username = %q, want %q", identity.Username, testUsername)
	}
	if !slices.Equal(identity.Groups, testGroups) {
		t.Errorf("groups = %v, want %v", identity.Groups, testGroups)
	}
}

func TestVerifierReadsTheConfiguredClaims(t *testing.T) {
	published := startIssuer(t)
	auth := published.authentication()
	auth.UsernameClaim = "email"
	auth.GroupsClaim = "roles"
	verifier, err := oidc.NewVerifier(t.Context(), auth)
	if err != nil {
		t.Fatalf("build the verifier: %v", err)
	}

	claims := published.claims()
	claims["email"] = "ada@example"
	claims["roles"] = []string{"admin"}

	identity, err := verifier.VerifyToken(t.Context(), published.sign(t, claims))
	if err != nil {
		t.Fatalf("verify a sound token: %v", err)
	}
	if identity.Username != "ada@example" {
		t.Errorf("username = %q, want ada@example", identity.Username)
	}
	if !slices.Equal(identity.Groups, []string{"admin"}) {
		t.Errorf("groups = %v, want [admin]", identity.Groups)
	}
}

// the identity provider in use answers with no memberships at all, which is an identity rather than a rejection
func TestVerifierAcceptsATokenWithoutGroups(t *testing.T) {
	published := startIssuer(t)
	claims := published.claims()
	delete(claims, "groups")

	identity, err := published.verifier(t).VerifyToken(t.Context(), published.sign(t, claims))
	if err != nil {
		t.Fatalf("verify a token without groups: %v", err)
	}
	if len(identity.Groups) != 0 {
		t.Errorf("groups = %v, want none", identity.Groups)
	}
}

func TestVerifierRejects(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"a wrong audience": func(claims map[string]any) { claims["aud"] = "somebody-else" },
		"a wrong issuer":   func(claims map[string]any) { claims["iss"] = "https://issuer.invalid" },
		"an expired token": func(claims map[string]any) {
			claims["exp"] = time.Now().Add(-tokenLifetime).Unix()
		},
		"a missing username claim": func(claims map[string]any) { delete(claims, "preferred_username") },
		"an empty username claim":  func(claims map[string]any) { claims["preferred_username"] = "" },
	} {
		t.Run(name, func(t *testing.T) {
			published := startIssuer(t)
			claims := published.claims()
			mutate(claims)

			identity, err := published.verifier(t).VerifyToken(t.Context(), published.sign(t, claims))
			if err == nil {
				t.Fatalf("%s was accepted as %q, want a rejection", name, identity.Username)
			}
		})
	}
}

func TestVerifierRejectsATokenSignedOutsideTheKeySet(t *testing.T) {
	published := startIssuer(t)
	foreign, err := rsa.GenerateKey(rand.Reader, keySize)
	if err != nil {
		t.Fatalf("generate a foreign key: %v", err)
	}

	identity, err := published.verifier(t).VerifyToken(t.Context(), signRS256(t, foreign, published.claims()))
	if err == nil {
		t.Fatalf("a foreign signature was accepted as %q, want a rejection", identity.Username)
	}
}

// a symmetric signature turns the published key set into a signing key, so an HS256 token is never an identity
func TestVerifierRejectsASymmetricallySignedToken(t *testing.T) {
	published := startIssuer(t)

	identity, err := published.verifier(t).VerifyToken(t.Context(), signHS256(t, published.secret, published.claims()))
	if err == nil {
		t.Fatalf("an HS256 token was accepted as %q, want a rejection", identity.Username)
	}
}

func TestNewVerifierNamesAnUnreachableIssuer(t *testing.T) {
	auth := web.Authentication{
		Issuer:        "http://127.0.0.1:1/realms/horizon",
		Audience:      testAudience,
		Header:        "Authorization",
		UsernameClaim: "preferred_username",
		GroupsClaim:   "groups",
	}

	_, err := oidc.NewVerifier(t.Context(), auth)
	if err == nil {
		t.Fatal("an unreachable issuer was accepted, want a refusal")
	}
	if !strings.Contains(err.Error(), auth.Issuer) {
		t.Errorf("error = %q, want it to name the issuer", err)
	}
}

func TestNewVerifierRefusesAnIncompleteAuthentication(t *testing.T) {
	if _, err := oidc.NewVerifier(context.Background(), web.Authentication{}); err == nil {
		t.Error("an empty authentication was accepted, want a refusal")
	}
}

// a key set from a source other than the issuer's own discovery document verifies signatures nobody vouched for
func TestVerifierTakesItsKeySetFromTheIssuerAlone(t *testing.T) {
	fields := reflect.TypeOf(web.Authentication{})
	for index := range fields.NumField() {
		name := strings.ToLower(fields.Field(index).Name)
		for _, forbidden := range []string{"jwks", "jwk", "keyset", "keys", "certs"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("field %s configures a key set, want it discovered from the issuer",
					fields.Field(index).Name)
			}
		}
	}
	if reflect.TypeOf(oidc.NewVerifier).NumIn() != 2 {
		t.Error("NewVerifier takes more than a context and an authentication, want the issuer as its only source")
	}
}
