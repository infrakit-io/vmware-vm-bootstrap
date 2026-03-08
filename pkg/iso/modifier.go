package iso

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
)

const isoModifierVersion = "2026-02-24-20.04-append-v2"

type isoMeta struct {
	Version       string `json:"version"`
	SourcePath    string `json:"source_path"`
	SourceSize    int64  `json:"source_size"`
	SourceModTime int64  `json:"source_mod_time"`
}

// ModifyUbuntuISO modifies Ubuntu ISO to enable autoinstall mode.
// Returns path to modified ISO and whether it was newly created (vs cached).
// wasCreated=true means datastore upload should be forced (overwrite stale version).
//
// Modifications:
// - GRUB timeout: 30s → 5s
// - Kernel parameter: adds "autoinstall ds=nocloud"
// - Default boot entry: set default=0
func (m *Manager) ModifyUbuntuISO(originalISOPath string) (path string, wasCreated bool, err error) {
	// Create modified ISO filename
	dir := filepath.Dir(originalISOPath)
	base := filepath.Base(originalISOPath)
	modifiedName := strings.Replace(base, ".iso", configs.Defaults.ISO.UbuntuModifiedSuffix+".iso", 1)
	modifiedPath := filepath.Join(dir, modifiedName)
	metaPath := modifiedPath + ".meta.json"

	srcInfo, srcErr := os.Stat(originalISOPath)
	if srcErr != nil {
		return "", false, fmt.Errorf("stat source ISO: %w", srcErr)
	}

	// Check if modified ISO already exists (idempotency)
	if _, err := os.Stat(modifiedPath); err == nil {
		if meta, err := readISOMeta(metaPath); err == nil {
			if meta.Version == isoModifierVersion &&
				meta.SourceSize == srcInfo.Size() &&
				meta.SourceModTime == srcInfo.ModTime().Unix() {
				fmt.Printf("✅ Modified Ubuntu ISO already exists: %s\n", modifiedPath)
				return modifiedPath, false, nil
			}
		}
		// Stale or unknown meta -> rebuild
		_ = os.Remove(modifiedPath)
		_ = os.Remove(metaPath)
	}

	fmt.Println("⚙️  Modifying Ubuntu ISO for autoinstall...")

	// Step 1: Extract ISO
	extractDir := filepath.Join(dir, configs.Defaults.ISO.ExtractDirName)

	// Clean up previous extraction (files may be read-only)
	if err := m.cleanupExtractDir(extractDir); err != nil {
		return "", false, fmt.Errorf("failed to clean extract dir: %w", err)
	}
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return "", false, fmt.Errorf("failed to create extract dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(extractDir)
	}() // Cleanup

	fmt.Println("   Extracting ISO (this may take 1-2 minutes)...")
	if err := m.extractISOWithProgress(originalISOPath, extractDir); err != nil {
		return "", false, fmt.Errorf("failed to extract ISO: %w", err)
	}

	// Make all extracted files writable (xorriso extracts as read-only)
	fmt.Println("   Setting file permissions...")
	if err := m.makeExtractedFilesWritable(extractDir); err != nil {
		return "", false, fmt.Errorf("failed to set permissions: %w", err)
	}

	// Step 2: Modify GRUB configs
	fmt.Println("   Modifying GRUB configuration...")
	if err := m.modifyGRUBConfigs(extractDir); err != nil {
		return "", false, fmt.Errorf("failed to modify GRUB configs: %w", err)
	}

	// Step 3: Repack ISO
	fmt.Println("   Repacking ISO...")
	if err := m.repackISO(extractDir, modifiedPath); err != nil {
		return "", false, fmt.Errorf("failed to repack ISO: %w", err)
	}

	if err := writeISOMeta(metaPath, isoMeta{
		Version:       isoModifierVersion,
		SourcePath:    originalISOPath,
		SourceSize:    srcInfo.Size(),
		SourceModTime: srcInfo.ModTime().Unix(),
	}); err != nil {
		return "", false, fmt.Errorf("failed to write ISO metadata: %w", err)
	}

	fmt.Printf("✅ Ubuntu ISO modified successfully: %s\n", modifiedPath)
	return modifiedPath, true, nil
}

