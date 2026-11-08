package iso

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
)

func TestShippedAlmaReleasesPassChecksumValidation(t *testing.T) {
	if len(configs.AlmaReleases.Releases) == 0 {
		t.Fatal("no AlmaLinux releases configured")
	}
	for v, r := range configs.AlmaReleases.Releases {
		if !sha256HexRe.MatchString(r.Checksum) {
			t.Errorf("alma %s: checksum %q is not a SHA256 hex digest", v, r.Checksum)
		}
		for name, u := range map[string]string{"url": r.URL, "baseos_url": r.BaseOSURL, "appstream_url": r.AppStreamURL} {
			if !strings.HasPrefix(u, "https://") {
				t.Errorf("alma %s: %s %q must be https", v, name, u)
			}
		}
		if !strings.Contains(r.URL, "AlmaLinux-"+v+"-") || strings.Contains(r.URL, "latest") {
			t.Errorf("alma %s: url must be pinned to the exact minor, got %s", v, r.URL)
		}
	}
}

func withAlmaRelease(t *testing.T, version string, r configs.AlmaRelease) {
	t.Helper()
	old := configs.AlmaReleases
	configs.AlmaReleases = configs.AlmaReleasesConfig{Releases: map[string]configs.AlmaRelease{version: r}}
	t.Cleanup(func() { configs.AlmaReleases = old })
}

func TestDownloadAlma_Refusals(t *testing.T) {
	m := NewManager(context.Background())
	if err := m.SetCacheDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DownloadAlma("9.0"); err == nil || !strings.Contains(err.Error(), "unsupported AlmaLinux version") {
		t.Errorf("unknown version: %v", err)
	}
	for _, cs := range []string{"", "  ", "deadbeef", strings.Repeat("z", 64)} {
		withAlmaRelease(t, "9.8", configs.AlmaRelease{URL: "http://127.0.0.1:1/x.iso", Checksum: cs})
		_, err := m.DownloadAlma("9.8")
		if err == nil {
			t.Errorf("checksum %q must be refused", cs)
		}
		if cs == "" && !strings.Contains(err.Error(), "no SHA256 checksum") {
			t.Errorf("missing-checksum message: %v", err)
		}
	}
}

func TestDownloadAlma_CachedISOMustMatch(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(context.Background())
	_ = m.SetCacheDir(dir)
	body := []byte("alma")
	withAlmaRelease(t, "9.8", configs.AlmaRelease{URL: "http://127.0.0.1:1/a.iso", Checksum: sha256Hex(body)})
	_ = os.WriteFile(filepath.Join(dir, "a.iso"), body, 0644)
	if p, err := m.DownloadAlma("9.8"); err != nil || filepath.Base(p) != "a.iso" {
		t.Fatalf("verified cache must be used: %q %v", p, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "a.iso"), []byte("tampered"), 0644)
	if _, err := m.DownloadAlma("9.8"); err == nil { // re-download hits a closed port
		t.Fatal("tampered cache must be discarded and the download must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.iso")); err == nil {
		t.Fatal("tampered ISO must be deleted")
	}
}

func TestPatchAlmaISOLinux(t *testing.T) {
	orig, err := os.ReadFile("testdata/alma-9.8-isolinux.cfg")
	if err != nil {
		t.Fatal(err)
	}
	got, err := patchAlmaISOLinux(string(orig), 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\ntimeout 50\n") {
		t.Error("timeout not set to 50 (1/10 s units)")
	}
	if n := strings.Count(got, "menu default"); n != 1 {
		t.Fatalf("exactly one default entry expected, got %d", n)
	}
	if !strings.Contains(got, "label linux\n  menu default\n  menu label ^Install AlmaLinux 9.8") {
		t.Error("plain Install entry must be the default")
	}
	if strings.Contains(got, "label check\n  menu label Test this ^media & install AlmaLinux 9.8\n  menu default") {
		t.Error("media check must no longer be the default")
	}
	// Idempotent, and the kernel args are untouched (inst.stage2 label must stay).
	again, _ := patchAlmaISOLinux(got, 5)
	if again != got {
		t.Error("patch must be idempotent")
	}
	if !strings.Contains(got, "inst.stage2=hd:LABEL=AlmaLinux-9-8-x86_64-dvd quiet") {
		t.Error("kernel args changed")
	}
	if _, err := patchAlmaISOLinux("nothing useful", 5); err == nil {
		t.Error("unexpected layout must fail loudly")
	}
}

