package collector

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeAddr(t *testing.T) {
	ok := map[string]string{
		"localhost:6060":     "http://localhost:6060",
		" http://x:1/ ":      "http://x:1",
		"http://x:1//":       "http://x:1",
		"HTTP://host:6060":   "http://host:6060",
		"https://host:6060/": "https://host:6060",
		"http://h:1/prefix/": "http://h:1/prefix",
	}
	for in, want := range ok {
		got, err := NormalizeAddr(in)
		if err != nil || got != want {
			t.Errorf("NormalizeAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "http://", "http:///", "ftp://host:1"} {
		if got, err := NormalizeAddr(bad); err == nil {
			t.Errorf("NormalizeAddr(%q) = %q, want error", bad, got)
		}
	}
}

func TestFetchRemoteAnyEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/debug/pprof/allocs" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "allocs.pb.gz")
	if err := FetchRemote(srv.URL+"/", "allocs", 0, out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "data" {
		t.Fatalf("content %q", b)
	}
}

func TestFetchRemoteRejectsPathTraversal(t *testing.T) {
	for _, typ := range []string{"", "..", "../x", "a/b", "heap?x=1"} {
		if err := FetchRemote("localhost:1", typ, 0, filepath.Join(t.TempDir(), "x")); err == nil {
			t.Errorf("type %q accepted", typ)
		}
	}
}
