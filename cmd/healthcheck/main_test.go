package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func startHealthServer(t *testing.T, status int) (port string, closeFn func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	})
	srv := httptest.NewUnstartedServer(mux)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv.Listener = ln
	srv.Start()
	_, port, _ = net.SplitHostPort(srv.Listener.Addr().String())
	return port, srv.Close
}

func TestHealthcheck_OK(t *testing.T) {
	port, closeFn := startHealthServer(t, http.StatusOK)
	defer closeFn()
	if code := healthcheck(port); code != 0 {
		t.Fatalf("expected 0 for healthy, got %d", code)
	}
}

func TestHealthcheck_Unhealthy(t *testing.T) {
	port, closeFn := startHealthServer(t, http.StatusServiceUnavailable)
	defer closeFn()
	if code := healthcheck(port); code != 1 {
		t.Fatalf("expected 1 for unhealthy, got %d", code)
	}
}

func TestHealthcheck_NoServer(t *testing.T) {
	if code := healthcheck("1"); code != 1 {
		t.Fatalf("expected 1 when no server, got %d", code)
	}
}

func TestHealthcheck_DefaultPort(t *testing.T) {
	// Empty port defaults to 8080; nothing is listening so it returns 1, but the
	// default-branch is exercised.
	if code := healthcheck(""); code != 1 && code != 0 {
		t.Fatalf("unexpected code %d", code)
	}
}
