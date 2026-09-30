package iso

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
)

// checksumFixture serves `served` over HTTP and registers release "99.99"
// pointing at it with `checksum`. It returns a manager with a private cache
// dir, the path the ISO would be cached at, and the server hit counter.
func checksumFixture(t *testing.T, served []byte, status int, checksum string) (*Manager, string, *int32) {
	t.Helper()
	mgr := NewManager(context.Background())
	cache := t.TempDir()
	if err := mgr.SetCacheDir(cache); err != nil {
		t.Fatalf("SetCacheDir: %v", err)
	}

	hits := new(int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write(served)
		}
	}))
	t.Cleanup(srv.Close)

	old := configs.UbuntuReleases.Releases
	configs.UbuntuReleases.Releases = map[string]configs.UbuntuRelease{
		"99.99": {URL: srv.URL + "/ubuntu-99.99-live-server-amd64.iso", Checksum: checksum},
	}
	t.Cleanup(func() { configs.UbuntuReleases.Releases = old })

	return mgr, filepath.Join(cache, "ubuntu-99.99-live-server-amd64.iso"), hits
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestDownloadUbuntu_MatchingChecksumAccepted(t *testing.T) {
	payload := []byte("genuine-iso")
	// Upper-case digest must be accepted too (normalised before comparing).
	mgr, cached, hits := checksumFixture(t, payload, http.StatusOK, strings.ToUpper(sha256Hex(payload)))

	path, err := mgr.DownloadUbuntu("99.99")
	if err != nil {
		t.Fatalf("DownloadUbuntu with matching checksum: %v", err)
	}
	if path != cached {
		t.Fatalf("path = %q, want %q", path, cached)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("cached content = %q (err %v), want %q", got, err, payload)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("expected 1 download, got %d", n)
	}
}

func TestDownloadUbuntu_DownloadMismatchRefused(t *testing.T) {
	mgr, cached, _ := checksumFixture(t, []byte("tampered-iso"), http.StatusOK, sha256Hex([]byte("genuine-iso")))

	path, err := mgr.DownloadUbuntu("99.99")
	if err == nil {
		t.Fatalf("expected refusal on checksum mismatch, got path %q", path)
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error should name the checksum mismatch, got: %v", err)
	}
	if path != "" {
		t.Errorf("no path may be returned on refusal, got %q", path)
	}
	if _, statErr := os.Stat(cached); !os.IsNotExist(statErr) {
		t.Errorf("mismatched download must be deleted from the cache (stat err: %v)", statErr)
	}
}

// A cached ISO that does not match is never used: it is discarded and the
// fresh download is verified in turn. If the fresh download is bad as well,
// the call refuses rather than falling back to either file.
func TestDownloadUbuntu_CachedMismatchNeverUsed(t *testing.T) {
	genuine := []byte("genuine-iso")

	t.Run("redownload also bad: refuse", func(t *testing.T) {
		mgr, cached, hits := checksumFixture(t, []byte("still-tampered"), http.StatusOK, sha256Hex(genuine))
		if err := os.WriteFile(cached, []byte("tampered-cache"), 0644); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		path, err := mgr.DownloadUbuntu("99.99")
		if err == nil {
			t.Fatalf("expected refusal, got path %q", path)
		}
		if n := atomic.LoadInt32(hits); n != 1 {
			t.Fatalf("expected a re-download attempt, got %d hits", n)
		}
		if _, statErr := os.Stat(cached); !os.IsNotExist(statErr) {
			t.Errorf("bad ISO must not remain in the cache (stat err: %v)", statErr)
		}
	})

	t.Run("redownload unavailable: refuse, never fall back to cache", func(t *testing.T) {
		mgr, cached, _ := checksumFixture(t, nil, http.StatusInternalServerError, sha256Hex(genuine))
		if err := os.WriteFile(cached, []byte("tampered-cache"), 0644); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		path, err := mgr.DownloadUbuntu("99.99")
		if err == nil {
			t.Fatalf("expected refusal, got path %q", path)
		}
		if _, statErr := os.Stat(cached); !os.IsNotExist(statErr) {
			t.Errorf("mismatched cached ISO must be discarded (stat err: %v)", statErr)
		}
	})

	t.Run("redownload good: verified fresh copy used", func(t *testing.T) {
		mgr, cached, hits := checksumFixture(t, genuine, http.StatusOK, sha256Hex(genuine))
		if err := os.WriteFile(cached, []byte("tampered-cache"), 0644); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		path, err := mgr.DownloadUbuntu("99.99")
		if err != nil {
			t.Fatalf("DownloadUbuntu: %v", err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != string(genuine) {
			t.Fatalf("returned ISO content = %q, want the genuine download", got)
		}
		if n := atomic.LoadInt32(hits); n != 1 {
			t.Fatalf("expected 1 re-download, got %d", n)
		}
	})
}

// A release without a checksum is refused before any download, and a file
// already sitting in the cache is not handed out unverified either.
func TestDownloadUbuntu_EmptyChecksumRefused(t *testing.T) {
	for _, cs := range []string{"", "   "} {
		mgr, cached, hits := checksumFixture(t, []byte("anything"), http.StatusOK, cs)
		if err := os.WriteFile(cached, []byte("anything"), 0644); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		path, err := mgr.DownloadUbuntu("99.99")
		if err == nil {
			t.Fatalf("checksum %q: expected refusal, got path %q", cs, path)
		}
		if !strings.Contains(err.Error(), "no SHA256 checksum") {
			t.Errorf("checksum %q: error should explain the missing checksum, got: %v", cs, err)
		}
		if n := atomic.LoadInt32(hits); n != 0 {
			t.Errorf("checksum %q: no download may be attempted, got %d hits", cs, n)
		}
	}
}

func TestDownloadUbuntu_MalformedChecksumRefused(t *testing.T) {
	for _, cs := range []string{"deadbeef", strings.Repeat("g", 64), sha256Hex([]byte("x")) + "0"} {
		mgr, _, hits := checksumFixture(t, []byte("x"), http.StatusOK, cs)
		if path, err := mgr.DownloadUbuntu("99.99"); err == nil {
			t.Fatalf("checksum %q: expected refusal, got path %q", cs, path)
		}
		if n := atomic.LoadInt32(hits); n != 0 {
			t.Errorf("checksum %q: no download may be attempted, got %d hits", cs, n)
		}
	}
}

// The shipped release table must be usable under the mandatory-checksum rule.
func TestShippedReleasesPassChecksumValidation(t *testing.T) {
	for version, r := range GetUbuntuReleases() {
		if _, err := normalizeChecksum(version, r.Checksum); err != nil {
			t.Errorf("shipped release %s: %v", version, err)
		}
	}
}
