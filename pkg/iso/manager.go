// Package iso provides ISO operations for VM bootstrapping.
package iso

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/kdomanski/iso9660"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// Manager handles ISO download, creation, and upload operations.
type Manager struct {
	ctx      context.Context
	cacheDir string // Local cache directory for downloaded ISOs
}

// NewManager creates a new ISO manager.
func NewManager(ctx context.Context) *Manager {
	cacheDir := configs.Defaults.ISO.CacheDir
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		fmt.Printf("⚠️  Failed to create cache dir %s: %v\n", cacheDir, err)
	}

	return &Manager{
		ctx:      ctx,
		cacheDir: cacheDir,
	}
}

// SetCacheDir sets custom cache directory for ISOs.
func (m *Manager) SetCacheDir(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create cache dir: %w", err)
	}
	m.cacheDir = dir
	return nil
}

// UbuntuRelease represents an Ubuntu release with download info.
type UbuntuRelease struct {
	Version  string // e.g., "24.04"
	URL      string
	Checksum string // SHA256
}

// GetUbuntuReleases returns available Ubuntu releases from configs/ubuntu-releases.yaml.
func GetUbuntuReleases() map[string]UbuntuRelease {
	result := make(map[string]UbuntuRelease, len(configs.UbuntuReleases.Releases))
	for version, r := range configs.UbuntuReleases.Releases {
		result[version] = UbuntuRelease{
			Version:  version,
			URL:      r.URL,
			Checksum: r.Checksum,
		}
	}
	return result
}

