package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchManifestUsesBoundedHTTPS(t *testing.T) {
	payload := []byte(`{"schemaVersion":1}`)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	got, err := FetchManifest(context.Background(), server.Client(), server.URL+"/manifest.json")
	if err != nil || string(got) != string(payload) {
		t.Fatalf("manifest=%q err=%v", got, err)
	}
	if _, err := FetchManifest(context.Background(), server.Client(), "http://example.invalid/manifest.json"); err == nil {
		t.Fatal("HTTP manifest URL accepted")
	}
}

func TestFetchManifestRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Del("Content-Length")
		_, _ = response.Write([]byte(strings.Repeat("x", maxManifestBytes+1)))
	}))
	defer server.Close()
	if _, err := FetchManifest(context.Background(), server.Client(), server.URL); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversized manifest was not rejected: %v", err)
	}
}
