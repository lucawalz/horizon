package hetzner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/lucawalz/horizon/internal/provider"
)

type fakeAPI struct {
	servers       []*hcloudgo.Server
	images        []*hcloudgo.Image
	sshKeys       map[string]*hcloudgo.SSHKey
	firewalls     map[string]*hcloudgo.Firewall
	created       []hcloudgo.ServerCreateOpts
	deleted       []int64
	nextID        int64
	imageSelector string
	listErr       error
	createErr     error
	deleteErr     error
	imageErr      error
	sshKeyErr     error
	firewallErr   error
}

func (f *fakeAPI) AllWithOpts(_ context.Context, opts hcloudgo.ServerListOpts) ([]*hcloudgo.Server, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return filterByName(filterByLabelSelector(f.servers, opts.LabelSelector), opts.Name), nil
}

func (f *fakeAPI) Create(_ context.Context, opts hcloudgo.ServerCreateOpts) (hcloudgo.ServerCreateResult, *hcloudgo.Response, error) {
	if f.createErr != nil {
		return hcloudgo.ServerCreateResult{}, nil, f.createErr
	}
	f.created = append(f.created, opts)
	f.nextID++
	srv := &hcloudgo.Server{ID: f.nextID, Name: opts.Name, Labels: opts.Labels, Status: hcloudgo.ServerStatusRunning}
	if opts.Location != nil {
		srv.Location = &hcloudgo.Location{Name: opts.Location.Name}
	}
	f.servers = append(f.servers, srv)
	return hcloudgo.ServerCreateResult{Server: srv}, nil, nil
}

func (f *fakeAPI) Delete(_ context.Context, server *hcloudgo.Server) (*hcloudgo.Response, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deleted = append(f.deleted, server.ID)
	kept := f.servers[:0]
	for _, s := range f.servers {
		if s.ID != server.ID {
			kept = append(kept, s)
		}
	}
	f.servers = kept
	return nil, nil
}

func (f *fakeAPI) imageList(_ context.Context, opts hcloudgo.ImageListOpts) ([]*hcloudgo.Image, error) {
	if f.imageErr != nil {
		return nil, f.imageErr
	}
	f.imageSelector = opts.LabelSelector
	return f.images, nil
}

type fakeImageAPI struct{ f *fakeAPI }

func (i fakeImageAPI) AllWithOpts(ctx context.Context, opts hcloudgo.ImageListOpts) ([]*hcloudgo.Image, error) {
	return i.f.imageList(ctx, opts)
}

type fakeSSHKeyAPI struct{ f *fakeAPI }

func (k fakeSSHKeyAPI) GetByName(_ context.Context, name string) (*hcloudgo.SSHKey, *hcloudgo.Response, error) {
	if k.f.sshKeyErr != nil {
		return nil, nil, k.f.sshKeyErr
	}
	if key, ok := k.f.sshKeys[name]; ok {
		return key, nil, nil
	}
	return nil, nil, nil
}

type fakeFirewallAPI struct{ f *fakeAPI }

func (w fakeFirewallAPI) GetByName(_ context.Context, name string) (*hcloudgo.Firewall, *hcloudgo.Response, error) {
	if w.f.firewallErr != nil {
		return nil, nil, w.f.firewallErr
	}
	if firewall, ok := w.f.firewalls[name]; ok {
		return firewall, nil, nil
	}
	return nil, nil, nil
}

func filterByLabelSelector(servers []*hcloudgo.Server, selector string) []*hcloudgo.Server {
	if selector == "" {
		return servers
	}
	out := []*hcloudgo.Server{}
	for _, s := range servers {
		if matchesSelector(s.Labels, selector) {
			out = append(out, s)
		}
	}
	return out
}

func matchesSelector(labels map[string]string, selector string) bool {
	for _, pair := range strings.Split(selector, ",") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if labels[key] != value {
			return false
		}
	}
	return true
}

