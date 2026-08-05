package hetzner

import (
	"context"
	"strings"
	"testing"
	"time"

	hcloudgo "github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeImages struct {
	images   []*hcloudgo.Image
	lastOpts hcloudgo.ImageListOpts
	err      error
}

func (f *fakeImages) AllWithOpts(_ context.Context, opts hcloudgo.ImageListOpts) ([]*hcloudgo.Image, error) {
	f.lastOpts = opts
	return f.images, f.err
}

type fakeServerTypes struct {
	arch hcloudgo.Architecture
	err  error
}

func (f *fakeServerTypes) GetByName(_ context.Context, _ string) (*hcloudgo.ServerType, *hcloudgo.Response, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return &hcloudgo.ServerType{Architecture: f.arch}, nil, nil
}

func TestResolveImage(t *testing.T) {
	arch := hcloudgo.ArchitectureX86
	img := func(id int64, created time.Time) *hcloudgo.Image {
		return &hcloudgo.Image{ID: id, Architecture: arch, Created: created}
	}
	older, newer := time.Now().Add(-time.Hour), time.Now()

	cases := []struct {
		name    string
		ref     ImageRef
		images  []*hcloudgo.Image
		wantID  int64
		wantErr string
	}{
		{name: "id short circuits", ref: ImageRef{ID: 42}, wantID: 42},
		{name: "name resolves one", ref: ImageRef{Name: "ubuntu-24.04"}, images: []*hcloudgo.Image{img(7, newer)}, wantID: 7},
		{name: "name with no match", ref: ImageRef{Name: "ubuntu-24.04"}, wantErr: "no image named"},
		{name: "name ambiguous", ref: ImageRef{Name: "ubuntu-24.04"}, images: []*hcloudgo.Image{img(7, newer), img(8, older)}, wantErr: "matches 2 images"},
		{name: "selector newest wins", ref: ImageRef{Selector: map[string]string{"k": "v"}}, images: []*hcloudgo.Image{img(8, older), img(9, newer)}, wantID: 9},
		{name: "selector ties on created time break on the higher id", ref: ImageRef{Selector: map[string]string{"k": "v"}}, images: []*hcloudgo.Image{img(5, newer), img(9, newer), img(7, newer)}, wantID: 9},
		{name: "selector with no match", ref: ImageRef{Selector: map[string]string{"k": "v"}}, wantErr: "no image matches label"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClientWithAPIs(nil, &fakeImages{images: tc.images}, nil, nil, &fakeServerTypes{arch: arch}, ServerSpec{})
			got, err := c.resolveImage(context.Background(), tc.ref, "cx23")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got.ID != tc.wantID {
				t.Fatalf("want image %d, got %d", tc.wantID, got.ID)
			}
		})
	}
}

func TestResolveImageFiltersByArchitecture(t *testing.T) {
	c := NewClientWithAPIs(nil, &fakeImages{}, nil, nil, &fakeServerTypes{arch: hcloudgo.ArchitectureARM}, ServerSpec{})
	if _, err := c.resolveImage(context.Background(), ImageRef{Name: "ubuntu-24.04"}, "cax11"); err == nil {
		t.Fatal("expected an error")
	}
	if got := c.images.(*fakeImages).lastOpts.Architecture; len(got) != 1 || got[0] != hcloudgo.ArchitectureARM {
		t.Fatalf("architecture was not passed to the lookup, got %v", got)
	}
}

func TestLabelSelectorExprSortsKeys(t *testing.T) {
	got := labelSelectorExpr(map[string]string{"zeta": "1", "alpha": "2", "mid": "3"})
	want := "alpha=2,mid=3,zeta=1"
	if got != want {
		t.Fatalf("selector expr is %q, want %q", got, want)
	}
}
