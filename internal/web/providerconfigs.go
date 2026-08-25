package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	providerConfigKind = "provider config"
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

// a reference tells a missing secret apart from a misnamed one, and resolving it would put a credential in a browser
type secretKeyReference struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type imageSelection struct {
	Name     *string           `json:"name"`
	ID       *int64            `json:"id"`
	Selector map[string]string `json:"selector"`
}

type hetznerProviderDetail struct {
	CredentialsSecretRef    secretKeyReference  `json:"credentialsSecretRef"`
	NodeCredentialSecretRef *secretKeyReference `json:"nodeCredentialSecretRef"`
	JoinTokenSecretRef      *secretKeyReference `json:"joinTokenSecretRef"`
	CloudInitSecretRef      secretKeyReference  `json:"cloudInitSecretRef"`
	Image                   *imageSelection     `json:"image"`
	ImageSelector           map[string]string   `json:"imageSelector"`
	SSHKeys                 []string            `json:"sshKeys"`
	Firewalls               []string            `json:"firewalls"`
}

type watchdogDetail struct {
	RenewIntervalSeconds int64 `json:"renewIntervalSeconds"`
	SlackSeconds         int64 `json:"slackSeconds"`
	MaxLifetimeSeconds   int64 `json:"maxLifetimeSeconds"`
}

type catalogueRegion struct {
	Region string `json:"region"`
	Types  int    `json:"types"`
}

// a published catalogue runs to hundreds of entries, so it is tallied here and listed by the machines route
type publishedCatalogue struct {
	Types       int               `json:"types"`
	Regions     []catalogueRegion `json:"regions"`
	RefreshedAt *string           `json:"refreshedAt"`
}

type providerConfigDetailResponse struct {
	Summary    providerConfigSummary  `json:"summary"`
	Hetzner    *hetznerProviderDetail `json:"hetzner"`
	Watchdog   watchdogDetail         `json:"watchdog"`
	Catalogue  publishedCatalogue     `json:"catalogue"`
	Conditions []conditionEntry       `json:"conditions"`
	ObservedAt string                 `json:"observedAt"`
}

func newSecretKeyReference(selector corev1.SecretKeySelector) secretKeyReference {
	return secretKeyReference{Name: selector.Name, Key: selector.Key}
}

func newOptionalSecretKeyReference(selector *corev1.SecretKeySelector) *secretKeyReference {
	if selector == nil {
		return nil
	}
	return ptr(newSecretKeyReference(*selector))
}

func newImageSelection(image *v1alpha1.ImageSpec) *imageSelection {
	if image == nil {
		return nil
	}
	selection := imageSelection{Name: nullable(image.Name), Selector: image.Selector}
	if image.ID != 0 {
		selection.ID = ptr(image.ID)
	}
	return &selection
}

func newHetznerProviderDetail(spec *v1alpha1.HetznerProviderSpec) *hetznerProviderDetail {
	if spec == nil {
		return nil
	}
	return &hetznerProviderDetail{
		CredentialsSecretRef:    newSecretKeyReference(spec.CredentialsSecretRef),
		NodeCredentialSecretRef: newOptionalSecretKeyReference(spec.NodeCredentialSecretRef),
		JoinTokenSecretRef:      newOptionalSecretKeyReference(spec.JoinTokenSecretRef),
		CloudInitSecretRef:      newSecretKeyReference(spec.CloudInitSecretRef),
		Image:                   newImageSelection(spec.Image),
		ImageSelector:           spec.ImageSelector,
		SSHKeys:                 orEmpty(spec.SSHKeys),
		Firewalls:               orEmpty(spec.Firewalls),
	}
}

func newWatchdogDetail(policy v1alpha1.WatchdogPolicy) watchdogDetail {
	return watchdogDetail{
		RenewIntervalSeconds: seconds(policy.RenewInterval.Duration),
		SlackSeconds:         seconds(policy.Slack.Duration),
		MaxLifetimeSeconds:   seconds(policy.MaxLifetime.Duration),
	}
}

func newPublishedCatalogue(status v1alpha1.ProviderConfigStatus) publishedCatalogue {
	offered := map[string]int{}
	for _, published := range status.InstanceTypes {
		offered[published.Region]++
	}

	regions := make([]catalogueRegion, 0, len(offered))
	for region, types := range offered {
		regions = append(regions, catalogueRegion{Region: region, Types: types})
	}
	slices.SortFunc(regions, func(a, b catalogueRegion) int { return strings.Compare(a.Region, b.Region) })

	return publishedCatalogue{
		Types:       len(status.InstanceTypes),
		Regions:     regions,
		RefreshedAt: instant(status.CatalogueRefreshedAt),
	}
}

func newProviderConfigDetailResponse(config *v1alpha1.ProviderConfig, now time.Time) providerConfigDetailResponse {
	return providerConfigDetailResponse{
		Summary:    newProviderConfigSummary(config),
		Hetzner:    newHetznerProviderDetail(config.Spec.Hetzner),
		Watchdog:   newWatchdogDetail(config.Spec.Watchdog),
		Catalogue:  newPublishedCatalogue(config.Status),
		Conditions: newConditionEntries(config.Status.Conditions),
		ObservedAt: rfc3339(now),
	}
}

func (s *Server) providerConfigDetail(w http.ResponseWriter, r *http.Request) {
	var config v1alpha1.ProviderConfig
	if !s.objectNamed(w, r, providerConfigKind, r.PathValue("name"), &config, configReadFailed) {
		return
	}
	writeJSON(w, http.StatusOK, newProviderConfigDetailResponse(&config, time.Now()))
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