// DownloadUbuntu downloads Ubuntu Server ISO with optional SHA256 verification.
// Returns local path to downloaded/cached ISO.
func (m *Manager) DownloadUbuntu(version string) (string, error) {
	releases := GetUbuntuReleases()
	release, ok := releases[version]
	if !ok {
		supported := make([]string, 0, len(configs.UbuntuReleases.Releases))
		for v := range configs.UbuntuReleases.Releases {
			supported = append(supported, v)
		}
		sort.Strings(supported)
		return "", fmt.Errorf("unsupported Ubuntu version %q (supported: %s)", version, strings.Join(supported, ", "))
	}

	// Check if already cached
	filename := filepath.Base(release.URL)
	localPath := filepath.Join(m.cacheDir, filename)

	if _, err := os.Stat(localPath); err == nil {
		// File exists in cache
		if release.Checksum != "" {
			// Verify checksum
			if err := m.verifyChecksum(localPath, release.Checksum); err != nil {
				// Corrupted - re-download
				_ = os.Remove(localPath)
			} else {
				return localPath, nil // Valid cached file
			}
		} else {
			return localPath, nil // No checksum, assume valid
		}
	}

	// Download ISO
	if err := m.downloadFile(release.URL, localPath); err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	// Verify checksum if provided
	if release.Checksum != "" {
		if err := m.verifyChecksum(localPath, release.Checksum); err != nil {
			_ = os.Remove(localPath)
			return "", fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	return localPath, nil
}

// downloadFile downloads a file with progress tracking.
func (m *Manager) downloadFile(url, destPath string) error {
	// Create destination file
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer func() {
		_ = out.Close()
	}()

	// HTTP GET with context
	req, err := http.NewRequestWithContext(m.ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{
		Timeout: configs.Defaults.Timeouts.Download(),
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Get file size
	size := resp.ContentLength
	filename := filepath.Base(url)

	if size > 0 {
		fmt.Printf("📥 Downloading %s (%.1f MB)...\n", filename, float64(size)/(1024*1024))
	} else {
		fmt.Printf("📥 Downloading %s...\n", filename)
	}

	// Copy with progress bar
	counter := &progressCounter{
		total:     size,
		startTime: time.Now(),
	}
	_, err = io.Copy(out, io.TeeReader(resp.Body, counter))
	if err != nil {
		return fmt.Errorf("failed to save file: %w", err)
	}

	// Final newline after progress
	fmt.Println()
	fmt.Printf("✅ Download complete: %s\n", filename)

	return nil
}

// progressCounter tracks download progress
type progressCounter struct {
	total     int64
	current   int64
	startTime time.Time
	lastPrint time.Time
}

func (pc *progressCounter) Write(p []byte) (int, error) {
	n := len(p)
	pc.current += int64(n)

	// Print progress every UploadProgress interval
	now := time.Now()
	if now.Sub(pc.lastPrint) > configs.Defaults.Timeouts.UploadProgress() || pc.current == pc.total {
		pc.lastPrint = now
		pc.printProgress()
	}

	return n, nil
}

func (pc *progressCounter) printProgress() {
	elapsed := time.Since(pc.startTime).Seconds()
	speed := float64(pc.current) / elapsed / (1024 * 1024) // MB/s

	if pc.total > 0 {
		percent := float64(pc.current) / float64(pc.total) * 100
		fmt.Printf("\r   Progress: %.1f MB / %.1f MB (%.1f%%) - %.1f MB/s",
			float64(pc.current)/(1024*1024),
			float64(pc.total)/(1024*1024),
			percent,
			speed)
	} else {
		fmt.Printf("\r   Downloaded: %.1f MB - %.1f MB/s",
			float64(pc.current)/(1024*1024),
			speed)
	}
}

// verifyChecksum verifies SHA256 checksum of a file.
func (m *Manager) verifyChecksum(filePath, expectedChecksum string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	actualChecksum := hex.EncodeToString(hash.Sum(nil))
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	return nil
}

// CreateNoCloudISO creates NoCloud seed ISO from cloud-init configs.
// Uses a pure Go ISO9660 writer (no Joliet).
// vmName parameter allows deterministic naming to prevent duplicates.
func (m *Manager) CreateNoCloudISO(userData, metaData, networkConfig, vmName string) (string, error) {
	isoFilename := fmt.Sprintf("nocloud-%s.iso", vmName)
	isoPath := filepath.Join(m.cacheDir, isoFilename)

	writer, err := iso9660.NewWriter()
	if err != nil {
		return "", fmt.Errorf("failed to create ISO writer: %w", err)
	}
	defer func() {
		_ = writer.Cleanup()
	}()

	files := map[string]string{
		"user-data":      userData,
		"meta-data":      metaData,
		"network-config": networkConfig,
	}
	for name, content := range files {
		if err := writer.AddFile(bytes.NewReader([]byte(content)), name); err != nil {
			return "", fmt.Errorf("failed to add %s: %w", name, err)
		}
	}

	isoFile, err := os.OpenFile(isoPath, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to create ISO file: %w", err)
	}
	defer func() {
		_ = isoFile.Close()
	}()

	if err := writer.WriteTo(isoFile, configs.Defaults.ISO.NoCloudVolumeID); err != nil {
		return "", fmt.Errorf("failed to write ISO: %w", err)
	}

	return isoPath, nil
}

// CheckFileExists verifies if a file exists on the datastore.
// Returns true if file exists, false if not found.
func (m *Manager) CheckFileExists(ds *object.Datastore, remotePath string) (bool, error) {
	// Split remote path into directory and filename
	// Example: "ISO/ubuntu/file.iso" → dir="ISO/ubuntu", filename="file.iso"
	lastSlash := -1
	for i := len(remotePath) - 1; i >= 0; i-- {
		if remotePath[i] == '/' {
			lastSlash = i
			break
		}
	}

	var directory, filename string
	if lastSlash == -1 {
		// No directory, just filename
		directory = ""
		filename = remotePath
	} else {
		directory = remotePath[:lastSlash]
		filename = remotePath[lastSlash+1:]
	}

	// Get datastore browser
	browser, err := ds.Browser(m.ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get datastore browser: %w", err)
	}

	// Create search spec
	searchSpec := types.HostDatastoreBrowserSearchSpec{
		MatchPattern: []string{filename},
	}

	// Build datastore path
	datastorePath := fmt.Sprintf("[%s] %s", ds.Name(), directory)

	// Search for file
	task, err := browser.SearchDatastore(m.ctx, datastorePath, &searchSpec)
	if err != nil {
		if types.IsFileNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("datastore search failed: %w", err)
	}

	// Wait for search to complete
	err = task.Wait(m.ctx)
	if err != nil {
		if types.IsFileNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("datastore search wait failed: %w", err)
	}

	// Get task result
	info, err := task.WaitForResult(m.ctx, nil)
	if err != nil {
		if types.IsFileNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("datastore search result failed: %w", err)
	}

	// Check if file was found in results
	if result, ok := info.Result.(types.HostDatastoreBrowserSearchResults); ok {
		if len(result.File) > 0 {
			return true, nil // File exists
		}
	}

	return false, nil // File not found
}

// hashFile returns the path where we store the SHA256 of the last uploaded version of localPath.
func hashFile(localPath string) string {
	return localPath + ".uploaded.sha256"
}

// computeSHA256 computes SHA256 hash of a file.
func computeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// needsUpload returns true if the local ISO has changed since last upload (hash mismatch).
func needsUpload(localPath string) bool {
	current, err := computeSHA256(localPath)
	if err != nil {
		return true // Can't compute → upload to be safe
	}

	saved, err := os.ReadFile(hashFile(localPath))
	if err != nil {
		return true // No saved hash → first upload
	}

	return string(saved) != current
}

// saveUploadedHash saves SHA256 of the uploaded file so future runs can detect changes.
func saveUploadedHash(localPath string) {
	hash, err := computeSHA256(localPath)
	if err != nil {
		return
	}
	_ = os.WriteFile(hashFile(localPath), []byte(hash), 0644)
}

// UploadToDatastore uploads ISO to vCenter datastore.
// Uses govc CLI if available (more reliable for large files), otherwise falls back to govmomi.
// Skips upload if file exists on datastore AND local hash matches last uploaded hash.
func (m *Manager) UploadToDatastore(ds *object.Datastore, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error {
	// Check if upload is needed: file must exist on datastore AND hash must match last upload
	exists, err := m.CheckFileExists(ds, remotePath)
	if err != nil {
		return fmt.Errorf("failed to check file existence: %w", err)
	}

	if exists && !needsUpload(localPath) {
		fmt.Printf("✅ ISO already exists on datastore (unchanged): %s\n", filepath.Base(localPath))
		return nil
	}

	if exists {
		fmt.Printf("🔄 Re-uploading (ISO changed since last upload): %s\n", filepath.Base(localPath))
	}

	return m.doUpload(ds, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass, insecure, true)
}

// UploadAlways uploads ISO unconditionally (no existence/hash check).
// Use for small, VM-specific ISOs like NoCloud that must always be fresh (matches Python behavior).
func (m *Manager) UploadAlways(ds *object.Datastore, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error {
	return m.doUpload(ds, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass, insecure, false)
}

func (m *Manager) doUpload(ds *object.Datastore, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool, saveHash bool) error {

	fileInfo, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	sizeMB := float64(fileInfo.Size()) / (1024 * 1024)
	fmt.Printf("📤 Uploading %s (%.1f MB) to datastore...\n", filepath.Base(localPath), sizeMB)

	// Try govc first (more reliable for large files)
	if err := m.uploadWithGovc(ds.Name(), localPath, remotePath, vcenterHost, vcenterUser, vcenterPass, insecure); err == nil {
		fmt.Printf("✅ Upload complete: %s\n", filepath.Base(localPath))
		if saveHash {
			saveUploadedHash(localPath)
		}
		return nil
	} else {
		fmt.Printf("⚠️  govc upload failed (%v) - using govmomi fallback...\n", err)
	}

	// Fallback to govmomi Upload
	fmt.Println("⚠️  govmomi fallback (may timeout on large files)...")
	if err := m.uploadWithGovmomi(ds, localPath, remotePath); err != nil {
		return err
	}
	if saveHash {
		saveUploadedHash(localPath)
	}
	return nil
}

// uploadWithGovc uploads using govc CLI tool
func (m *Manager) uploadWithGovc(datastoreName, localPath, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error {
	// Check if govc is available
	if _, err := exec.LookPath("govc"); err != nil {
		return fmt.Errorf("govc not found: %w", err)
	}

	cmd := newGovcCmd(m.ctx, vcenterHost, vcenterUser, vcenterPass, insecure,
		"datastore.upload", "-ds", datastoreName, localPath, remotePath,
	)

	// Start upload with progress tracking
	startTime := time.Now()
	done := make(chan bool)

	// Progress goroutine
	go func() {
		ticker := time.NewTicker(configs.Defaults.Timeouts.UploadProgress())
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				elapsed := time.Since(startTime)
				fmt.Printf("\r   Uploading... elapsed: %dm %ds",
					int(elapsed.Minutes()),
					int(elapsed.Seconds())%60)
			}
		}
	}()

	// Run upload
	output, err := cmd.CombinedOutput()
	done <- true
	fmt.Println() // Newline after progress

	if err != nil {
		return fmt.Errorf("govc upload failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// uploadWithGovmomi uploads using govmomi library (fallback)
func (m *Manager) uploadWithGovmomi(ds *object.Datastore, localPath, remotePath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	p := soap.Upload{
		ContentLength: fileInfo.Size(),
	}

	if err := ds.Upload(m.ctx, file, remotePath, &p); err != nil {
		return fmt.Errorf("failed to upload to datastore: %w", err)
	}

	return nil
}

// RemoveAllCDROMs removes all existing CD-ROM devices from VM.
// Uses Reconfigure() directly like Python does (not wrapper methods).
func (m *Manager) RemoveAllCDROMs(vm *object.VirtualMachine) error {
	devices, err := getDevices(m.ctx, vm)
	if err != nil {
		return err
	}

	cdroms := getCDROMs(devices)
	if len(cdroms) == 0 {
		return nil
	}

	fmt.Printf("   Removing %d existing CD-ROM(s)...\n", len(cdroms))

	changes := make([]types.BaseVirtualDeviceConfigSpec, 0, len(cdroms))
	for _, cdrom := range cdroms {
		changes = append(changes, &types.VirtualDeviceConfigSpec{
			Operation: types.VirtualDeviceConfigSpecOperationRemove,
			Device:    cdrom,
		})
	}

	return reconfigureVM(m.ctx, vm, changes)
}

// MountISOs mounts Ubuntu + NoCloud ISOs to VM.
// ubuntuISO and nocloudISO are datastore paths (e.g., "[datastore1] ISO/ubuntu.iso")
//
// Matches Python implementation:
// 1. Remove ALL existing CD-ROMs
// 2. Mount Ubuntu ISO
// 3. Mount NoCloud ISO
func (m *Manager) MountISOs(vm *object.VirtualMachine, ubuntuISO, nocloudISO string) error {
	// STEP 1: Remove all existing CD-ROMs (matches Python)
	if err := m.RemoveAllCDROMs(vm); err != nil {
		return fmt.Errorf("failed to remove existing CD-ROMs: %w", err)
	}

	// STEP 2: Mount Ubuntu ISO (primary boot)
	if err := m.mountSingleISO(vm, ubuntuISO, "Ubuntu"); err != nil {
		return err
	}

	// STEP 3: Mount NoCloud ISO (config)
	if err := m.mountSingleISO(vm, nocloudISO, "NoCloud"); err != nil {
		return err
	}

	// STEP 4: Connect all CD-ROMs (Python does this explicitly!)
	if err := m.ConnectAllCDROMs(vm); err != nil {
		return fmt.Errorf("failed to connect CD-ROMs: %w", err)
	}

	fmt.Println("✅ Both ISOs mounted and connected successfully")
	return nil
}

// MountSingleISO mounts one ISO and ensures CD-ROMs are connected.
func (m *Manager) MountSingleISO(vm *object.VirtualMachine, isoPath, label string) error {
	if err := m.RemoveAllCDROMs(vm); err != nil {
		return fmt.Errorf("failed to remove existing CD-ROMs: %w", err)
	}
	if err := m.mountSingleISO(vm, isoPath, label); err != nil {
		return err
	}
	if err := m.ConnectAllCDROMs(vm); err != nil {
		return fmt.Errorf("failed to connect CD-ROMs: %w", err)
	}
	fmt.Printf("✅ %s ISO mounted and connected successfully\n", label)
	return nil
}

// ConnectAllCDROMs ensures all CD-ROM devices are connected.
// Matches Python's connect_all_cdroms() behavior.
func (m *Manager) ConnectAllCDROMs(vm *object.VirtualMachine) error {
	fmt.Println("   Ensuring all CD-ROMs are connected...")

	devices, err := getDevices(m.ctx, vm)
	if err != nil {
		return err
	}

	cdroms := getCDROMs(devices)
	if len(cdroms) == 0 {
		return nil
	}

	changes := make([]types.BaseVirtualDeviceConfigSpec, 0, len(cdroms))
	for _, cdrom := range cdroms {
		if cdrom.Connectable == nil {
			cdrom.Connectable = &types.VirtualDeviceConnectInfo{}
		}
		cdrom.Connectable.Connected = true
		cdrom.Connectable.StartConnected = true
		cdrom.Connectable.AllowGuestControl = true

		changes = append(changes, &types.VirtualDeviceConfigSpec{
			Operation: types.VirtualDeviceConfigSpecOperationEdit,
			Device:    cdrom,
		})
	}

	if err := reconfigureVM(m.ctx, vm, changes); err != nil {
		return err
	}

	fmt.Printf("   ✅ %d CD-ROM(s) connected\n", len(cdroms))
	return nil
}

// EnsureCDROMsConnectedAfterBoot verifies CD-ROMs are actually connected after VM boots.
// Matches Python's _ensure_cdroms_connected_after_boot() behavior exactly.
//
// VMware sometimes disconnects CD-ROMs after power-on even with startConnected=true.
// If disconnected: power-cycle VM to force reconnection.
func (m *Manager) EnsureCDROMsConnectedAfterBoot(vm *object.VirtualMachine) error {
	// Wait for VM hardware to initialize (matches Python: time.sleep(5))
	fmt.Printf("   Waiting %ds for VM hardware to initialize...\n", configs.Defaults.Timeouts.HardwareInitSeconds)
	time.Sleep(configs.Defaults.Timeouts.HardwareInit())

	// Refresh device list (matches Python: _safe_reload_vm)
	devices, err := getDevices(m.ctx, vm)
	if err != nil {
		return err
	}

	// Check actual connected status
	cdroms := getCDROMs(devices)
	if len(cdroms) == 0 {
		fmt.Println("   ⚠️  No CD-ROM devices found after boot!")
		return nil
	}

	disconnected := 0
	for i, cdrom := range cdroms {
		connected := cdrom.Connectable != nil && cdrom.Connectable.Connected
		fmt.Printf("   CD-ROM %d: connected=%v\n", i+1, connected)
		if !connected {
			disconnected++
		}
	}

	if disconnected == 0 {
		fmt.Printf("   ✅ All %d CD-ROM(s) connected after boot\n", len(cdroms))
		return nil
	}

	// Some disconnected - power-cycle to force reconnection (matches Python)
	fmt.Printf("   ⚠️  %d CD-ROM(s) disconnected - power-cycling to force reconnection...\n", disconnected)

	// Power off
	powerOffTask, err := vm.PowerOff(m.ctx)
	if err != nil {
		return fmt.Errorf("failed to power off VM: %w", err)
	}
	if err := powerOffTask.Wait(m.ctx); err != nil {
		return fmt.Errorf("failed to wait for power off: %w", err)
	}

	// Re-connect all CD-ROMs
	if err := m.ConnectAllCDROMs(vm); err != nil {
		return fmt.Errorf("failed to reconnect CD-ROMs: %w", err)
	}

	// Power on again
	powerOnTask, err := vm.PowerOn(m.ctx)
	if err != nil {
		return fmt.Errorf("failed to power on VM: %w", err)
	}
	if err := powerOnTask.Wait(m.ctx); err != nil {
		return fmt.Errorf("failed to wait for power on: %w", err)
	}

	// Wait and verify (matches Python: time.sleep(5) + reload)
	time.Sleep(configs.Defaults.Timeouts.HardwareInit())
	devices, err = getDevices(m.ctx, vm)
	if err != nil {
		return err
	}

	cdroms = getCDROMs(devices)
	allConnected := true
	for i, cdrom := range cdroms {
		connected := cdrom.Connectable != nil && cdrom.Connectable.Connected
		fmt.Printf("   CD-ROM %d: connected=%v\n", i+1, connected)
		if !connected {
			allConnected = false
		}
	}

	if allConnected {
		fmt.Printf("   ✅ All %d CD-ROM(s) connected after power-cycle\n", len(cdroms))
	} else {
		fmt.Println("   ⚠️  Some CD-ROMs still disconnected - continuing anyway")
	}

	return nil
}

// mountSingleISO mounts a single ISO to VM.
// Internal helper that matches Python's add_cdrom_with_iso behavior.
func (m *Manager) mountSingleISO(vm *object.VirtualMachine, isoPath, label string) error {
	// Refresh device list
	devices, err := getDevices(m.ctx, vm)
	if err != nil {
		return err
	}

	// Get or create AHCI/SATA controller
	controller, err := m.getOrCreateSATAController(vm, devices)
	if err != nil {
		return fmt.Errorf("failed to get SATA controller: %w", err)
	}

	// Get next available unit number
	unitNumber, err := m.getNextCDROMUnitNumber(vm, controller)
	if err != nil {
		return fmt.Errorf("failed to get unit number: %w", err)
	}

	controllerKey := controller.(types.BaseVirtualDevice).GetVirtualDevice().Key

	// Create CD-ROM device
	cdrom := &types.VirtualCdrom{
		VirtualDevice: types.VirtualDevice{
			Key:           -1, // vCenter assigns
			ControllerKey: controllerKey,
			UnitNumber:    &unitNumber,
			Backing: &types.VirtualCdromIsoBackingInfo{
				VirtualDeviceFileBackingInfo: types.VirtualDeviceFileBackingInfo{
					FileName: isoPath,
				},
			},
			Connectable: &types.VirtualDeviceConnectInfo{
				StartConnected:    true, // Connect at power on
				AllowGuestControl: true, // Allow guest control
				Connected:         true, // Immediately connected
			},
		},
	}

	// Add device using Reconfigure (like Python: ReconfigVM_Task)
	if err := reconfigureVM(m.ctx, vm, []types.BaseVirtualDeviceConfigSpec{
		&types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationAdd, Device: cdrom},
	}); err != nil {
		return fmt.Errorf("failed to add %s CD-ROM: %w", label, err)
	}

	fmt.Printf("   ✅ %s CD-ROM added (unit %d)\n", label, unitNumber)
	return nil
}

// getOrCreateSATAController finds existing SATA controller or creates one.
func (m *Manager) getOrCreateSATAController(vm *object.VirtualMachine, devices object.VirtualDeviceList) (types.BaseVirtualController, error) {
	// Look for existing SATA controller
	for _, device := range devices {
		if controller, ok := device.(types.BaseVirtualSATAController); ok {
			return controller.(types.BaseVirtualController), nil
		}
	}

	// Create AHCI (SATA) controller if not found
	fmt.Println("⚠️  No SATA controller found, creating AHCI controller...")

	ahci := &types.VirtualAHCIController{
		VirtualSATAController: types.VirtualSATAController{
			VirtualController: types.VirtualController{
				BusNumber: 0,
				VirtualDevice: types.VirtualDevice{
					Key: 15000, // Standard key for SATA controller
				},
			},
		},
	}

	if err := vm.AddDevice(m.ctx, ahci); err != nil {
		return nil, fmt.Errorf("failed to add SATA controller: %w", err)
	}

	// Refresh devices and return the new controller
	devices, err := vm.Device(m.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh devices after adding controller: %w", err)
	}

	for _, device := range devices {
		if controller, ok := device.(types.BaseVirtualSATAController); ok {
			return controller.(types.BaseVirtualController), nil
		}
	}

	return nil, fmt.Errorf("SATA controller not found after creation")
}

// getNextCDROMUnitNumber finds next available unit number on SATA controller.
func (m *Manager) getNextCDROMUnitNumber(vm *object.VirtualMachine, controller types.BaseVirtualController) (int32, error) {
	devices, err := getDevices(m.ctx, vm)
	if err != nil {
		return 0, err
	}

	controllerKey := controller.(types.BaseVirtualDevice).GetVirtualDevice().Key
	usedUnits := make(map[int32]bool)

	// Find all CD-ROMs on this controller
	for _, device := range devices {
		if cdrom, ok := device.(*types.VirtualCdrom); ok {
			if cdrom.ControllerKey == controllerKey {
				if cdrom.UnitNumber != nil {
					usedUnits[*cdrom.UnitNumber] = true
				}
			}
		}
	}

	// SATA supports unit numbers 0-29
	for unit := int32(0); unit < 30; unit++ {
		if !usedUnits[unit] {
			return unit, nil
		}
	}

	return 0, fmt.Errorf("no available unit numbers on SATA controller")
}

// CleanupNoCloudISO removes NoCloud ISO after first boot.
// DeleteFromDatastore deletes a file from vCenter datastore using govc.
// Used for cleanup of temporary ISOs (e.g., NoCloud ISO after installation).
func (m *Manager) DeleteFromDatastore(datastoreName, remotePath, vcenterHost, vcenterUser, vcenterPass string, insecure bool) error {
	if _, err := exec.LookPath("govc"); err != nil {
		return fmt.Errorf("govc not found - cannot delete from datastore: %w", err)
	}

	cmd := newGovcCmd(m.ctx, vcenterHost, vcenterUser, vcenterPass, insecure,
		"datastore.rm", "-ds", datastoreName, remotePath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if strings.Contains(msg, "was not found") {
			return nil
		}
		return fmt.Errorf("govc datastore.rm failed: %w\nOutput: %s", err, msg)
	}

	return nil
}

func (m *Manager) CleanupNoCloudISO(vm *object.VirtualMachine) error {
	devices, err := vm.Device(m.ctx)
	if err != nil {
		return fmt.Errorf("failed to get VM devices: %w", err)
	}

	// Find all CD-ROM devices
	cdroms := devices.SelectByType((*types.VirtualCdrom)(nil))

	// Remove all CD-ROMs (simple approach - removes both Ubuntu and NoCloud)
	// In production, you might want to keep Ubuntu ISO and only remove NoCloud
	for _, cdrom := range cdroms {
		if err := vm.RemoveDevice(m.ctx, false, cdrom); err != nil {
			return fmt.Errorf("failed to remove CD-ROM: %w", err)
		}
	}

	return nil
}
