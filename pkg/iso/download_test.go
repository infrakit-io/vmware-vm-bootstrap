package iso

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
)

func TestDownloadFile_OKAndHTTPError(t *testing.T) {
	mgr := NewManager(context.Background())

	data := []byte("hello")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.iso")
	if err := mgr.downloadFile(srv.URL, dest); err != nil {
		t.Fatalf("downloadFile: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("content mismatch")
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", hits)
	}

	// HTTP error
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvErr.Close()

	dest2 := filepath.Join(dir, "file2.iso")
	if err := mgr.downloadFile(srvErr.URL, dest2); err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestDownloadUbuntu_UsesCacheAndRedownloadsOnCorruption(t *testing.T) {
	mgr := NewManager(context.Background())
	if err := mgr.SetCacheDir(t.TempDir()); err != nil {
		t.Fatalf("SetCacheDir: %v", err)
	}

	payload := []byte("iso-content")
	sum := sha256.Sum256(payload)
	checksum := hex.EncodeToString(sum[:])

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	old := configs.UbuntuReleases.Releases
	configs.UbuntuReleases.Releases = map[string]configs.UbuntuRelease{
		"99.99": {URL: srv.URL + "/ubuntu.iso", Checksum: checksum},
	}
	defer func() { configs.UbuntuReleases.Releases = old }()

	// First download
	path, err := mgr.DownloadUbuntu("99.99")
	if err != nil {
		t.Fatalf("DownloadUbuntu: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected 1 download, got %d", hits)
	}

	// Cached (no re-download)
	_, err = mgr.DownloadUbuntu("99.99")
	if err != nil {
		t.Fatalf("DownloadUbuntu cached: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected cache hit, got %d", hits)
	}

	// Corrupt cache and re-download
	if err := os.WriteFile(path, []byte("corrupt"), 0644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	_, err = mgr.DownloadUbuntu("99.99")
	if err != nil {
		t.Fatalf("DownloadUbuntu redownload: %v", err)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("expected re-download, got %d", hits)
	}
}