func TestCreateKickstartISO(t *testing.T) {
	m := NewManager(context.Background())
	_ = m.SetCacheDir(t.TempDir())
	p, err := m.CreateKickstartISO("text\n", "da1")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	// ISO9660 primary volume descriptor at sector 16; volume ID is bytes 40..72.
	if len(data) < 16*2048+72 || !strings.HasPrefix(strings.TrimSpace(string(data[16*2048+40:16*2048+72])), "OEMDRV") {
		t.Fatalf("volume ID is not OEMDRV: %q", data[16*2048+40:16*2048+72])
	}
	if !strings.Contains(strings.ToLower(string(data)), "ks.cfg") || !strings.Contains(string(data), "text\n") {
		t.Error("ks.cfg not found in the image")
	}
}

// Real xorriso on a tiny hybrid-less ISO: the patched menu is written, the volume ID survives.
func TestModifyAlmaISO_RealXorriso(t *testing.T) {
	if _, err := exec.LookPath("xorriso"); err != nil {
		t.Skip("xorriso not installed")
	}
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree", "isolinux")
	_ = os.MkdirAll(tree, 0755)
	cfg, _ := os.ReadFile("testdata/alma-9.8-isolinux.cfg")
	_ = os.WriteFile(filepath.Join(tree, "isolinux.cfg"), cfg, 0644)
	src := filepath.Join(dir, "AlmaLinux-9.8-x86_64-boot.iso")
	if out, err := exec.Command("xorriso", "-as", "mkisofs", "-V", "AlmaLinux-9-8-x86_64-dvd", "-o", src, filepath.Join(dir, "tree")).CombinedOutput(); err != nil {
		t.Fatalf("mkisofs: %v\n%s", err, out)
	}
	m := NewManager(context.Background())
	out, created, err := m.ModifyAlmaISO(src)
	if err != nil || !created {
		t.Fatalf("modify: created=%v err=%v", created, err)
	}
	if !strings.HasSuffix(out, "-unattended.iso") {
		t.Errorf("unexpected name %s", out)
	}
	rep, _ := exec.Command("xorriso", "-indev", out, "-find", "/isolinux/isolinux.cfg", "-exec", "report_lba").CombinedOutput()
	if !strings.Contains(string(rep), "isolinux.cfg") {
		t.Fatalf("patched file missing: %s", rep)
	}
	ext := filepath.Join(dir, "got.cfg")
	if o, err := exec.Command("xorriso", "-osirrox", "on", "-indev", out, "-extract", "/isolinux/isolinux.cfg", ext).CombinedOutput(); err != nil {
		t.Fatalf("extract: %v\n%s", err, o)
	}
	got, _ := os.ReadFile(ext)
	if !strings.Contains(string(got), "\ntimeout 50\n") || !strings.Contains(string(got), "label linux\n  menu default") {
		t.Error("boot menu not patched in the repacked ISO")
	}
	vol, _ := exec.Command("xorriso", "-indev", out, "-pvd_info").CombinedOutput()
	if !strings.Contains(string(vol), "AlmaLinux-9-8-x86_64-dvd") {
		t.Errorf("volume ID lost: %s", vol)
	}
	// Second call hits the cache.
	if _, created, err := m.ModifyAlmaISO(src); err != nil || created {
		t.Errorf("expected cache hit: created=%v err=%v", created, err)
	}
}