func readISOMeta(path string) (isoMeta, error) {
	var meta isoMeta
	data, err := os.ReadFile(path)
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

func writeISOMeta(path string, meta isoMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// extractISOWithProgress extracts ISO with progress indicator
func (m *Manager) extractISOWithProgress(isoPath, extractDir string) error {
	// Start extraction in background
	cmd := exec.CommandContext(m.ctx, "xorriso",
		"-osirrox", "on",
		"-indev", isoPath,
		"-extract", "/", extractDir,
	)

	// Start command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start xorriso: %w", err)
	}

	// Progress ticker
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	ticker := time.NewTicker(configs.Defaults.Timeouts.ExtractProgress())
	defer ticker.Stop()

	startTime := time.Now()
	for {
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("xorriso extract failed: %w", err)
			}
			elapsed := time.Since(startTime)
			fmt.Printf("\r   Extraction complete (took %d seconds)\n", int(elapsed.Seconds()))
			return nil

		case <-ticker.C:
			elapsed := time.Since(startTime)
			fmt.Printf("\r   Extracting... %ds elapsed", int(elapsed.Seconds()))
		}
	}
}

// makeExtractedFilesWritable makes all extracted files writable
// xorriso extracts files as read-only by default
func (m *Manager) makeExtractedFilesWritable(extractDir string) error {
	return filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Make file/directory writable (add write permission for owner)
		currentMode := info.Mode()
		newMode := currentMode | 0200 // Add write permission for owner

		if err := os.Chmod(path, newMode); err != nil {
			// Ignore permission errors (some files may be protected)
			return nil
		}

		return nil
	})
}

// cleanupExtractDir removes extract directory, handling read-only files
func (m *Manager) cleanupExtractDir(extractDir string) error {
	// Check if directory exists
	if _, err := os.Stat(extractDir); os.IsNotExist(err) {
		// Directory doesn't exist - nothing to clean
		return nil
	}

	// Make all files writable first (xorriso creates read-only files)
	_ = m.makeExtractedFilesWritable(extractDir) // Ignore errors, try removal anyway

	// Now remove directory
	if err := os.RemoveAll(extractDir); err != nil {
		return err
	}

	return nil
}

// modifyGRUBConfigs finds and modifies all GRUB configuration files
func (m *Manager) modifyGRUBConfigs(extractDir string) error {
	// List of GRUB config files to modify (from Python implementation)
	configFiles := []string{
		"boot/grub/grub.cfg",     // UEFI boot
		"boot/grub/loopback.cfg", // Loopback boot
		"isolinux/txt.cfg",       // BIOS boot
	}

	modifiedCount := 0
	for _, relPath := range configFiles {
		fullPath := filepath.Join(extractDir, relPath)

		// Skip if file doesn't exist (ISO structure may vary)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			continue
		}

		if err := m.modifyGRUBFile(fullPath); err != nil {
			// Don't fail entire process if one file fails (best-effort)
			fmt.Printf("   ⚠️  Warning: Failed to modify %s: %v\n", relPath, err)
			continue
		}

		modifiedCount++
		fmt.Printf("   ✅ Modified: %s\n", relPath)
	}

	if modifiedCount == 0 {
		return fmt.Errorf("no GRUB config files found or modified")
	}

	return nil
}

