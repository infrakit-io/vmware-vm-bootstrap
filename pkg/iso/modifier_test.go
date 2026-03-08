package iso

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
)

func TestModifyGRUBFile_AddsDefaultTimeoutAndAutoinstall(t *testing.T) {
	m := NewManager(context.Background())
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "grub.cfg")

	original := `set timeout=30
menuentry "Install" {
 linux /casper/vmlinuz --- 
}
`
	if err := os.WriteFile(cfg, []byte(original), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := m.modifyGRUBFile(cfg); err != nil {
		t.Fatalf("modifyGRUBFile: %v", err)
	}

	updated, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(updated)

	if !strings.Contains(s, "set default=0") {
		t.Errorf("expected set default=0 to be present")
	}
	if !strings.Contains(s, "set timeout=5") {
		t.Errorf("expected timeout to be 5, got:\n%s", s)
	}
	if !strings.Contains(s, "autoinstall ds=nocloud ---") {
		t.Errorf("expected autoinstall ds=nocloud before ---, got:\n%s", s)
	}
}

func TestModifyGRUBFile_DoesNotDuplicateAutoinstall(t *testing.T) {
	m := NewManager(context.Background())
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "grub.cfg")

	original := `set timeout=30
set default=1
menuentry "Install" {
 linux /casper/vmlinuz autoinstall ds=nocloud --- 
}
`
	if err := os.WriteFile(cfg, []byte(original), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := m.modifyGRUBFile(cfg); err != nil {
		t.Fatalf("modifyGRUBFile: %v", err)
	}

	updated, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(updated)

	if strings.Count(s, "autoinstall") != 1 {
		t.Errorf("expected autoinstall to appear once, got:\n%s", s)
	}
	if !strings.Contains(s, "set default=0") {
		t.Errorf("expected set default=0 to replace existing default")
	}
}

func TestModifyGRUBFile_AddsAutoinstallToIsolinuxAppend(t *testing.T) {
	m := NewManager(context.Background())
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "txt.cfg")

	original := `default live
label live
  menu label ^Install Ubuntu Server
  kernel /casper/vmlinuz
  append   initrd=/casper/initrd quiet  ---
`
	if err := os.WriteFile(cfg, []byte(original), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := m.modifyGRUBFile(cfg); err != nil {
		t.Fatalf("modifyGRUBFile: %v", err)
	}

	updated, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(updated)
	if !strings.Contains(s, "append") || !strings.Contains(s, "autoinstall ds=nocloud ---") {
		t.Errorf("expected autoinstall on append line, got:\n%s", s)
	}
}

func TestModifyGRUBConfigs_NoFiles(t *testing.T) {
	m := NewManager(context.Background())
	tmp := t.TempDir()

	if err := m.modifyGRUBConfigs(tmp); err == nil {
		t.Fatalf("expected error when no GRUB files exist")
	}
}

func TestModifyGRUBConfigs_OneFile(t *testing.T) {
	m := NewManager(context.Background())
	tmp := t.TempDir()

	cfg := filepath.Join(tmp, "boot/grub/grub.cfg")
	if err := os.MkdirAll(filepath.Dir(cfg), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfg, []byte("timeout 30\nlinux /vmlinuz ---\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := m.modifyGRUBConfigs(tmp); err != nil {
		t.Fatalf("modifyGRUBConfigs: %v", err)
	}
}

func TestRepackISO_MissingBIOSBoot(t *testing.T) {
	m := NewManager(context.Background())
	tmp := t.TempDir()
	out := filepath.Join(tmp, "out.iso")

	if err := m.repackISO(tmp, out); err == nil {
		t.Fatalf("expected error when BIOS boot image missing")
	}
}

func TestModifyGRUBFile_MissingFile(t *testing.T) {
	mgr := NewManager(context.Background())
	err := mgr.modifyGRUBFile(filepath.Join(t.TempDir(), "missing.cfg"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMakeExtractedFilesWritable(t *testing.T) {
	mgr := NewManager(context.Background())
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("data"), 0444); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := mgr.makeExtractedFilesWritable(dir); err != nil {
		t.Fatalf("makeExtractedFilesWritable: %v", err)
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode()&0200 == 0 {
		t.Fatalf("expected owner write bit to be set, mode=%v", info.Mode())
	}
}

func TestCleanupExtractDir(t *testing.T) {
	mgr := NewManager(context.Background())
	dir := filepath.Join(t.TempDir(), "extract")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("data"), 0444); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := mgr.cleanupExtractDir(dir); err != nil {
		t.Fatalf("cleanupExtractDir: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected dir to be removed, err=%v", err)
	}
}

func TestModifyUbuntuISO_UsesCachedWhenMetaMatches(t *testing.T) {
	m := NewManager(context.Background())
	tmp := t.TempDir()

	orig := filepath.Join(tmp, "ubuntu-20.04.iso")
	if err := os.WriteFile(orig, []byte("iso"), 0644); err != nil {
		t.Fatalf("write orig: %v", err)
	}
	if err := os.Chtimes(orig, time.Now(), time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	info, err := os.Stat(orig)
	if err != nil {
		t.Fatalf("stat orig: %v", err)
	}

	modified := filepath.Join(tmp, "ubuntu-20.04"+configs.Defaults.ISO.UbuntuModifiedSuffix+".iso")
	if err := os.WriteFile(modified, []byte("cached"), 0644); err != nil {
		t.Fatalf("write modified: %v", err)
	}
	metaPath := modified + ".meta.json"
	if err := writeISOMeta(metaPath, isoMeta{
		Version:       isoModifierVersion,
		SourcePath:    orig,
		SourceSize:    info.Size(),
		SourceModTime: info.ModTime().Unix(),
	}); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	got, created, err := m.ModifyUbuntuISO(orig)
	if err != nil {
		t.Fatalf("ModifyUbuntuISO: %v", err)
	}
	if created {
		t.Fatalf("expected cached ISO, got created=true")
	}
	if got != modified {
		t.Fatalf("expected %s, got %s", modified, got)
	}
}
