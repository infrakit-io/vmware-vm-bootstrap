package talos

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	isomocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/iso/mocks"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/profile"
	vmmocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

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

func TestProvisionerNameAndPostInstall(t *testing.T) {
	p := New()
	if p.Name() != "talos" {
		t.Fatalf("unexpected profile name: %q", p.Name())
	}
	if err := p.PostInstall(context.Background(), profile.Input{}, profile.Runtime{}, profile.Result{}); err != nil {
		t.Fatalf("unexpected post-install error: %v", err)
	}
}

func TestDownloadTalosISO_CreateCacheDirError(t *testing.T) {
	old := configs.Defaults.ISO.CacheDir
	t.Cleanup(func() { configs.Defaults.ISO.CacheDir = old })

	dir := t.TempDir()
	filePath := filepath.Join(dir, "cache-file")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	configs.Defaults.ISO.CacheDir = filePath

	_, err := downloadTalosISO(context.Background(), "v1.2.3", "abc123")
	if err == nil {
		t.Fatal("expected cache-dir creation error")
	}
}

func TestDownloadTalosISO_HTTPStatusError(t *testing.T) {
	oldCache := configs.Defaults.ISO.CacheDir
	oldTransport := http.DefaultTransport
	t.Cleanup(func() {
		configs.Defaults.ISO.CacheDir = oldCache
		http.DefaultTransport = oldTransport
	})
	configs.Defaults.ISO.CacheDir = t.TempDir()
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader("unavailable")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	_, err := downloadTalosISO(context.Background(), "v1.2.3", "")
	if err == nil {
		t.Fatal("expected http status error")
	}
}

func TestDownloadTalosISO_RequestError(t *testing.T) {
	oldCache := configs.Defaults.ISO.CacheDir
	oldTransport := http.DefaultTransport
	t.Cleanup(func() {
		configs.Defaults.ISO.CacheDir = oldCache
		http.DefaultTransport = oldTransport
	})
	configs.Defaults.ISO.CacheDir = t.TempDir()
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})

	_, err := downloadTalosISO(context.Background(), "v1.2.3", "abc123")
	if err == nil {
		t.Fatal("expected request error")
	}
}