// modifyGRUBFile modifies a single GRUB config file
func (m *Manager) modifyGRUBFile(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	original := string(content)
	modified := original

	// 1. Fix timeout value (30 → 5 seconds)
	// Matches: "timeout 30", "set timeout=30", "timeout=30"
	timeoutRegex := regexp.MustCompile(`(?m)(^|\s)(timeout|set timeout)(\s*=?\s*)(\d+)`)
	modified = timeoutRegex.ReplaceAllString(modified, fmt.Sprintf("${1}${2}${3}%d", configs.Defaults.ISO.GRUBTimeoutSeconds))

	// 2. Set default boot entry to 0 (first entry)
	// Add or replace "set default=X" with "set default=0"
	if strings.Contains(modified, "set default=") {
		defaultRegex := regexp.MustCompile(`set default=\d+`)
		modified = defaultRegex.ReplaceAllString(modified, "set default=0")
	} else {
		// Add "set default=0" at beginning if not present
		modified = "set default=0\n" + modified
	}

	// 3. Add kernel parameter "autoinstall ds=nocloud"
	// Find all "linux" kernel boot lines and add parameter
	// Matches: "linux /casper/vmlinuz ..." or "linuxefi ..."
	kernelRegex := regexp.MustCompile(`(?m)(^\s*(?:linux|linuxefi)\s+\S+)(.*)$`)
	modified = kernelRegex.ReplaceAllStringFunc(modified, func(match string) string {
		// Check if already has autoinstall parameter
		if strings.Contains(match, "autoinstall") {
			return match
		}

		// Add "autoinstall ds=nocloud" before "---" or at end
		parts := kernelRegex.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}

		kernelCmd := parts[1]
		params := parts[2]

		// Insert before "---" if present, otherwise at end
		// CRITICAL: Use "autoinstall ds=nocloud" (WITHOUT path specification!)
		// Do NOT add path like ds=nocloud;s=/cdrom - that's for single-ISO method
		// We use dual-ISO: Ubuntu boot + NoCloud seed with CIDATA label (auto-detected)
		if strings.Contains(params, "---") {
			params = strings.Replace(params, "---", "autoinstall ds=nocloud ---", 1)
		} else {
			params = " autoinstall ds=nocloud" + params
		}

		return kernelCmd + params
	})

	// 4. Add autoinstall for ISOLINUX "append" lines (used by 20.04 BIOS)
	appendRegex := regexp.MustCompile(`(?m)^(\s*append\s+)(.*)$`)
	modified = appendRegex.ReplaceAllStringFunc(modified, func(match string) string {
		if strings.Contains(match, "autoinstall") {
			return match
		}
		parts := appendRegex.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		prefix := parts[1]
		params := parts[2]
		if strings.Contains(params, "---") {
			params = strings.Replace(params, "---", "autoinstall ds=nocloud ---", 1)
		} else {
			params = "autoinstall ds=nocloud " + params
		}
		return prefix + params
	})

	// Only write if content changed
	if modified == original {
		return nil
	}

	// Write modified content
	if err := os.WriteFile(filePath, []byte(modified), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// repackISO repacks extracted directory to ISO using genisoimage
// Matches Python implementation exactly - genisoimage handles large ISOs better than xorriso
func (m *Manager) repackISO(extractDir, outputPath string) error {
	// Find BIOS boot image (supports both grub and isolinux layouts)
	type biosBoot struct {
		path    string
		catalog string
	}
	biosCandidates := []biosBoot{
		{path: "boot/grub/i386-pc/eltorito.img", catalog: "boot.catalog"},
		{path: "isolinux/isolinux.bin", catalog: "isolinux/boot.cat"},
	}
	var biosBootPath string
	var biosCatalog string
	for _, candidate := range biosCandidates {
		fullPath := filepath.Join(extractDir, candidate.path)
		if _, err := os.Stat(fullPath); err == nil {
			biosBootPath = candidate.path
			biosCatalog = candidate.catalog
			break
		}
	}
	if biosBootPath == "" {
		return fmt.Errorf("BIOS boot image not found")
	}

	// Find UEFI boot image (try multiple locations like Python does)
	uefiBootCandidates := []string{
		"boot/grub/efi.img",
		"EFI/ubuntu/grubx64.efi",
		"EFI/boot/bootx64.efi",
	}

	var uefiBootPath string
	for _, candidate := range uefiBootCandidates {
		fullPath := filepath.Join(extractDir, candidate)
		if _, err := os.Stat(fullPath); err == nil {
			uefiBootPath = candidate
			fmt.Printf("   Found EFI boot: %s\n", candidate)
			break
		}
	}

	// Build genisoimage command (matches Python exactly)
	args := []string{
		"-r",                                      // Rock Ridge
		"-V", configs.Defaults.ISO.UbuntuVolumeID, // Volume ID (matches Python)
		"-J", "-joliet-long", // Joliet
		"-o", outputPath, // Output file
		// BIOS boot (legacy)
		"-b", biosBootPath,
		"-c", biosCatalog,
		"-no-emul-boot",
		"-boot-load-size", "4",
		"-boot-info-table",
	}

	// Add UEFI boot if found
	if uefiBootPath != "" {
		args = append(args,
			"-eltorito-alt-boot",
			"-e", uefiBootPath,
			"-no-emul-boot",
		)
	} else {
		fmt.Println("   ⚠️  EFI boot not found - creating BIOS-only ISO")
	}

	// Source directory
	args = append(args, extractDir)

	// Execute genisoimage
	cmd := exec.CommandContext(m.ctx, "genisoimage", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("genisoimage failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}
