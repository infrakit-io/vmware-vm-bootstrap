package iso

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
)

func TestAlmaVaultURL(t *testing.T) {
	cases := map[string]string{
		"https://repo.almalinux.org/almalinux/9.8/isos/x86_64/AlmaLinux-9.8-x86_64-boot.iso": "https://vault.almalinux.org/9.8/isos/x86_64/AlmaLinux-9.8-x86_64-boot.iso",
		"https://repo.almalinux.org/almalinux/9.9/BaseOS/x86_64/os/":                         "https://vault.almalinux.org/9.9/BaseOS/x86_64/os/",
	}
	for in, want := range cases {
		if got, ok := AlmaVaultURL(in); !ok || got != want {
			t.Errorf("%s -> %q %v", in, got, ok)
		}
	}
	for _, in := range []string{
		"https://repo.almalinux.org/almalinux/9/isos/x86_64/x.iso", // moving alias
		"https://example.com/almalinux/9.8/x.iso",
		"http://repo.almalinux.org/almalinux/9.8/x.iso",
	} {
		if got, ok := AlmaVaultURL(in); ok {
			t.Errorf("%s must have no vault equivalent, got %q", in, got)
		}
	}
}

func TestShippedAlmaReleasesArePinnedToVersionedPath(t *testing.T) {
	for v, r := range configs.AlmaReleases.Releases {
		for name, u := range map[string]string{"url": r.URL, "baseos_url": r.BaseOSURL, "appstream_url": r.AppStreamURL} {
			if !strings.Contains(u, "/almalinux/"+v+"/") {
				t.Errorf("%s %s: %s is not under /almalinux/%s/ (moving alias breaks on the next minor)", v, name, u, v)
			}
			if _, ok := AlmaVaultURL(u); !ok {
				t.Errorf("%s %s: no vault equivalent for %s", v, name, u)
			}
		}
	}
	if _, ok := configs.AlmaReleases.Releases["9.9"]; !ok {
		t.Error("9.9 missing")
	}
}

// fakeMirror serves `primary` and `vault` handlers on two httptest servers and
// maps primary URLs onto the vault one.
func fakeMirror(t *testing.T, primary, vault http.HandlerFunc) (pURL, vURL string) {
	t.Helper()
	p := httptest.NewServer(primary)
	v := httptest.NewServer(vault)
	t.Cleanup(p.Close)
	t.Cleanup(v.Close)
	old := vaultURLFn
	vaultURLFn = func(u string) (string, bool) {
		rest, ok := strings.CutPrefix(u, p.URL+"/")
		return v.URL + "/" + rest, ok
	}
	t.Cleanup(func() { vaultURLFn = old })
	return p.URL, v.URL
}

func TestDownloadAlma_FallsBackToVaultOn404AndStaysStrict(t *testing.T) {
	body := []byte("alma-iso-bytes")
	var mu sync.Mutex
	var primaryHits, vaultHits int
	pURL, _ := fakeMirror(t,
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			primaryHits++
			mu.Unlock()
			http.NotFound(w, r)
		},
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			vaultHits++
			mu.Unlock()
			_, _ = w.Write(body)
		})

	m := NewManager(context.Background())
	_ = m.SetCacheDir(t.TempDir())
	withAlmaRelease(t, "9.8", configs.AlmaRelease{URL: pURL + "/9.8/a.iso", Checksum: sha256Hex(body)})
	p, err := m.DownloadAlma("9.8")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != string(body) || primaryHits != 1 || vaultHits != 1 {
		t.Fatalf("body/hits wrong: %q primary=%d vault=%d", got, primaryHits, vaultHits)
	}

	// Wrong checksum from the vault must still be refused and deleted.
	m2 := NewManager(context.Background())
	dir := t.TempDir()
	_ = m2.SetCacheDir(dir)
	withAlmaRelease(t, "9.8", configs.AlmaRelease{URL: pURL + "/9.8/a.iso", Checksum: sha256Hex([]byte("other"))})
	if _, err := m2.DownloadAlma("9.8"); err == nil || !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("vault download with bad checksum must fail: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.iso")); err == nil {
		t.Error("mismatching vault download must be deleted")
	}
}

func TestDownloadAlma_NoFallbackOnNon404(t *testing.T) {
	vaultHit := false
	pURL, _ := fakeMirror(t,
		func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) },
		func(w http.ResponseWriter, r *http.Request) { vaultHit = true })
	m := NewManager(context.Background())
	_ = m.SetCacheDir(t.TempDir())
	withAlmaRelease(t, "9.8", configs.AlmaRelease{URL: pURL + "/9.8/a.iso", Checksum: sha256Hex([]byte("x"))})
	if _, err := m.DownloadAlma("9.8"); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("want HTTP 500 error, got %v", err)
	}
	if vaultHit {
		t.Error("vault must not be tried on a non-404 error")
	}
}

func TestResolveAlmaTreeURL(t *testing.T) {
	ok := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method %s, want HEAD", r.Method)
		}
	}
	nf := func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
	ctx := context.Background()

	t.Run("primary ok", func(t *testing.T) {
		p, _ := fakeMirror(t, ok, nf)
		if got, err := ResolveAlmaTreeURL(ctx, nil, p+"/9.9/BaseOS/"); err != nil || got != p+"/9.9/BaseOS/" {
			t.Fatalf("%q %v", got, err)
		}
	})
	t.Run("primary 404 vault ok", func(t *testing.T) {
		p, v := fakeMirror(t, nf, ok)
		if got, err := ResolveAlmaTreeURL(ctx, nil, p+"/9.8/BaseOS/"); err != nil || got != v+"/9.8/BaseOS/" {
			t.Fatalf("%q %v", got, err)
		}
	})
	t.Run("both 404", func(t *testing.T) {
		p, _ := fakeMirror(t, nf, nf)
		if _, err := ResolveAlmaTreeURL(ctx, nil, p+"/9.8/BaseOS/"); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("primary 500 no vault", func(t *testing.T) {
		p, _ := fakeMirror(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, ok)
		if _, err := ResolveAlmaTreeURL(ctx, nil, p+"/9.8/BaseOS/"); err == nil {
			t.Fatal("want error")
		}
	})
}
