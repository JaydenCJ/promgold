// Tests for source resolution: files, stdin, and loopback HTTP scrapes.
// The HTTP tests use net/http/httptest bound to 127.0.0.1 — no external
// network is ever touched.
package fetch

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.txt")
	if err := os.WriteFile(p, []byte("up 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := Read(p, nil, 0)
	if err != nil || string(data) != "up 1\n" {
		t.Fatalf("Read file: %q, %v", data, err)
	}
}

func TestReadMissingFileErrors(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "absent"), nil, 0)
	if err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadStdin(t *testing.T) {
	data, err := Read("-", strings.NewReader("up 0\n"), 0)
	if err != nil || string(data) != "up 0\n" {
		t.Fatalf("Read stdin: %q, %v", data, err)
	}
}

func TestScrapeLoopbackEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); !strings.Contains(got, "text/plain") {
			t.Errorf("Accept header = %q", got)
		}
		w.Write([]byte("# TYPE up gauge\nup 1\n"))
	}))
	defer srv.Close()
	data, err := Read(srv.URL+"/metrics", nil, time.Second)
	if err != nil || !strings.Contains(string(data), "up 1") {
		t.Fatalf("scrape: %q, %v", data, err)
	}
}

func TestScrapeNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, err := Read(srv.URL, nil, time.Second)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v", err)
	}
}

func TestScrapeConnectionRefusedIsError(t *testing.T) {
	// A closed loopback port: deterministic failure without any network.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	_, err := Read(url, nil, time.Second)
	if err == nil || !strings.Contains(err.Error(), "scraping") {
		t.Fatalf("err = %v", err)
	}
}