func filterByName(servers []*hcloudgo.Server, name string) []*hcloudgo.Server {
	if name == "" {
		return servers
	}
	out := []*hcloudgo.Server{}
	for _, s := range servers {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

func newFake(spec ServerSpec, images []*hcloudgo.Image, servers ...*hcloudgo.Server) (*Client, *fakeAPI) {
	return newFakeWithServerTypes(spec, images, &fakeServerTypes{}, servers...)
}

func newFakeWithServerTypes(spec ServerSpec, images []*hcloudgo.Image, serverTypes *fakeServerTypes, servers ...*hcloudgo.Server) (*Client, *fakeAPI) {
	f := &fakeAPI{
		servers: servers,
		images:  images,
		sshKeys: map[string]*hcloudgo.SSHKey{"k": {ID: 42, Name: "k"}},
		firewalls: map[string]*hcloudgo.Firewall{
			"edge":     {ID: 7, Name: "edge"},
			"internal": {ID: 8, Name: "internal"},
		},
	}
	for _, s := range servers {
		if s.ID > f.nextID {
			f.nextID = s.ID
		}
	}
	return NewClientWithAPIs(f, fakeImageAPI{f}, fakeSSHKeyAPI{f}, fakeFirewallAPI{f}, serverTypes, spec), f
}

func server(id int64, name string, labels map[string]string) *hcloudgo.Server {
	return &hcloudgo.Server{ID: id, Name: name, Labels: labels, Status: hcloudgo.ServerStatusRunning}
}

func horizonLabels() map[string]string {
	return map[string]string{
		provider.PoolLabelKey:      provider.ReservedPoolValue,
		provider.ManagedByLabelKey: provider.ManagedByValue,
	}
}

func poolImage() []*hcloudgo.Image {
	return []*hcloudgo.Image{{ID: 1, Name: "img-old", Created: time.Unix(100, 0)}}
}

func provisionableSpec() ServerSpec {
	return ServerSpec{
		Location:   "hel1",
		ServerType: "cpx22",
		Image:      ImageRef{Selector: map[string]string{"pool-image": "reserved-pool"}},
		UserData:   "ud",
	}
}

func reservedRequest(name string) provider.CreateRequest {
	return provider.CreateRequest{Name: name, Labels: horizonLabels()}
}

func TestCapabilitiesReportBillingUntilDeletion(t *testing.T) {
	c, _ := newFake(provisionableSpec(), poolImage())

	caps := c.Capabilities()
	if caps.SelfTerminationStopsBilling {
		t.Error("hetzner bills until the server object is deleted")
	}
	if !caps.SupportsResourceLabels {
		t.Error("hetzner servers carry labels")
	}
	if len(caps.Regions) == 0 {
		t.Error("capabilities must name the available regions")
	}
}

func TestListDropsForeignServers(t *testing.T) {
	autoscaler := server(2, "elastic-1", map[string]string{
		provider.PoolLabelKey:      "elastic",
		provider.ManagedByLabelKey: "cluster-autoscaler",
		NodeGroupLabelKey:          "elastic",
	})
	mine := server(1, "reserved-abc", horizonLabels())
	c, _ := newFake(ServerSpec{}, nil, mine, autoscaler)

	got, err := c.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "reserved-abc" {
		t.Fatalf("expected only horizon-owned instances, got %+v", got)
	}
}

func TestListDefensivelyDropsNodeGroupEvenIfManagedByHorizon(t *testing.T) {
	poisoned := server(3, "reserved-poison", map[string]string{
		provider.ManagedByLabelKey: provider.ManagedByValue,
		NodeGroupLabelKey:          "elastic",
	})
	c, _ := newFake(ServerSpec{}, nil, poisoned)

	got, err := c.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a node-group-labelled server must never be listed, got %+v", got)
	}
}

func TestListNarrowsToTheRequestedSelector(t *testing.T) {
	reserved := server(1, "reserved-abc", horizonLabels())
	other := server(2, "other-abc", map[string]string{
		provider.PoolLabelKey:      "other",
		provider.ManagedByLabelKey: provider.ManagedByValue,
	})
	c, _ := newFake(ServerSpec{}, nil, reserved, other)

	got, err := c.List(context.Background(), map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "reserved-abc" {
		t.Fatalf("selector must narrow the result, got %+v", got)
	}
}

func TestGetReportsNotFoundForAForeignName(t *testing.T) {
	c, _ := newFake(ServerSpec{}, nil)

	if _, err := c.Get(context.Background(), "absent"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func TestGetSurfacesForeignServersSoDeleteCanRefuseThem(t *testing.T) {
	autoscaler := server(2, "elastic-1", map[string]string{NodeGroupLabelKey: "elastic"})
	c, _ := newFake(ServerSpec{}, nil, autoscaler)

	got, err := c.Get(context.Background(), "elastic-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ProviderID != "hcloud://2" {
		t.Errorf("provider id = %q, want hcloud://2", got.ProviderID)
	}
}

func TestDeleteRefusesForeignServer(t *testing.T) {
	autoscaler := server(2, "elastic-1", map[string]string{NodeGroupLabelKey: "elastic"})
	c, f := newFake(ServerSpec{}, nil, autoscaler)

	err := c.Delete(context.Background(), "elastic-1")
	if err == nil || !strings.Contains(err.Error(), "refusing to delete") {
		t.Fatalf("Delete = %v, want a refusal", err)
	}
	if len(f.deleted) != 0 {
		t.Fatalf("a refused delete must not reach the API, got %v", f.deleted)
	}
}

func TestDeleteRefusesNodeGroupServerEvenWhenManagedByHorizon(t *testing.T) {
	poisoned := server(3, "reserved-poison", map[string]string{
		provider.ManagedByLabelKey: provider.ManagedByValue,
		NodeGroupLabelKey:          "elastic",
	})
	c, f := newFake(ServerSpec{}, nil, poisoned)

	if err := c.Delete(context.Background(), "reserved-poison"); err == nil {
		t.Fatal("a node-group-labelled server must never be deleted")
	}
	if len(f.deleted) != 0 {
		t.Fatalf("a refused delete must not reach the API, got %v", f.deleted)
	}
}

func TestDeleteRemovesOwnServer(t *testing.T) {
	mine := server(1, "reserved-abc", horizonLabels())
	c, f := newFake(provisionableSpec(), poolImage(), mine)

	if err := c.Delete(context.Background(), "reserved-abc"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(f.deleted) != 1 || f.deleted[0] != 1 {
		t.Fatalf("expected own server deleted, got %v", f.deleted)
	}
}

func TestCreateAppliesLabelsInTheCreateCall(t *testing.T) {
	spec := provisionableSpec()
	spec.SSHKeys = []string{"k"}
	c, f := newFake(spec, poolImage())

	if _, err := c.Create(context.Background(), reservedRequest("reserved-abc")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(f.created) != 1 {
		t.Fatalf("expected 1 create, got %d", len(f.created))
	}
	opts := f.created[0]
	if opts.Labels[provider.ManagedByLabelKey] != provider.ManagedByValue {
		t.Errorf("missing managed-by label: %v", opts.Labels)
	}
	if opts.Labels[provider.PoolLabelKey] != provider.ReservedPoolValue {
		t.Errorf("missing reserved pool label: %v", opts.Labels)
	}
	if opts.Name != "reserved-abc" {
		t.Errorf("name = %q, want the requested reserved-abc", opts.Name)
	}
	if opts.UserData != "ud" {
		t.Errorf("user-data = %q, want ud", opts.UserData)
	}
}

func TestCreateStampsTheManagementLabelOnAnUnlabelledRequest(t *testing.T) {
	c, f := newFake(provisionableSpec(), poolImage())

	if _, err := c.Create(context.Background(), provider.CreateRequest{Name: "reserved-abc"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if f.created[0].Labels[provider.ManagedByLabelKey] != provider.ManagedByValue {
		t.Errorf("labels = %v, want the management label", f.created[0].Labels)
	}
}

func TestCreatePrefersRequestRegionAndSizeOverSpecDefaults(t *testing.T) {
	c, f := newFake(provisionableSpec(), poolImage())

	req := reservedRequest("reserved-abc")
	req.Region = "fsn1"
	req.Size = "cax41"
	if _, err := c.Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if f.created[0].Location.Name != "fsn1" || f.created[0].ServerType.Name != "cax41" {
		t.Errorf("create opts = %+v, want the requested region and size", f.created[0])
	}
}

func TestCreateIsIdempotentAndDoesNotCallTheAPITwice(t *testing.T) {
	c, f := newFake(provisionableSpec(), poolImage())

	first, err := c.Create(context.Background(), reservedRequest("reserved-abc"))
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := c.Create(context.Background(), reservedRequest("reserved-abc"))
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if len(f.created) != 1 {
		t.Fatalf("expected a single API create, got %d", len(f.created))
	}
	if second.ProviderID != first.ProviderID {
		t.Errorf("second create returned %q, want the existing %q", second.ProviderID, first.ProviderID)
	}
}

func TestCreateRefusesANodeGroupLabelItCouldNeverDelete(t *testing.T) {
	c, _ := newFake(provisionableSpec(), poolImage())

	req := reservedRequest("reserved-abc")
	req.Labels[NodeGroupLabelKey] = "elastic"
	if _, err := c.Create(context.Background(), req); err == nil {
		t.Fatal("expected a refusal for a node-group-labelled request")
	}
}

func TestCreatePicksNewestImage(t *testing.T) {
	images := []*hcloudgo.Image{
		{ID: 1, Name: "old", Created: time.Unix(100, 0)},
		{ID: 2, Name: "new", Created: time.Unix(200, 0)},
	}
	c, f := newFake(provisionableSpec(), images)

	if _, err := c.Create(context.Background(), reservedRequest("reserved-abc")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if f.created[0].Image.ID != 2 {
		t.Errorf("image = %d, want newest (2)", f.created[0].Image.ID)
	}
}

func TestCreateFailsFastWithoutImage(t *testing.T) {
	c, _ := newFake(provisionableSpec(), nil)

	_, err := c.Create(context.Background(), reservedRequest("reserved-abc"))
	if err == nil || !strings.Contains(err.Error(), "no image") {
		t.Fatalf("expected no-image error, got %v", err)
	}
}

func TestCreateFailsFastOnEmptyImage(t *testing.T) {
	spec := provisionableSpec()
	spec.Image = ImageRef{}
	c, _ := newFake(spec, poolImage())

	_, err := c.Create(context.Background(), reservedRequest("reserved-abc"))
	if err == nil || !strings.Contains(err.Error(), "spec.hetzner.image is required") {
		t.Fatalf("expected image required error, got %v", err)
	}
}

func TestCreateFailsFastOnEmptyName(t *testing.T) {
	c, _ := newFake(provisionableSpec(), poolImage())

	if _, err := c.Create(context.Background(), provider.CreateRequest{}); err == nil {
		t.Fatal("expected an error for an unnamed create request")
	}
}

func TestCreateUsesConfiguredImageSelector(t *testing.T) {
	spec := provisionableSpec()
	spec.Image = ImageRef{Selector: map[string]string{"custom-label": "custom-node"}}
	c, f := newFake(spec, poolImage())

	if _, err := c.Create(context.Background(), reservedRequest("reserved-abc")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if f.imageSelector != "custom-label=custom-node" {
		t.Errorf("image selector = %q, want custom-label=custom-node", f.imageSelector)
	}
}

func TestCreateResolvesSSHKeyToID(t *testing.T) {
	spec := provisionableSpec()
	spec.SSHKeys = []string{"k"}
	c, f := newFake(spec, poolImage())

	if _, err := c.Create(context.Background(), reservedRequest("reserved-abc")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(f.created) != 1 || len(f.created[0].SSHKeys) != 1 {
		t.Fatalf("expected one ssh key in create opts, got %+v", f.created)
	}
	if f.created[0].SSHKeys[0].ID != 42 {
		t.Errorf("ssh key ID = %d, want resolved 42", f.created[0].SSHKeys[0].ID)
	}
}

func TestCreateFailsFastOnUnknownSSHKey(t *testing.T) {
	spec := provisionableSpec()
	spec.SSHKeys = []string{"missing"}
	c, _ := newFake(spec, poolImage())

	_, err := c.Create(context.Background(), reservedRequest("reserved-abc"))
	if err == nil || !strings.Contains(err.Error(), `ssh key "missing" not found`) {
		t.Fatalf("expected not-found ssh key error, got %v", err)
	}
}

func TestCreateAttachesEveryConfiguredFirewall(t *testing.T) {
	spec := provisionableSpec()
	spec.Firewalls = []string{"edge", "internal"}
	c, f := newFake(spec, poolImage())

	if _, err := c.Create(context.Background(), reservedRequest("reserved-abc")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(f.created) != 1 {
		t.Fatalf("expected 1 create, got %d", len(f.created))
	}
	got := []int64{}
	for _, attached := range f.created[0].Firewalls {
		got = append(got, attached.Firewall.ID)
	}
	if !slices.Equal(got, []int64{7, 8}) {
		t.Errorf("firewall ids = %v, want [7 8]", got)
	}
}

func TestCreateFailsFastOnUnknownFirewall(t *testing.T) {
	spec := provisionableSpec()
	spec.Firewalls = []string{"edge", "missing"}
	c, f := newFake(spec, poolImage())

	_, err := c.Create(context.Background(), reservedRequest("reserved-abc"))
	if err == nil || !strings.Contains(err.Error(), `firewall "missing" not found`) {
		t.Fatalf("expected not-found firewall error, got %v", err)
	}
	if len(f.created) != 0 {
		t.Fatalf("an unresolvable firewall must not reach the create call, got %d creates", len(f.created))
	}
}

func TestCreateWithoutFirewallsAttachesNone(t *testing.T) {
	c, f := newFake(provisionableSpec(), poolImage())

	if _, err := c.Create(context.Background(), reservedRequest("reserved-abc")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(f.created) != 1 {
		t.Fatalf("expected 1 create, got %d", len(f.created))
	}
	if len(f.created[0].Firewalls) != 0 {
		t.Errorf("firewalls = %+v, want none", f.created[0].Firewalls)
	}
}

func TestToInstancePopulatesSizeFromServerType(t *testing.T) {
	s := &hcloudgo.Server{ID: 1, Name: "reserved-abc", ServerType: &hcloudgo.ServerType{Name: "cpx22"}, Status: hcloudgo.ServerStatusRunning}
	got := toInstance(s)
	if got.Size != "cpx22" {
		t.Errorf("Size = %q, want cpx22", got.Size)
	}
}

func TestToInstanceLeavesSizeEmptyWithoutAServerType(t *testing.T) {
	s := &hcloudgo.Server{ID: 1, Name: "reserved-abc", Status: hcloudgo.ServerStatusRunning}
	got := toInstance(s)
	if got.Size != "" {
		t.Errorf("Size = %q, want empty when the server carries no type", got.Size)
	}
}

func TestInstanceStateMapsHetznerStatus(t *testing.T) {
	cases := map[hcloudgo.ServerStatus]provider.InstanceState{
		hcloudgo.ServerStatusInitializing: provider.Provisioning,
		hcloudgo.ServerStatusStarting:     provider.Provisioning,
		hcloudgo.ServerStatusRunning:      provider.Running,
		hcloudgo.ServerStatusStopping:     provider.Terminating,
		hcloudgo.ServerStatusOff:          provider.Terminating,
		hcloudgo.ServerStatusDeleting:     provider.Terminating,
	}
	for status, want := range cases {
		if got := instanceState(status); got != want {
			t.Errorf("instanceState(%q) = %q, want %q", status, got, want)
		}
	}
}
