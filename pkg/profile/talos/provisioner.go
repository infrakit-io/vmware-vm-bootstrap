package talos

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/profile"
)

type Provisioner struct{}

func New() *Provisioner { return &Provisioner{} }

func (p *Provisioner) Name() string { return "talos" }

func (p *Provisioner) ProvisionAndBoot(ctx context.Context, in profile.Input, rt profile.Runtime) (profile.Result, error) {
	version := normalizeTalosVersion(in.OSVersion)
	if version == "" {
		return profile.Result{}, fmt.Errorf("talos version is required")
	}

	isoPath, err := downloadTalosISO(ctx, version, in.OSSchematicID)
	if err != nil {
		return profile.Result{}, err
	}
	rt.Logger.Info("Talos ISO ready", "path", isoPath)

	uploadPath := fmt.Sprintf("ISO/talos/%s", filepath.Base(isoPath))
	if err := rt.ISOManager.UploadToDatastore(rt.ISODatastore, isoPath, uploadPath,
		in.VCenterHost, in.VCenterUsername, in.VCenterPassword, in.VCenterInsecure); err != nil {
		return profile.Result{}, fmt.Errorf("failed to upload Talos ISO: %w", err)
	}

	mountPath := fmt.Sprintf("[%s] %s", rt.ISODatastoreName, uploadPath)
	if err := rt.ISOManager.MountSingleISO(rt.CreatedVM, mountPath, "Talos"); err != nil {
		return profile.Result{}, fmt.Errorf("failed to mount Talos ISO: %w", err)
	}

	if err := rt.Creator.PowerOn(rt.CreatedVM); err != nil {
		return profile.Result{}, fmt.Errorf("failed to power on VM: %w", err)
	}

	if err := rt.ISOManager.EnsureCDROMsConnectedAfterBoot(rt.CreatedVM); err != nil {
		rt.Logger.Warn("CD-ROM post-boot check failed (continuing)", "error", err)
	}

	return profile.Result{}, nil
}

func (p *Provisioner) PostInstall(ctx context.Context, in profile.Input, rt profile.Runtime, res profile.Result) error {
	_ = ctx
	_ = in
	_ = rt
	_ = res
	return nil
}

func normalizeTalosVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

func talosISOURL(version, schematicID string) string {
	schematicID = strings.TrimSpace(schematicID)
	if schematicID != "" {
		return fmt.Sprintf("https://factory.talos.dev/image/%s/%s/metal-amd64.iso", schematicID, version)
	}
	return fmt.Sprintf("https://github.com/siderolabs/talos/releases/download/%s/metal-amd64.iso", version)
}

func talosCacheFilename(version, schematicID string) string {
	base := "talos-" + strings.TrimPrefix(version, "v")
	if schematicID != "" {
		shortID := schematicID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		base += "-sch-" + shortID
	}
	return base + ".iso"
}

func downloadTalosISO(ctx context.Context, version, schematicID string) (string, error) {
	cacheDir := filepath.Join(configs.Defaults.ISO.CacheDir, "talos")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create Talos cache directory: %w", err)
	}

	localPath := filepath.Join(cacheDir, talosCacheFilename(version, schematicID))
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	url := talosISOURL(version, schematicID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Talos ISO request: %w", err)
	}

	client := &http.Client{Timeout: configs.Defaults.Timeouts.Download()}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download Talos ISO: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download Talos ISO: unexpected status %d from %s", resp.StatusCode, url)
	}

	tmpPath := localPath + ".part"
	out, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to create Talos ISO temp file: %w", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to write Talos ISO: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to close Talos ISO temp file: %w", err)
	}

	if err := os.Rename(tmpPath, localPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to finalize Talos ISO cache file: %w", err)
	}

	return localPath, nil
}