func TestDownloadTalosISO_DownloadSuccess(t *testing.T) {
	oldCache := configs.Defaults.ISO.CacheDir
	oldTransport := http.DefaultTransport
	t.Cleanup(func() {
		configs.Defaults.ISO.CacheDir = oldCache
		http.DefaultTransport = oldTransport
	})
	configs.Defaults.ISO.CacheDir = t.TempDir()
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("iso-content")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	got, err := downloadTalosISO(context.Background(), "v1.2.3", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(content) != "iso-content" {
		t.Fatalf("unexpected downloaded content: %q", string(content))
	}
}

func talosRuntimeForErrors(t *testing.T, cachePath string) (profile.Input, profile.Runtime, *isomocks.ManagerInterface, *vmmocks.CreatorInterface) {
	t.Helper()
	isoMgr := &isomocks.ManagerInterface{}
	creator := &vmmocks.CreatorInterface{}
	in := profile.Input{
		OSVersion:       "v1.2.3",
		OSSchematicID:   "abc123",
		VCenterHost:     "vc",
		VCenterUsername: "user",
		VCenterPassword: "pass",
		VCenterInsecure: true,
	}
	rt := profile.Runtime{
		CreatedVM:        object.NewVirtualMachine(nil, types.ManagedObjectReference{Type: "VirtualMachine", Value: "vm-1"}),
		Creator:          creator,
		ISOManager:       isoMgr,
		ISODatastoreName: "ds",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	isoMgr.On("UploadToDatastore", mock.Anything, cachePath, "ISO/talos/"+filepath.Base(cachePath), "vc", "user", "pass", true).Return(nil)
	isoMgr.On("MountSingleISO", mock.Anything, "[ds] ISO/talos/"+filepath.Base(cachePath), "Talos").Return(nil)
	creator.On("PowerOn", mock.Anything).Return(nil)
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", mock.Anything).Return(nil)
	return in, rt, isoMgr, creator
}

func TestProvisionAndBoot_UploadFails(t *testing.T) {
	old := configs.Defaults.ISO.CacheDir
	t.Cleanup(func() { configs.Defaults.ISO.CacheDir = old })
	configs.Defaults.ISO.CacheDir = t.TempDir()

	cachePath := filepath.Join(configs.Defaults.ISO.CacheDir, "talos", talosCacheFilename("v1.2.3", "abc123"))
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	in, rt, isoMgr, _ := talosRuntimeForErrors(t, cachePath)
	isoMgr.ExpectedCalls = nil
	isoMgr.On("UploadToDatastore", mock.Anything, cachePath, "ISO/talos/"+filepath.Base(cachePath), "vc", "user", "pass", true).
		Return(errors.New("upload failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), in, rt)
	if err == nil || err.Error() == "" {
		t.Fatal("expected upload error")
	}
}

func TestProvisionAndBoot_MountFails(t *testing.T) {
	old := configs.Defaults.ISO.CacheDir
	t.Cleanup(func() { configs.Defaults.ISO.CacheDir = old })
	configs.Defaults.ISO.CacheDir = t.TempDir()

	cachePath := filepath.Join(configs.Defaults.ISO.CacheDir, "talos", talosCacheFilename("v1.2.3", "abc123"))
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	in, rt, isoMgr, _ := talosRuntimeForErrors(t, cachePath)
	isoMgr.ExpectedCalls = nil
	isoMgr.On("UploadToDatastore", mock.Anything, cachePath, "ISO/talos/"+filepath.Base(cachePath), "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("MountSingleISO", mock.Anything, "[ds] ISO/talos/"+filepath.Base(cachePath), "Talos").Return(errors.New("mount failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), in, rt)
	if err == nil || err.Error() == "" {
		t.Fatal("expected mount error")
	}
}

func TestProvisionAndBoot_PowerOnFails(t *testing.T) {
	old := configs.Defaults.ISO.CacheDir
	t.Cleanup(func() { configs.Defaults.ISO.CacheDir = old })
	configs.Defaults.ISO.CacheDir = t.TempDir()

	cachePath := filepath.Join(configs.Defaults.ISO.CacheDir, "talos", talosCacheFilename("v1.2.3", "abc123"))
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	in, rt, isoMgr, creator := talosRuntimeForErrors(t, cachePath)
	isoMgr.ExpectedCalls = nil
	creator.ExpectedCalls = nil
	isoMgr.On("UploadToDatastore", mock.Anything, cachePath, "ISO/talos/"+filepath.Base(cachePath), "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("MountSingleISO", mock.Anything, "[ds] ISO/talos/"+filepath.Base(cachePath), "Talos").Return(nil).Once()
	creator.On("PowerOn", mock.Anything).Return(errors.New("power on failed")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), in, rt)
	if err == nil || err.Error() == "" {
		t.Fatal("expected power-on error")
	}
}

func TestProvisionAndBoot_EnsureCDROMWarningDoesNotFail(t *testing.T) {
	old := configs.Defaults.ISO.CacheDir
	t.Cleanup(func() { configs.Defaults.ISO.CacheDir = old })
	configs.Defaults.ISO.CacheDir = t.TempDir()

	cachePath := filepath.Join(configs.Defaults.ISO.CacheDir, "talos", talosCacheFilename("v1.2.3", "abc123"))
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	in, rt, isoMgr, creator := talosRuntimeForErrors(t, cachePath)
	isoMgr.ExpectedCalls = nil
	creator.ExpectedCalls = nil
	isoMgr.On("UploadToDatastore", mock.Anything, cachePath, "ISO/talos/"+filepath.Base(cachePath), "vc", "user", "pass", true).Return(nil).Once()
	isoMgr.On("MountSingleISO", mock.Anything, "[ds] ISO/talos/"+filepath.Base(cachePath), "Talos").Return(nil).Once()
	creator.On("PowerOn", mock.Anything).Return(nil).Once()
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", mock.Anything).Return(errors.New("warn")).Once()

	_, err := New().ProvisionAndBoot(context.Background(), in, rt)
	if err != nil {
		t.Fatalf("expected warning-only path to succeed, got: %v", err)
	}
}
