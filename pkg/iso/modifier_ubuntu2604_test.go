package iso

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The boot configs below are byte copies of /boot/grub/grub.cfg and
// /boot/grub/loopback.cfg from ubuntu-26.04.1-live-server-amd64.iso
// (sha256 cc8a95cd...d927). They pin that the autoinstall rewrite still
// matches the 26.04 layout: every kernel line gets "autoinstall ds=nocloud"
// before the "---" separator, and the menu timeout is shortened.
func TestModifyGRUBConfigs_Ubuntu2604ISO(t *testing.T) {
	m := NewManager(context.Background())
	extract := t.TempDir()

	for _, name := range []string{"grub.cfg", "loopback.cfg"} {
		src, err := os.ReadFile(filepath.Join("testdata", "ubuntu-26.04.1", name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		dst := filepath.Join(extract, "boot", "grub", name)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(dst, src, 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if err := m.modifyGRUBConfigs(extract); err != nil {
		t.Fatalf("modifyGRUBConfigs: %v", err)
	}

	kernelLine := regexp.MustCompile(`(?m)^\s*linux\s+/casper/vmlinuz.*$`)
	for _, name := range []string{"grub.cfg", "loopback.cfg"} {
		out, err := os.ReadFile(filepath.Join(extract, "boot", "grub", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := string(out)
		lines := kernelLine.FindAllString(s, -1)
		if len(lines) == 0 {
			t.Fatalf("%s: no kernel line found after rewrite:\n%s", name, s)
		}
		for _, l := range lines {
			if !strings.Contains(l, "autoinstall ds=nocloud ---") {
				t.Errorf("%s: kernel line lacks autoinstall before ---: %q", name, l)
			}
		}
		if name == "grub.cfg" && !strings.Contains(s, "set timeout=5") {
			t.Errorf("grub.cfg: timeout not shortened:\n%s", s)
		}
	}
}
