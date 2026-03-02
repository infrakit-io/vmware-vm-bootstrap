package talos

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bibi40k/vmware-vm-bootstrap/configs"
	isomocks "github.com/Bibi40k/vmware-vm-bootstrap/pkg/iso/mocks"
	"github.com/Bibi40k/vmware-vm-bootstrap/pkg/profile"
	vmmocks "github.com/Bibi40k/vmware-vm-bootstrap/pkg/vm/mocks"
	"github.com/stretchr/testify/mock"
)

func TestNormalizeTalosVersion(t *testing.T) {
	if got := normalizeTalosVersion("1.2.3"); got != "v1.2.3" {
		t.Fatalf("unexpected normalized version: %q", got)
	}
	if got := normalizeTalosVersion(" v1.2.3 "); got != "v1.2.3" {
		t.Fatalf("unexpected normalized version: %q", got)
	}
	if got := normalizeTalosVersion(" "); got != "" {
		t.Fatalf("expected empty, got: %q", got)
	}
}

func TestTalosISOURL(t *testing.T) {
	if got := talosISOURL("v1.2.3", "schem"); got != "https://factory.talos.dev/image/schem/v1.2.3/metal-amd64.iso" {
		t.Fatalf("unexpected talos url: %q", got)
	}
	if got := talosISOURL("v1.2.3", ""); got != "https://github.com/siderolabs/talos/releases/download/v1.2.3/metal-amd64.iso" {
		t.Fatalf("unexpected talos url: %q", got)
	}
}

func TestTalosCacheFilename(t *testing.T) {
	if got := talosCacheFilename("v1.2.3", ""); got != "talos-1.2.3.iso" {
		t.Fatalf("unexpected filename: %q", got)
	}
	if got := talosCacheFilename("v1.2.3", "1234567890abcdef"); got != "talos-1.2.3-sch-1234567890ab.iso" {
		t.Fatalf("unexpected filename: %q", got)
	}
}

func TestDownloadTalosISO_UsesCache(t *testing.T) {
	old := configs.Defaults.ISO.CacheDir
	t.Cleanup(func() { configs.Defaults.ISO.CacheDir = old })
	configs.Defaults.ISO.CacheDir = t.TempDir()

	version := "v1.2.3"
	schematic := "abc123"
	cachePath := filepath.Join(configs.Defaults.ISO.CacheDir, "talos", talosCacheFilename(version, schematic))
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := downloadTalosISO(context.Background(), version, schematic)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != cachePath {
		t.Fatalf("unexpected path: %s", got)
	}
}

func TestProvisionAndBoot_RequiresVersion(t *testing.T) {
	p := New()
	_, err := p.ProvisionAndBoot(context.Background(), profile.Input{}, profile.Runtime{})
	if err == nil || err.Error() != "talos version is required" {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestProvisionAndBoot_SuccessFromCache(t *testing.T) {
	old := configs.Defaults.ISO.CacheDir
	t.Cleanup(func() { configs.Defaults.ISO.CacheDir = old })
	configs.Defaults.ISO.CacheDir = t.TempDir()

	version := "v1.2.3"
	schematic := "abc123"
	cachePath := filepath.Join(configs.Defaults.ISO.CacheDir, "talos", talosCacheFilename(version, schematic))
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	isoMgr.On("UploadToDatastore", mock.Anything, cachePath, "ISO/talos/"+filepath.Base(cachePath), "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("MountSingleISO", mock.Anything, "[ds] ISO/talos/"+filepath.Base(cachePath), "Talos").Return(nil).Once()
	creator.On("PowerOn", mock.Anything).Return(nil).Once()
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", mock.Anything).Return(nil).Once()

	p := New()
	_, err := p.ProvisionAndBoot(context.Background(), profile.Input{
		OSVersion:         version,
		OSSchematicID:     schematic,
		VCenterHost:       "vc",
		VCenterUsername:   "user",
		VCenterPassword:   "pass",
		VCenterInsecure:   true,
		NetworkInterface:  "eth0",
		DataDiskMountPath: "/data",
	}, profile.Runtime{
		Creator:          creator,
		ISOManager:       isoMgr,
		ISODatastoreName: "ds",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	isoMgr.AssertExpectations(t)
	creator.AssertExpectations(t)
}
