package hetzner

import (
	"context"
	"testing"

	"github.com/lucawalz/horizon/internal/provider"
)

func TestNewTokenClientRejectsAnEmptyToken(t *testing.T) {
	if _, err := NewTokenClient(""); err == nil {
		t.Fatal("NewTokenClient must reject an empty token")
	}
}

func TestNewTokenClientNeedsNoCloudInit(t *testing.T) {
	client, err := NewTokenClient("token")
	if err != nil {
		t.Fatalf("NewTokenClient: %v", err)
	}
	if client == nil {
		t.Fatal("NewTokenClient returned no provider")
	}
}

func TestNewClientStillRejectsCloudInitWithoutThePoolLabel(t *testing.T) {
	if _, err := NewClient("token", ServerSpec{UserData: "#cloud-config"}); err == nil {
		t.Fatal("NewClient must reject cloud-init that does not apply the pool label")
	}
}

func TestANodeScopedClientCannotProvision(t *testing.T) {
	client, api := newFake(ServerSpec{}, nil)

	req := provider.CreateRequest{Name: "burst-0", Region: "nbg1", Size: "cx22"}
	if _, err := client.Create(context.Background(), req); err == nil {
		t.Fatal("a client built without a server spec must refuse to create an instance")
	}
	if len(api.created) != 0 {
		t.Errorf("create calls = %d, want none", len(api.created))
	}
}
