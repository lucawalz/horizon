package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

const dialTimeout = 500 * time.Millisecond

func TestLoopbackAddressBindsALoopbackAddress(t *testing.T) {
	listener, err := LoopbackAddress(freePort(t)).listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T, want a tcp address", listener.Addr())
	}
	if !address.IP.IsLoopback() {
		t.Errorf("bound to %s, want a loopback address", address.IP)
	}
}

func TestBindAddressRejectsAnAddressItCannotName(t *testing.T) {
	for name, bind := range map[string]BindAddress{
		"an unset port":         LoopbackAddress(0),
		"an empty address":      ExplicitAddress(""),
		"an unconstructed bind": {},
	} {
		t.Run(name, func(t *testing.T) {
			listener, err := bind.listen()
			if err == nil {
				_ = listener.Close()
				t.Errorf("%s bound a listener, want a rejection", name)
			}
		})
	}
}

func TestExplicitAddressBindsTheAddressItIsGiven(t *testing.T) {
	wanted := net.JoinHostPort(loopbackHost, strconv.Itoa(int(freePort(t))))

	listener, err := ExplicitAddress(wanted).listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	if listener.Addr().String() != wanted {
		t.Errorf("bound to %s, want %s", listener.Addr(), wanted)
	}
}

func TestLoopbackAddressRefusesRoutableAddresses(t *testing.T) {
	routable := routableAddresses(t)
	if len(routable) == 0 {
		t.Skip("the host carries no routable address to attempt the connection from")
	}

	listener, err := LoopbackAddress(freePort(t)).listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	for _, ip := range routable {
		target := net.JoinHostPort(ip, port)
		conn, err := net.DialTimeout("tcp", target, dialTimeout)
		if err == nil {
			_ = conn.Close()
			t.Errorf("connected on %s, want the loopback binding to refuse it", target)
		}
	}
}

func routableAddresses(t *testing.T) []string {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatalf("read the host addresses: %v", err)
	}

	var routable []string
	for _, address := range addresses {
		network, ok := address.(*net.IPNet)
		if !ok {
			continue
		}
		ip := network.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		routable = append(routable, ip.String())
	}
	return routable
}

func TestListenAndServeRejectsAnUnusableBindAddress(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	if err := server.ListenAndServe(t.Context(), LoopbackAddress(0)); err == nil {
		t.Error("serving on port 0 succeeded, want a rejection")
	}
}

func TestListenAndServeAnswersOnLoopbackAndStops(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	port := freePort(t)
	server := newTestServer(t, testEnv.Client, AbsentCatalogue())
	ctx, cancel := context.WithCancel(t.Context())

	served := make(chan error, 1)
	go func() { served <- server.ListenAndServe(ctx, LoopbackAddress(port)) }()

	response := poll(t, fmt.Sprintf("http://127.0.0.1:%d/", port))
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(shutdownGrace * 2):
		t.Error("the interface did not stop once the context was cancelled")
	}
}

// the helper is pinned above, so this pins that the listener the interface actually serves is the one it builds
func TestListenAndServeRefusesRoutableAddresses(t *testing.T) {
	routable := routableAddresses(t)
	if len(routable) == 0 {
		t.Skip("the host carries no routable address to attempt the connection from")
	}

	port := freePort(t)
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	served := make(chan error, 1)
	go func() { served <- server.ListenAndServe(ctx, LoopbackAddress(port)) }()

	response := poll(t, fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err := response.Body.Close(); err != nil {
		t.Errorf("close the response body: %v", err)
	}

	for _, ip := range routable {
		target := net.JoinHostPort(ip, strconv.Itoa(int(port)))
		conn, err := net.DialTimeout("tcp", target, dialTimeout)
		if err == nil {
			_ = conn.Close()
			t.Errorf("the served interface answered on %s, want the loopback binding to refuse it", target)
		}
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(shutdownGrace * 2):
		t.Error("the interface did not stop once the context was cancelled")
	}
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(loopbackHost, "0"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close the listener: %v", err)
	}
	return uint16(port)
}

func poll(t *testing.T, url string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := http.Get(url)
		if err == nil {
			return response
		}
		if time.Now().After(deadline) {
			t.Fatalf("fetch %s: %v", url, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNewRefusesAnIncompleteConfiguration(t *testing.T) {
	for name, opts := range map[string]Options{
		"no client":    {Catalogue: AbsentCatalogue()},
		"no catalogue": {Client: failingReader{err: errors.New("unused")}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(opts); err == nil {
				t.Error("the server was built, want a rejection")
			}
		})
	}
}
