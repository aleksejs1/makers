package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheckUsesServePortResolution(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())

	t.Setenv("MAKERS_ENV", "dev")
	// The --port flag is honoured, as in `makers serve --port N`.
	t.Setenv("MAKERS_PORT", "1")
	if err := healthcheck([]string{"--port", port}); err != nil {
		t.Fatalf("flag: %v", err)
	}
	// MAKERS_PORT is used when no flag is given.
	t.Setenv("MAKERS_PORT", port)
	if err := healthcheck(nil); err != nil {
		t.Fatalf("env: %v", err)
	}
	// A server that is not running is reported as unhealthy.
	ts.Close()
	if err := healthcheck(nil); err == nil {
		t.Fatal("expected an error for a stopped server")
	}
}
