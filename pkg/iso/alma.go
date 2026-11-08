package iso

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/kdomanski/iso9660"
)

const almaModifierVersion = "2026-11-08-alma9-isolinux-v1"

// almaISOLinuxCfg is the BIOS boot menu inside the AlmaLinux boot ISO.
const almaISOLinuxCfg = "/isolinux/isolinux.cfg"

// DownloadAlma downloads the AlmaLinux boot ISO and verifies its SHA256 against
// the checksum pinned in configs/alma-releases.yaml (mandatory, same rules as Ubuntu).
func (m *Manager) DownloadAlma(version string) (string, error) {
	release, ok := configs.AlmaReleases.Releases[version]
	if !ok {
		supported := make([]string, 0, len(configs.AlmaReleases.Releases))
		for v := range configs.AlmaReleases.Releases {
			supported = append(supported, v)
		}
		sort.Strings(supported)
		return "", fmt.Errorf("unsupported AlmaLinux version %q (supported: %s)", version, strings.Join(supported, ", "))
	}
	checksum := strings.TrimSpace(release.Checksum)
	if checksum == "" {
		return "", fmt.Errorf("almalinux %s has no SHA256 checksum in configs/alma-releases.yaml; "+
			"refusing to use an unverified ISO (add the value from the signed CHECKSUM file)", version)
	}
	if !sha256HexRe.MatchString(checksum) {
		return "", fmt.Errorf("almalinux %s checksum %q in configs/alma-releases.yaml is not a SHA256 hex digest", version, checksum)
	}
	return m.fetchVerified(release.URL, strings.ToLower(checksum))
}

// ModifyAlmaISO patches the BIOS boot menu so the installer starts unattended
// (default entry = plain install instead of "Test this media", short timeout)
// and writes the result next to the original. The kickstart itself is NOT in
// this ISO: Anaconda loads ks.cfg from the separate OEMDRV volume.
// xorriso `-boot_image any replay` keeps the El Torito, isohybrid and GPT layout
// and the volume ID (inst.stage2=hd:LABEL=... depends on it).
// Returns the path and whether it was newly created (forces datastore re-upload).
func (m *Manager) ModifyAlmaISO(originalISOPath string) (string, bool, error) {
	dir := filepath.Dir(originalISOPath)
	base := filepath.Base(originalISOPath)
	modifiedPath := filepath.Join(dir, strings.Replace(base, ".iso", configs.Defaults.ISO.AlmaModifiedSuffix+".iso", 1))
	metaPath := modifiedPath + ".meta.json"

	srcInfo, err := os.Stat(originalISOPath)
	if err != nil {
		return "", false, fmt.Errorf("stat source ISO: %w", err)
	}
	if _, err := os.Stat(modifiedPath); err == nil {
		if meta, err := readISOMeta(metaPath); err == nil &&
			meta.Version == almaModifierVersion &&
			meta.SourceSize == srcInfo.Size() &&
			meta.SourceModTime == srcInfo.ModTime().Unix() {
			return modifiedPath, false, nil
		}
		_ = os.Remove(modifiedPath)
		_ = os.Remove(metaPath)
	}

	fmt.Println("⚙️  Patching AlmaLinux boot menu for unattended install...")
	work, err := os.MkdirTemp(dir, "alma-patch-")
	if err != nil {
		return "", false, fmt.Errorf("create work dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	cfgPath := filepath.Join(work, "isolinux.cfg")
	if out, err := exec.CommandContext(m.ctx, "xorriso", "-osirrox", "on", "-indev", originalISOPath,
		"-extract", almaISOLinuxCfg, cfgPath).CombinedOutput(); err != nil {
		return "", false, fmt.Errorf("xorriso extract %s failed: %w\n%s", almaISOLinuxCfg, err, out)
	}
	orig, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", false, fmt.Errorf("read isolinux.cfg: %w", err)
	}
	patched, err := patchAlmaISOLinux(string(orig), configs.Defaults.ISO.GRUBTimeoutSeconds)
	if err != nil {
		return "", false, err
	}
	if err := os.WriteFile(cfgPath, []byte(patched), 0644); err != nil {
		return "", false, fmt.Errorf("write patched isolinux.cfg: %w", err)
	}

	if out, err := exec.CommandContext(m.ctx, "xorriso", "-indev", originalISOPath, "-outdev", modifiedPath,
		"-boot_image", "any", "replay", "-map", cfgPath, almaISOLinuxCfg, "-commit").CombinedOutput(); err != nil {
		_ = os.Remove(modifiedPath)
		return "", false, fmt.Errorf("xorriso repack failed: %w\n%s", err, out)
	}
	if err := writeISOMeta(metaPath, isoMeta{
		Version: almaModifierVersion, SourcePath: originalISOPath,
		SourceSize: srcInfo.Size(), SourceModTime: srcInfo.ModTime().Unix(),
	}); err != nil {
		return "", false, fmt.Errorf("write ISO metadata: %w", err)
	}
	fmt.Printf("✅ AlmaLinux ISO patched: %s\n", modifiedPath)
	return modifiedPath, true, nil
}

var (
	isolinuxTimeoutRe = regexp.MustCompile(`(?m)^(\s*timeout\s+)\d+\s*$`)
	isolinuxDefaultRe = regexp.MustCompile(`(?m)^[ \t]*menu default[ \t]*\n`)
	isolinuxLinuxRe   = regexp.MustCompile(`(?m)^label linux[ \t]*\n`)
)

// patchAlmaISOLinux sets the menu timeout (isolinux counts in 1/10 s) and makes
// the plain "Install" entry the default instead of "Test this media & install".
func patchAlmaISOLinux(cfg string, timeoutSeconds int) (string, error) {
	if !isolinuxTimeoutRe.MatchString(cfg) {
		return "", fmt.Errorf("isolinux.cfg: no timeout line found (unexpected AlmaLinux ISO layout)")
	}
	if !isolinuxLinuxRe.MatchString(cfg) {
		return "", fmt.Errorf("isolinux.cfg: no 'label linux' entry found (unexpected AlmaLinux ISO layout)")
	}
	out := isolinuxTimeoutRe.ReplaceAllString(cfg, fmt.Sprintf("${1}%d", timeoutSeconds*10))
	out = isolinuxDefaultRe.ReplaceAllString(out, "")
	out = isolinuxLinuxRe.ReplaceAllString(out, "label linux\n  menu default\n")
	return out, nil
}

// CreateKickstartISO writes ks.cfg into a small ISO9660 image labelled OEMDRV.
// Anaconda automatically loads ks.cfg from the root of a volume with that label.
func (m *Manager) CreateKickstartISO(kickstart, vmName string) (string, error) {
	isoPath := filepath.Join(m.cacheDir, fmt.Sprintf("oemdrv-%s.iso", vmName))
	writer, err := iso9660.NewWriter()
	if err != nil {
		return "", fmt.Errorf("failed to create ISO writer: %w", err)
	}
	defer func() { _ = writer.Cleanup() }()
	if err := writer.AddFile(bytes.NewReader([]byte(kickstart)), "ks.cfg"); err != nil {
		return "", fmt.Errorf("failed to add ks.cfg: %w", err)
	}
	f, err := os.OpenFile(isoPath, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create ISO file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := writer.WriteTo(f, configs.Defaults.ISO.OEMDRVVolumeID); err != nil {
		return "", fmt.Errorf("failed to write ISO: %w", err)
	}
	return isoPath, nil
}
