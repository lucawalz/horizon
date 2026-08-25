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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lucawalz/horizon/api/v1alpha1"
)

const (
	providerConfigKind  = "provider config"
	unreadableConfig    = "the request body is not a provider config this interface can submit"
	configCreateFailed  = "the provider config could not be created in the cluster"
	configReplaceFailed = "the provider config could not be replaced in the cluster"
	configDeleteFailed  = "the provider config could not be deleted"
	deleteRequested     = "the controller was asked to delete this provider config. it holds the object back until no " +
		"capacity lease names it, because horizon tears a lease down with the credentials this configuration resolves"
	replaceRaced = "another change to this provider config landed first, so this replacement was measured against a spec " +
		"the config no longer carries. reading it again and submitting the change against what it now holds is the way through"
	deleteRefused = "releasing those leases is what frees this configuration. horizon tears each of them down with the " +
		"credentials resolved from here, so a configuration deleted first leaves their machines billing until the watchdog on each node powers it off"
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

type providerConfigSpecRequest struct {
	Type     string                  `json:"type"`
	Hetzner  *hetznerProviderRequest `json:"hetzner"`
	Watchdog watchdogRequest         `json:"watchdog"`
}

type providerConfigCreateRequest struct {
	Name string `json:"name"`
	providerConfigSpecRequest
}

type providerConfigDeleteResponse struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
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

func newSecretKeyRequest(ref corev1.SecretKeySelector) secretKeyRequest {
	return secretKeyRequest{Name: ref.Name, Key: ref.Key}
}

func newOptionalSecretKeyRequest(ref *corev1.SecretKeySelector) *secretKeyRequest {
	if ref == nil {
		return nil
	}
	return ptr(newSecretKeyRequest(*ref))
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

func newHetznerProviderRequest(spec *v1alpha1.HetznerProviderSpec) *hetznerProviderRequest {
	// this interface submits a named image and nothing else, so a configuration written any other way is reported as one this shape cannot carry
	if spec == nil || len(spec.ImageSelector) > 0 || spec.Image == nil || spec.Image.Name == "" {
		return nil
	}
	return &hetznerProviderRequest{
		CredentialsSecretRef:    newSecretKeyRequest(spec.CredentialsSecretRef),
		NodeCredentialSecretRef: newOptionalSecretKeyRequest(spec.NodeCredentialSecretRef),
		JoinTokenSecretRef:      newOptionalSecretKeyRequest(spec.JoinTokenSecretRef),
		CloudInitSecretRef:      newSecretKeyRequest(spec.CloudInitSecretRef),
		Image:                   spec.Image.Name,
		SSHKeys:                 orEmpty(spec.SSHKeys),
		Firewalls:               orEmpty(spec.Firewalls),
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

func newWatchdogRequest(policy v1alpha1.WatchdogPolicy) watchdogRequest {
	return watchdogRequest{
		RenewIntervalSeconds: seconds(policy.RenewInterval.Duration),
		SlackSeconds:         seconds(policy.Slack.Duration),
		MaxLifetimeSeconds:   seconds(policy.MaxLifetime.Duration),
	}
}

// past this bound a count of seconds no longer survives the conversion, and the apiserver would quote back a duration nobody asked for
func watchdogSpan(field string, requested int64) (time.Duration, error) {
	if requested <= 0 || requested > maxDurationSeconds {
		return 0, fmt.Errorf("%d is not a number of seconds %s can hold", requested, field)
	}
	return span(requested), nil
}

func (r providerConfigSpecRequest) spec() (v1alpha1.ProviderConfigSpec, error) {
	watchdog, err := r.Watchdog.policy()
	if err != nil {
		return v1alpha1.ProviderConfigSpec{}, err
	}
	return v1alpha1.ProviderConfigSpec{
		Type:     r.Type,
		Hetzner:  r.Hetzner.providerSpec(),
		Watchdog: watchdog,
	}, nil
}

func newProviderConfigSpecRequest(spec v1alpha1.ProviderConfigSpec) *providerConfigSpecRequest {
	hetzner := newHetznerProviderRequest(spec.Hetzner)
	if hetzner == nil {
		return nil
	}
	return &providerConfigSpecRequest{
		Type:     spec.Type,
		Hetzner:  hetzner,
		Watchdog: newWatchdogRequest(spec.Watchdog),
	}
}

func (r providerConfigCreateRequest) config() (*v1alpha1.ProviderConfig, error) {
	spec, err := r.spec()
	if err != nil {
		return nil, err
	}
	return &v1alpha1.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: r.Name}, Spec: spec}, nil
}

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
	// the reference tells a missing secret apart from a misnamed one, and resolving it would put a credential in a browser
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
	// a published catalogue runs to hundreds of entries, so it is tallied here and listed by the machines route
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

func decodeConfigBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var submitted T
	if err := decoder.Decode(&submitted); err != nil {
		writeAPIError(w, http.StatusBadRequest, unreadableConfig+": "+err.Error())
		var unread T
		return unread, false
	}
	return submitted, true
}

func writeConfigRefusal(w http.ResponseWriter, r *http.Request, err error, name, fallback string) {
	if apierrors.IsNotFound(err) {
		writeNotFound(w, providerConfigKind, name)
		return
	}
	if refusedByCluster(w, r, err) {
		return
	}
	slog.Error("mutate the provider config", "config", name, "error", err)
	writeAPIError(w, http.StatusBadGateway, fallback)
}

// the form names the secrets it references and creates none, so nothing here reaches the namespace holding the controller's own credentials
func (s *Server) providerConfigCreate(w http.ResponseWriter, r *http.Request) {
	writer, held := requestClient(w, r, s.writers.configs)
	if !held {
		return
	}
	submitted, readable := decodeConfigBody[providerConfigCreateRequest](w, r)
	if !readable {
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

func (s *Server) providerConfigReplace(w http.ResponseWriter, r *http.Request) {
	// a lease bound to this config reads the replaced spec on the controller's next pass, which is what makes rotating a credential possible while capacity is held
	writer, held := requestClient(w, r, s.writers.configs)
	if !held {
		return
	}
	name := r.PathValue("name")
	if refusedAsAnInvalidName(w, providerConfigKind, name) {
		return
	}
	submitted, readable := decodeConfigBody[providerConfigSpecRequest](w, r)
	if !readable {
		return
	}

	spec, err := submitted.spec()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	replaced, err := writer.Replace(r.Context(), name, spec)
	if err != nil {
		if apierrors.IsConflict(err) {
			writeAPIError(w, http.StatusConflict,
				fmt.Sprintf("%q changed while this replacement was in flight. %s", name, replaceRaced))
			return
		}
		writeConfigRefusal(w, r, err, name, configReplaceFailed)
		return
	}
	writeJSON(w, http.StatusOK, newProviderConfigSummary(replaced))
}

func (s *Server) providerConfigDelete(w http.ResponseWriter, r *http.Request) {
	writer, held := requestClient(w, r, s.writers.configs)
	if !held {
		return
	}
	name := r.PathValue("name")
	if refusedAsAnInvalidName(w, providerConfigKind, name) {
		return
	}
	bound, read := s.leasesBoundTo(w, r, name)
	if !read {
		return
	}
	// the finalizer in the controller is what holds a bound config back whatever the client, and this answers for the interface rather than describing a deletion that will not happen
	if len(bound) > 0 {
		writeAPIError(w, http.StatusConflict,
			fmt.Sprintf("%q is still named by %s. %s", name, strings.Join(bound, ", "), deleteRefused))
		return
	}

	if err := writer.Delete(r.Context(), name); err != nil {
		writeConfigRefusal(w, r, err, name, configDeleteFailed)
		return
	}
	writeJSON(w, http.StatusAccepted, providerConfigDeleteResponse{Name: name, Detail: deleteRequested})
}

func (s *Server) leasesBoundTo(w http.ResponseWriter, r *http.Request, config string) ([]string, bool) {
	reader, held := requestClient(w, r, s.readers)
	if !held {
		return nil, false
	}

	var leases v1alpha1.CapacityLeaseList
	if err := reader.List(r.Context(), &leases); err != nil {
		if refusedByAuthorisation(w, r, err) {
			return nil, false
		}
		slog.Error("list the capacity leases bound to the provider config", "config", config, "error", err)
		writeAPIError(w, http.StatusBadGateway, leaseReadFailed)
		return nil, false
	}
	return leases.NamesBoundTo(config), true
}
