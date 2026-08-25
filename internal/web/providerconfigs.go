package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	unreadableConfig   = "the request body is not a provider config this interface can submit"
	configCreateFailed = "the provider config could not be created in the cluster"
)

type secretKeyRequest struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type hetznerProviderRequest struct {
	CredentialsSecretRef    secretKeyRequest  `json:"credentialsSecretRef"`
	NodeCredentialSecretRef *secretKeyRequest `json:"nodeCredentialSecretRef"`
	JoinTokenSecretRef      *secretKeyRequest `json:"joinTokenSecretRef"`
	CloudInitSecretRef      secretKeyRequest  `json:"cloudInitSecretRef"`
	Image                   string            `json:"image"`
	SSHKeys                 []string          `json:"sshKeys"`
	Firewalls               []string          `json:"firewalls"`
}

type watchdogRequest struct {
	RenewIntervalSeconds int64 `json:"renewIntervalSeconds"`
	SlackSeconds         int64 `json:"slackSeconds"`
	MaxLifetimeSeconds   int64 `json:"maxLifetimeSeconds"`
}

type providerConfigCreateRequest struct {
	Name     string                  `json:"name"`
	Type     string                  `json:"type"`
	Hetzner  *hetznerProviderRequest `json:"hetzner"`
	Watchdog watchdogRequest         `json:"watchdog"`
}

func (r secretKeyRequest) selector() corev1.SecretKeySelector {
	return corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: r.Name},
		Key:                  r.Key,
	}
}

func (r *secretKeyRequest) optionalSelector() *corev1.SecretKeySelector {
	if r == nil {
		return nil
	}
	return ptr(r.selector())
}

func (r *hetznerProviderRequest) providerSpec() *v1alpha1.HetznerProviderSpec {
	if r == nil {
		return nil
	}
	return &v1alpha1.HetznerProviderSpec{
		CredentialsSecretRef:    r.CredentialsSecretRef.selector(),
		NodeCredentialSecretRef: r.NodeCredentialSecretRef.optionalSelector(),
		JoinTokenSecretRef:      r.JoinTokenSecretRef.optionalSelector(),
		CloudInitSecretRef:      r.CloudInitSecretRef.selector(),
		Image:                   namedImage(r.Image),
		SSHKeys:                 r.SSHKeys,
		Firewalls:               r.Firewalls,
	}
}

func namedImage(name string) *v1alpha1.ImageSpec {
	if name == "" {
		return nil
	}
	return &v1alpha1.ImageSpec{Name: name}
}

func (r watchdogRequest) policy() (v1alpha1.WatchdogPolicy, error) {
	renew, err := watchdogSpan("renewIntervalSeconds", r.RenewIntervalSeconds)
	if err != nil {
		return v1alpha1.WatchdogPolicy{}, err
	}
	slack, err := watchdogSpan("slackSeconds", r.SlackSeconds)
	if err != nil {
		return v1alpha1.WatchdogPolicy{}, err
	}
	lifetime, err := watchdogSpan("maxLifetimeSeconds", r.MaxLifetimeSeconds)
	if err != nil {
		return v1alpha1.WatchdogPolicy{}, err
	}
	return v1alpha1.WatchdogPolicy{
		RenewInterval: metav1.Duration{Duration: renew},
		Slack:         metav1.Duration{Duration: slack},
		MaxLifetime:   metav1.Duration{Duration: lifetime},
	}, nil
}

// past this bound a count of seconds no longer survives the conversion, and the apiserver would quote back a duration nobody asked for
func watchdogSpan(field string, requested int64) (time.Duration, error) {
	if requested <= 0 || requested > maxDurationSeconds {
		return 0, fmt.Errorf("%d is not a number of seconds %s can hold", requested, field)
	}
	return span(requested), nil
}

func (r providerConfigCreateRequest) config() (*v1alpha1.ProviderConfig, error) {
	watchdog, err := r.Watchdog.policy()
	if err != nil {
		return nil, err
	}
	return &v1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: r.Name},
		Spec: v1alpha1.ProviderConfigSpec{
			Type:     r.Type,
			Hetzner:  r.Hetzner.providerSpec(),
			Watchdog: watchdog,
		},
	}, nil
}

// the form names the secrets it references and creates none, so nothing here reaches the namespace holding the controller's own credentials
func (s *Server) providerConfigCreate(w http.ResponseWriter, r *http.Request) {
	writer, held := requestClient(w, r, s.writers.configs)
	if !held {
		return
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var submitted providerConfigCreateRequest
	if err := decoder.Decode(&submitted); err != nil {
		writeAPIError(w, http.StatusBadRequest, unreadableConfig+": "+err.Error())
		return
	}

	config, err := submitted.config()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := writer.Create(r.Context(), config); err != nil {
		if refusedByCluster(w, r, err) {
			return
		}
		slog.Error("create the provider config", "config", config.Name, "error", err)
		writeAPIError(w, http.StatusBadGateway, configCreateFailed)
		return
	}
	writeJSON(w, http.StatusCreated, newProviderConfigSummary(config))
}
