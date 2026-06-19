package hcloud_test

import (
	"context"
	"strings"
	"testing"
	"time"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
	hz "github.com/lucawalz/horizon/internal/hcloud"
)

type fakeAPI struct {
	servers   []*hcloudgo.Server
	images    []*hcloudgo.Image
	created   []hcloudgo.ServerCreateOpts
	deleted   []int64
	nextID    int64
	listErr   error
	createErr error
	deleteErr error
	imageErr  error
}

func (f *fakeAPI) AllWithOpts(_ context.Context, opts hcloudgo.ServerListOpts) ([]*hcloudgo.Server, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return filterByLabelSelector(f.servers, opts.LabelSelector), nil
}

func (f *fakeAPI) Create(_ context.Context, opts hcloudgo.ServerCreateOpts) (hcloudgo.ServerCreateResult, *hcloudgo.Response, error) {
	if f.createErr != nil {
		return hcloudgo.ServerCreateResult{}, nil, f.createErr
	}
	f.created = append(f.created, opts)
	f.nextID++
	srv := &hcloudgo.Server{ID: f.nextID, Name: opts.Name, Labels: opts.Labels}
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
	return f.images, nil
}

type fakeImageAPI struct{ f *fakeAPI }

func (i fakeImageAPI) AllWithOpts(ctx context.Context, opts hcloudgo.ImageListOpts) ([]*hcloudgo.Image, error) {
	return i.f.imageList(ctx, opts)
}

func filterByLabelSelector(servers []*hcloudgo.Server, selector string) []*hcloudgo.Server {
	if selector == "" {
		return servers
	}
	parts := strings.SplitN(selector, "=", 2)
	if len(parts) != 2 {
		return servers
	}
	key, val := parts[0], parts[1]
	out := []*hcloudgo.Server{}
	for _, s := range servers {
		if s.Labels[key] == val {
			out = append(out, s)
		}
	}
	return out
}

func newFake(images []*hcloudgo.Image, servers ...*hcloudgo.Server) (*hz.Client, *fakeAPI) {
	f := &fakeAPI{servers: servers, images: images}
	return hz.NewClientWithAPIs(f, fakeImageAPI{f}), f
}

func server(id int64, name string, labels map[string]string) *hcloudgo.Server {
	return &hcloudgo.Server{ID: id, Name: name, Labels: labels}
}

func horizonLabels() map[string]string {
	return map[string]string{
		hz.PoolLabelKey:      hz.ReservedPoolValue,
		hz.ManagedByLabelKey: hz.ManagedByValue,
	}
}

func poolImage() []*hcloudgo.Image {
	return []*hcloudgo.Image{{ID: 1, Name: "img-old", Created: time.Unix(100, 0)}}
}

func TestListReservedServersDropsForeignServers(t *testing.T) {
	autoscaler := server(2, "elastic-1", map[string]string{
		hz.PoolLabelKey:      "elastic",
		hz.ManagedByLabelKey: "cluster-autoscaler",
		hz.NodeGroupLabelKey: "elastic",
	})
	mine := server(1, "reserved-abc", horizonLabels())
	c, _ := newFake(nil, mine, autoscaler)

	got, err := c.ListReservedServers(context.Background())
	if err != nil {
		t.Fatalf("ListReservedServers: %v", err)
	}
	if len(got) != 1 || got[0].Name != "reserved-abc" {
		t.Fatalf("expected only horizon-owned server, got %+v", got)
	}
}

func TestListReservedServersDefensivelyDropsNodeGroupEvenIfManagedByHorizon(t *testing.T) {
	poisoned := server(3, "reserved-poison", map[string]string{
		hz.ManagedByLabelKey: hz.ManagedByValue,
		hz.NodeGroupLabelKey: "elastic",
	})
	c, _ := newFake(nil, poisoned)

	got, err := c.ListReservedServers(context.Background())
	if err != nil {
		t.Fatalf("ListReservedServers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a node-group-labelled server must never be listed, got %+v", got)
	}
}

func TestScaleReservedToRefusesToDeleteForeignServer(t *testing.T) {
	mine := server(1, "reserved-abc", horizonLabels())
	c, f := newFake(poolImage(), mine)

	if _, err := c.ScaleReservedTo(context.Background(), hz.ServerSpec{Location: "hel1", ServerType: "cpx22", UserData: "x"}, 0); err != nil {
		t.Fatalf("ScaleReservedTo: %v", err)
	}
	if len(f.deleted) != 1 || f.deleted[0] != 1 {
		t.Fatalf("expected own server deleted, got %v", f.deleted)
	}
}

func TestScaleReservedToCreatesWithReservedLabels(t *testing.T) {
	c, f := newFake(poolImage())

	if _, err := c.ScaleReservedTo(context.Background(), hz.ServerSpec{Location: "hel1", ServerType: "cpx22", SSHKeys: []string{"k"}, UserData: "ud"}, 2); err != nil {
		t.Fatalf("ScaleReservedTo: %v", err)
	}
	if len(f.created) != 2 {
		t.Fatalf("expected 2 creates, got %d", len(f.created))
	}
	for _, opts := range f.created {
		if opts.Labels[hz.ManagedByLabelKey] != hz.ManagedByValue {
			t.Errorf("missing managed-by label: %v", opts.Labels)
		}
		if opts.Labels[hz.PoolLabelKey] != hz.ReservedPoolValue {
			t.Errorf("missing reserved pool label: %v", opts.Labels)
		}
		if !strings.HasPrefix(opts.Name, hz.ReservedPoolValue+"-") {
			t.Errorf("server name %q should be reserved-prefixed", opts.Name)
		}
		if opts.UserData != "ud" {
			t.Errorf("user-data = %q, want ud", opts.UserData)
		}
	}
}

func TestCreateReservedServerPicksNewestImage(t *testing.T) {
	images := []*hcloudgo.Image{
		{ID: 1, Name: "old", Created: time.Unix(100, 0)},
		{ID: 2, Name: "new", Created: time.Unix(200, 0)},
	}
	c, f := newFake(images)

	if _, err := c.CreateReservedServer(context.Background(), hz.ServerSpec{Location: "hel1", ServerType: "cpx22", UserData: "ud"}); err != nil {
		t.Fatalf("CreateReservedServer: %v", err)
	}
	if f.created[0].Image.ID != 2 {
		t.Errorf("image = %d, want newest (2)", f.created[0].Image.ID)
	}
}

func TestCreateReservedServerFailsFastWithoutImage(t *testing.T) {
	c, _ := newFake(nil)
	_, err := c.CreateReservedServer(context.Background(), hz.ServerSpec{Location: "hel1", ServerType: "cpx22", UserData: "ud"})
	if err == nil || !strings.Contains(err.Error(), "no image") {
		t.Fatalf("expected no-image error, got %v", err)
	}
}
