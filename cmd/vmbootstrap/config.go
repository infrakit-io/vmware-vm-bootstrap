package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	survey "github.com/AlecAivazis/survey/v2"
	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter"
	"github.com/chzyer/readline"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// VMWizardOutput is the YAML structure for vm.*.sops.yaml files.
type VMWizardOutput struct {
	VM struct {
		Name              string `yaml:"name"`
		Profile           string `yaml:"profile,omitempty"`
		CPUs              int    `yaml:"cpus"`
		MemoryMB          int    `yaml:"memory_mb"`
		DiskSizeGB        int    `yaml:"disk_size_gb"`
		DataDiskSizeGB    int    `yaml:"data_disk_size_gb,omitempty"`
		DataDiskMountPath string `yaml:"data_disk_mount_path,omitempty"`
		SwapSizeGB        *int   `yaml:"swap_size_gb,omitempty"`
		Username          string `yaml:"username"`
		SSHKeyPath        string `yaml:"ssh_key_path,omitempty"`
		SSHKey            string `yaml:"ssh_key,omitempty"`
		Password          string `yaml:"password,omitempty"`
		AllowPasswordSSH  bool   `yaml:"allow_password_ssh,omitempty"`
		SSHPort           int    `yaml:"ssh_port,omitempty"`
		IPAddress         string `yaml:"ip_address"`
		Netmask           string `yaml:"netmask"`
		Gateway           string `yaml:"gateway"`
		DNS               string `yaml:"dns"`
		DNS2              string `yaml:"dns2,omitempty"`
		Datastore         string `yaml:"datastore,omitempty"`
		NetworkName       string `yaml:"network_name,omitempty"`
		NetworkInterface  string `yaml:"network_interface,omitempty"`
		Folder            string `yaml:"folder,omitempty"`
		ResourcePool      string `yaml:"resource_pool,omitempty"`
		TimeoutMinutes    int    `yaml:"timeout_minutes"`
		Profiles          struct {
			Ubuntu struct {
				Version string `yaml:"version,omitempty"`
			} `yaml:"ubuntu,omitempty"`
			Talos struct {
				Version     string `yaml:"version,omitempty"`
				SchematicID string `yaml:"schematic_id,omitempty"`
			} `yaml:"talos,omitempty"`
		} `yaml:"profiles,omitempty"`
	} `yaml:"vm"`
}

// vcenterFileConfig is the YAML structure for vcenter.sops.yaml.
type vcenterFileConfig struct {
	VCenter struct {
		Host             string `yaml:"host"`
		Username         string `yaml:"username"`
		Password         string `yaml:"password"`
		Datacenter       string `yaml:"datacenter"`
		ContentLibrary   string `yaml:"content_library,omitempty"`
		ContentLibraryID string `yaml:"content_library_id,omitempty"`
		ISODatastore     string `yaml:"iso_datastore"`
		Folder           string `yaml:"folder"`        // default VM folder for new VMs
		ResourcePool     string `yaml:"resource_pool"` // default resource pool for new VMs
		Network          string `yaml:"network"`       // default network for new VMs
		Port             int    `yaml:"port"`
		Insecure         bool   `yaml:"insecure"`
	} `yaml:"vcenter"`
}

// datastoreCandidate holds a scored datastore for recommendations.
type datastoreCandidate struct {
	Info         vcenter.DatastoreInfo
	FreeAfterGB  float64
	FreePctAfter float64
	LatencyMs    float64
	Score        float64
	Rationale    string
}

func listVCenterContentLibraries(vc *vcenterFileConfig, timeout time.Duration) ([]string, error) {
	if vc == nil {
		return nil, fmt.Errorf("vcenter config is nil")
	}
	v := vc.VCenter
	if strings.TrimSpace(v.Host) == "" || strings.TrimSpace(v.Username) == "" || strings.TrimSpace(v.Password) == "" {
		return nil, fmt.Errorf("missing vcenter host/username/password")
	}
	if _, err := exec.LookPath("govc"); err != nil {
		return nil, fmt.Errorf("govc not found")
	}
	url := strings.TrimSpace(v.Host)
	if !strings.Contains(url, "://") {
		port := v.Port
		if port == 0 {
			port = 443
		}
		url = fmt.Sprintf("https://%s:%d/sdk", strings.TrimSpace(v.Host), port)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "govc", "library.ls")
	cmd.Env = append(os.Environ(),
		"GOVC_URL="+url,
		"GOVC_USERNAME="+v.Username,
		"GOVC_PASSWORD="+v.Password,
		fmt.Sprintf("GOVC_INSECURE=%t", v.Insecure),
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	seen := map[string]struct{}{}
	libs := make([]string, 0, len(lines))
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		parts := strings.Split(s, "/")
		name := strings.TrimSpace(parts[len(parts)-1])
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		libs = append(libs, name)
	}
	sort.Strings(libs)
	return libs, nil
}

func selectContentLibraryInteractive(vc *vcenterFileConfig, currentName, currentID string) (string, string) {
	const (
		createOption = "Create new library..."
		idOption     = "Use explicit library ID (advanced)..."
		manualOption = "Manual input..."
		backOption   = "Back"
	)
	libs, err := listVCenterContentLibraries(vc, 20*time.Second)
	if err != nil {
		fmt.Printf("  ⚠ Could not list content libraries: %v\n", err)
		name := strings.TrimSpace(readLine("Talos content library", currentName))
		id := strings.TrimSpace(readLine("Talos content library ID (optional)", currentID))
		return name, id
	}

	options := append([]string{}, libs...)
	options = append(options, createOption, idOption, manualOption, backOption)

	defaultChoice := createOption
	if strings.TrimSpace(currentID) != "" {
		defaultChoice = idOption
	} else if strings.TrimSpace(currentName) != "" {
		for _, lib := range libs {
			if lib == currentName {
				defaultChoice = lib
				break
			}
		}
		if defaultChoice == createOption {
			defaultChoice = manualOption
		}
	} else if len(libs) > 0 {
		defaultChoice = libs[0]
	}

	choice := interactiveSelect(options, defaultChoice, "Talos content library:")
	switch choice {
	case backOption:
		return currentName, currentID
	case createOption:
		name := strings.TrimSpace(readLine("New content library name", strOrDefault(currentName, "talos-images")))
		if name == "" {
			name = strOrDefault(strings.TrimSpace(currentName), "talos-images")
		}
		return name, ""
	case idOption:
		id := strings.TrimSpace(readLine("Talos content library ID", currentID))
		name := strings.TrimSpace(readLine("Talos content library name (optional)", currentName))
		return name, id
	case manualOption:
		name := strings.TrimSpace(readLine("Talos content library", currentName))
		id := strings.TrimSpace(readLine("Talos content library ID (optional)", currentID))
		return name, id
	default:
		return strings.TrimSpace(choice), ""
	}
}

func runVMOSProfileStep(vm *VMWizardOutput) bool {
	if vm == nil {
		return false
	}
	fmt.Println("[1/5] OS Profile")
	currentProfile := strOrDefault(vm.VM.Profile, "ubuntu")
	vm.VM.Profile = selectOSProfile(currentProfile)
	switch vm.VM.Profile {
	case "talos":
		vm.VM.Profiles.Talos.Version = selectTalosVersion(vm.VM.Profiles.Talos.Version)
		for {
			vm.VM.Profiles.Talos.SchematicID = strings.TrimSpace(selectTalosSchematicID(vm.VM.Profiles.Talos.SchematicID))
			if wasPromptInterrupted() {
				fmt.Println("  Cancelled.")
				return true
			}
			if vm.VM.Profiles.Talos.SchematicID != "" {
				break
			}
			fmt.Println("  Talos schematic ID is required")
		}
	default:
		selectUbuntuVersion(&vm.VM.Profiles.Ubuntu.Version)
	}
	fmt.Println()
	return false
}

type vmPlacementStepDefaults struct {
	Folder       string
	ResourcePool string
	ShowWarnings bool
}

type vmSpecsStepOptions struct {
	NamePrompt                 string
	AutoNameFromOutputFile     string
	ExistingDataDiskAlwaysEdit bool
}

func runVMSpecsStep(vm *VMWizardOutput, opts vmSpecsStepOptions) {
	if vm == nil {
		return
	}
	fmt.Println("[2/5] VM Specs")

	if vm.VM.Name == "" && strings.TrimSpace(opts.AutoNameFromOutputFile) != "" {
		vm.VM.Name = strings.TrimSuffix(filepath.Base(opts.AutoNameFromOutputFile), ".sops.yaml")
		vm.VM.Name = strings.TrimPrefix(vm.VM.Name, "vm.")
	}
	vm.VM.Name = readLine(strOrDefault(opts.NamePrompt, "VM name"), vm.VM.Name)

	defaultCPU := intOrDefault(vm.VM.CPUs, 4)
	vm.VM.CPUs = readInt("CPU cores", defaultCPU, 1, 64)
	defaultRAM := 16
	if vm.VM.MemoryMB > 0 {
		defaultRAM = vm.VM.MemoryMB / 1024
	}
	ramGB := readInt("RAM (GB)", defaultRAM, 1, 512)
	vm.VM.MemoryMB = ramGB * 1024
	vm.VM.DiskSizeGB = readInt("OS disk (GB)", intOrDefault(vm.VM.DiskSizeGB, 50), 10, 2000)

	if opts.ExistingDataDiskAlwaysEdit && vm.VM.DataDiskSizeGB > 0 {
		vm.VM.DataDiskSizeGB = readInt("Data disk (GB)", vm.VM.DataDiskSizeGB, 10, 2000)
		vm.VM.DataDiskMountPath = readLine("Mount point", strOrDefault(vm.VM.DataDiskMountPath, "/data"))
	} else if readYesNo("Add separate data disk?", vm.VM.DataDiskSizeGB > 0) {
		defaultData := intOrDefault(vm.VM.DataDiskSizeGB, 500)
		vm.VM.DataDiskSizeGB = readInt("Data disk (GB)", defaultData, 10, 2000)
		vm.VM.DataDiskMountPath = readLine("Mount point", strOrDefault(vm.VM.DataDiskMountPath, "/data"))
	}
	defaultSwap := configs.Defaults.CloudInit.SwapSizeGB
	if vm.VM.SwapSizeGB != nil {
		defaultSwap = *vm.VM.SwapSizeGB
	}
	swap := readInt("Swap size (GB, 0 = no swap)", defaultSwap, 0, 64)
	vm.VM.SwapSizeGB = &swap
	fmt.Println()
}

func runVMPlacementStorageStep(vm *VMWizardOutput, resources *VCenterResources, defaults vmPlacementStepDefaults) {
	if vm == nil {
		return
	}
	fmt.Println("[3/5] Placement & Storage")
	vm.VM.Folder = pickVMFolder(resources, vm.VM.Folder, defaults.Folder)
	vm.VM.ResourcePool = pickVMResourcePool(resources, vm.VM.ResourcePool, defaults.ResourcePool)
	fmt.Println()

	requiredGB := float64(vm.VM.DiskSizeGB + vm.VM.DataDiskSizeGB)
	if resources == nil || resources.DatastoresErr != nil {
		if defaults.ShowWarnings && resources != nil && resources.DatastoresErr != nil {
			fmt.Printf("  ⚠ Could not list datastores: %v\n", resources.DatastoresErr)
		}
		vm.VM.Datastore = readLine("Datastore", vm.VM.Datastore)
	} else {
		vm.VM.Datastore = selectDatastore(resources.Datastores, requiredGB, vm.VM.Datastore)
	}
	fmt.Println()
}

type vmNetworkStepDefaults struct {
	NetworkName  string
	Gateway      string
	DNS          string
	ShowWarnings bool
}

type vmAccessStepOptions struct {
	SSHKeyPathDefaultWhenEmpty string
	UseChangePasswordFlow      bool
	SetPasswordDefault         bool
	AllowPasswordDefault       bool
}

type vmSummarySaveOptions struct {
	ConfirmPrompt   string
	CancelMessage   string
	SaveErrorPrefix string
	DraftPath       string
	SuccessLabel    string
	PostSave        func(string)
}

func runVMSummaryAndSave(targetPath string, vm VMWizardOutput, opts vmSummarySaveOptions) error {
	fmt.Println("Summary")
	printSummary(vm)

	if !readYesNo(strOrDefault(opts.ConfirmPrompt, "Save and re-encrypt?"), true) {
		fmt.Println(strOrDefault(opts.CancelMessage, "  Cancelled — no changes saved."))
		return nil
	}

	if err := saveAndEncrypt(targetPath, vm, opts.DraftPath); err != nil {
		if strings.TrimSpace(opts.SaveErrorPrefix) != "" {
			return fmt.Errorf("%s: %w", opts.SaveErrorPrefix, err)
		}
		return err
	}

	successLabel := strOrDefault(opts.SuccessLabel, targetPath)
	fmt.Printf("\n\033[32m✓ Saved and encrypted: %s\033[0m\n", successLabel)
	if opts.PostSave != nil {
		opts.PostSave(targetPath)
	}
	return nil
}

func runVMAccessStep(vm *VMWizardOutput, opts vmAccessStepOptions) {
	if vm == nil {
		return
	}
	fmt.Println("[5/5] Access / Node Options")
	if vm.VM.Profile == "talos" {
		fmt.Println("  Talos profile selected: SSH/bootstrap user settings are not required.")
		vm.VM.Username = ""
		vm.VM.SSHKeyPath = ""
		vm.VM.SSHKey = ""
		vm.VM.Password = ""
		vm.VM.AllowPasswordSSH = false
		fmt.Println()
		return
	}

	vm.VM.Username = readLine("Username", strOrDefault(vm.VM.Username, "sysadmin"))
	defaultKeyPath := vm.VM.SSHKeyPath
	if strings.TrimSpace(defaultKeyPath) == "" && strings.TrimSpace(opts.SSHKeyPathDefaultWhenEmpty) != "" {
		defaultKeyPath = opts.SSHKeyPathDefaultWhenEmpty
	}
	vm.VM.SSHKeyPath = readFilePath("SSH public key file", defaultKeyPath)
	vm.VM.SSHPort = readInt("SSH port", intOrDefault(vm.VM.SSHPort, 22), 1, 65535)

	if opts.UseChangePasswordFlow {
		pwStatus := "not set"
		if vm.VM.Password != "" {
			pwStatus = "set"
		}
		if readYesNo(fmt.Sprintf("Change password? (currently %s)", pwStatus), false) {
			vm.VM.Password = readPassword("New password (blank = remove)")
		}
		if vm.VM.Password != "" {
			vm.VM.AllowPasswordSSH = readYesNo("Allow SSH password authentication?", vm.VM.AllowPasswordSSH)
		} else {
			vm.VM.AllowPasswordSSH = false
		}
		fmt.Println()
		return
	}

	if readYesNo("Set password?", opts.SetPasswordDefault) {
		vm.VM.Password = readPassword("Password")
	}
	if vm.VM.Password != "" {
		vm.VM.AllowPasswordSSH = readYesNo("Allow SSH password authentication?", opts.AllowPasswordDefault)
	}
	fmt.Println()
}

func runVMNetworkStep(vm *VMWizardOutput, resources *VCenterResources, defaults vmNetworkStepDefaults) {
	if vm == nil {
		return
	}
	fmt.Println("[4/5] Network")
	if resources == nil || resources.NetworksErr != nil || len(resources.Networks) == 0 {
		if defaults.ShowWarnings && resources != nil && resources.NetworksErr != nil {
			fmt.Printf("  ⚠ Could not list networks: %v\n", resources.NetworksErr)
		}
		vm.VM.NetworkName = readLine("Network name", strOrDefault(vm.VM.NetworkName, defaults.NetworkName))
	} else {
		vm.VM.NetworkName = interactiveSelect(vcenterNetworkLeafNames(resources.Networks), strOrDefault(vm.VM.NetworkName, defaults.NetworkName), "Network:")
	}
	vm.VM.NetworkInterface = readLine("Guest NIC name", strOrDefault(vm.VM.NetworkInterface, configs.Defaults.Network.Interface))
	vm.VM.IPAddress = readIPLine("IP address", vm.VM.IPAddress)
	vm.VM.Netmask = readIPLine("Netmask", strOrDefault(vm.VM.Netmask, "255.255.255.0"))
	vm.VM.Gateway = readIPLine("Gateway", strOrDefault(vm.VM.Gateway, defaults.Gateway))
	vm.VM.DNS = readLine("DNS", strOrDefault(vm.VM.DNS, defaults.DNS))
	vm.VM.DNS2 = readLine("Secondary DNS (optional, Enter to skip)", vm.VM.DNS2)
	fmt.Println()
}

func pickVMFolder(resources *VCenterResources, current, fallback string) string {
	current = strOrDefault(current, fallback)
	if resources == nil || resources.FoldersErr != nil {
		return readLine("VM folder", current)
	}
	return selectFolder(resources.Folders, current, "VM folder:")
}

func pickVMResourcePool(resources *VCenterResources, current, fallback string) string {
	current = strOrDefault(current, fallback)
	if resources == nil || resources.PoolsErr != nil {
		return readLine("Resource pool", current)
	}
	return selectResourcePool(resources.Pools, current, "Resource pool:")
}

// ─── Edit existing configs ───────────────────────────────────────────────────

func editVCenterConfig(path string) error {
	fmt.Printf("\nEdit: %s\n", filepath.Base(path))
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	data, err := sopsDecrypt(path)
	if err != nil {
		return err
	}

	var cfg vcenterFileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}

	// Connect to vCenter upfront to fetch datastores + folders for pickers.
	fmt.Print("  Connecting to vCenter... ")
	cat, catErr := fetchVCenterCatalog(&cfg, 30*time.Second)
	if catErr != nil {
		fmt.Printf("\033[33m⚠ %v\033[0m (will use manual input)\n", catErr)
	} else {
		fmt.Printf("\033[32m✓\033[0m  (%d datastores, %d networks, %d folders, %d pools)\n",
			len(cat.Datastores), len(cat.Networks), len(cat.Folders), len(cat.Pools))
	}
	fmt.Println()

	v := &cfg.VCenter
	v.Host = readLine("Host", v.Host)
	v.Username = readLine("Username", v.Username)
	if pw := readPassword("Password (blank = keep current)"); pw != "" {
		v.Password = pw
	}
	v.Datacenter = readLine("Datacenter", v.Datacenter)
	v.ContentLibrary, v.ContentLibraryID = selectContentLibraryInteractive(&cfg, v.ContentLibrary, v.ContentLibraryID)

	readyCat := catalogIfReady(cat, catErr)
	v.ISODatastore = pickDatastoreFromCatalogWithPrompt(readyCat, v.ISODatastore, "ISO datastore (where Ubuntu + seed ISOs are stored)", "ISO datastore (where Ubuntu + seed ISOs are stored):")
	v.Folder = pickFolderFromCatalogWithPrompt(readyCat, v.Folder, "Default VM folder", "Default VM folder:")
	v.ResourcePool = pickResourcePoolFromCatalogWithPrompt(readyCat, v.ResourcePool, "Default resource pool", "Default resource pool:")
	v.Network = pickNetworkFromCatalogWithPrompt(readyCat, v.Network, "Default network", "Default network:")

	fmt.Println()
	if !readYesNo("Save and re-encrypt?", true) {
		fmt.Println("  Cancelled — no changes saved.")
		return nil
	}

	if err := saveAndEncrypt(path, cfg, ""); err != nil {
		return err
	}

	fmt.Printf("\n\033[32m✓ Saved and encrypted: %s\033[0m\n", filepath.Base(path))
	return nil
}

func createVCenterConfig(path string) error {
	return createVCenterConfigWithSeed(path, vcenterFileConfig{}, "")
}

func createVCenterConfigWithDraft(path, draftPath string) error {
	return createVCenterConfigWithSeed(path, vcenterFileConfig{}, draftPath)
}

func createVCenterConfigWithSeed(path string, seed vcenterFileConfig, draftPath string) error {
	fmt.Printf("\nCreate: %s\n", filepath.Base(path))
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	cfg := seed
	session := NewWizardSession(path, draftPath, &cfg, func() bool {
		v := cfg.VCenter
		return strings.TrimSpace(v.Host) == "" &&
			strings.TrimSpace(v.Username) == "" &&
			strings.TrimSpace(v.Datacenter) == ""
	})
	if loaded, err := session.LoadDraft(); err == nil && loaded {
		fmt.Printf("\033[33m⚠ Resuming draft: %s\033[0m\n\n", filepath.Base(draftPath))
	}
	v := &cfg.VCenter

	session.Start()
	defer session.Stop()

	v.Host = readLine("Host", v.Host)
	v.Username = readLine("Username", v.Username)
	if v.Password != "" {
		if readYesNo("Use saved password?", true) {
			// keep existing
		} else {
			v.Password = readPassword("Password")
		}
	} else {
		v.Password = readPassword("Password")
	}
	v.Datacenter = readLine("Datacenter", v.Datacenter)
	v.ContentLibrary, v.ContentLibraryID = selectContentLibraryInteractive(&cfg, v.ContentLibrary, v.ContentLibraryID)
	v.Port = readInt("Port", intOrDefault(v.Port, configs.Defaults.VCenter.Port), 1, 65535)
	v.Insecure = readYesNo("Skip TLS verification? (not recommended)", v.Insecure)

	// Try to connect and fetch resource pickers.
	fmt.Print("  Connecting to vCenter... ")
	cat, catErr := fetchVCenterCatalog(&cfg, 30*time.Second)
	if catErr != nil {
		fmt.Printf("\033[33m⚠ %v\033[0m (will use manual input)\n", catErr)
	} else {
		fmt.Printf("\033[32m✓\033[0m  (%d datastores, %d networks, %d folders, %d pools)\n",
			len(cat.Datastores), len(cat.Networks), len(cat.Folders), len(cat.Pools))
	}
	fmt.Println()

	readyCat := catalogIfReady(cat, catErr)
	v.ISODatastore = pickDatastoreFromCatalogWithPrompt(readyCat, v.ISODatastore, "ISO datastore (where Ubuntu + seed ISOs are stored)", "ISO datastore (where Ubuntu + seed ISOs are stored):")
	v.Folder = pickFolderFromCatalogWithPrompt(readyCat, v.Folder, "Default VM folder", "Default VM folder:")
	v.ResourcePool = pickResourcePoolFromCatalogWithPrompt(readyCat, v.ResourcePool, "Default resource pool", "Default resource pool:")
	v.Network = pickNetworkFromCatalogWithPrompt(readyCat, v.Network, "Default network", "Default network:")

	fmt.Println()
	if !readYesNo("Save and encrypt?", true) {
		fmt.Println("  Cancelled — no changes saved.")
		return nil
	}

	if err := saveAndEncrypt(path, cfg, draftPath); err != nil {
		return err
	}
	_ = session.Finalize()

	fmt.Printf("\n\033[32m✓ Saved and encrypted: %s\033[0m\n", filepath.Base(path))
	return nil
}

func editVMConfig(path string) error {
	fmt.Printf("\nEdit: %s\n", filepath.Base(path))
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	data, err := sopsDecrypt(path)
	if err != nil {
		return err
	}

	var cfg VMWizardOutput
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}

	// Connect to vCenter upfront to fetch pickers (same pattern as runCreateWizard).
	fmt.Print("  Connecting to vCenter... ")
	vcCfg, vcfgErr := loadVCenterConfig(vcenterConfigFile)

	var resources *VCenterResources

	if vcfgErr != nil {
		fmt.Printf("\033[33m⚠ %v\033[0m (pickers unavailable)\n", vcfgErr)
	} else {
		var vcErr error
		resources, vcErr = fetchVCenterResources(vcCfg, 60*time.Second)
		if vcErr != nil {
			fmt.Printf("\033[33m⚠ %v\033[0m (pickers unavailable)\n", vcErr)
		} else {
			fmt.Printf("\033[32m✓\033[0m  (%d datastores, %d networks, %d folders, %d pools)\n",
				len(resources.Datastores), len(resources.Networks), len(resources.Folders), len(resources.Pools))
		}
	}
	fmt.Println()

	// === [1] OS Profile ===
	if runVMOSProfileStep(&cfg) {
		return nil
	}

	// === [2] VM Specs ===
	runVMSpecsStep(&cfg, vmSpecsStepOptions{
		NamePrompt:                 "VM name",
		ExistingDataDiskAlwaysEdit: true,
	})

	// === [3] Placement & Storage ===
	runVMPlacementStorageStep(&cfg, resources, vmPlacementStepDefaults{})

	// === [4] Network ===
	runVMNetworkStep(&cfg, resources, vmNetworkStepDefaults{})

	// === [5] Access / Node Options ===
	runVMAccessStep(&cfg, vmAccessStepOptions{
		UseChangePasswordFlow: true,
	})

	targetPath := path
	oldPath := path
	removeOldAfterSave := false
	if suggested, ok := suggestedVMConfigPathFromName(cfg.VM.Name); ok {
		if filepath.Clean(suggested) != filepath.Clean(path) {
			if readYesNo(fmt.Sprintf("Rename config file to %s to match VM name?", suggested), true) {
				targetPath = suggested
				removeOldAfterSave = true
			}
		}
	}

	if err := runVMSummaryAndSave(targetPath, cfg, vmSummarySaveOptions{
		ConfirmPrompt: "Save and re-encrypt?",
		CancelMessage: "  Cancelled — no changes saved.",
		SuccessLabel:  filepath.Base(targetPath),
		PostSave: func(_ string) {
			if removeOldAfterSave && filepath.Clean(oldPath) != filepath.Clean(targetPath) {
				if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
					fmt.Printf("  \033[33m⚠ Could not remove old config:\033[0m %s (%v)\n", oldPath, err)
				} else {
					fmt.Printf("  \033[32m✓ Renamed config:\033[0m %s → %s\n", filepath.Base(oldPath), filepath.Base(targetPath))
				}
			}
		},
	}); err != nil {
		return err
	}
	return nil
}

func suggestedVMConfigPathFromName(name string) (string, bool) {
	slug := sanitizeVMConfigSlug(name)
	if slug == "" {
		return "", false
	}
	return filepath.Join("configs", fmt.Sprintf("vm.%s.sops.yaml", slug)), true
}

func sanitizeVMConfigSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		case r == ' ' || r == '/':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	return out
}

// saveAndEncrypt marshals v to YAML, writes to path, and encrypts in-place with SOPS.
// If the file already exists, it is backed up and restored on failure.
func saveAndEncrypt(path string, v interface{}, draftPath string) error {
	backup, backupExisted := func() ([]byte, bool) {
		data, err := os.ReadFile(path)
		return data, err == nil
	}()

	plaintext, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal YAML: %w", err)
	}

	if err := sopsEncrypt(path, plaintext); err != nil {
		pathOverride := draftPath
		if pathOverride != "" {
			_ = os.MkdirAll("tmp", 0700)
			_ = os.WriteFile(pathOverride, plaintext, 0600)
		}
		dp := pathOverride
		if dp == "" {
			dp, _ = writeDraft(path, plaintext)
		}
		if dp != "" {
			return &userError{
				msg:  err.Error(),
				hint: fmt.Sprintf("Progress saved (plaintext): %s (delete after use)", dp),
			}
		}
		if backupExisted {
			if restoreErr := os.WriteFile(path, backup, 0600); restoreErr != nil {
				return fmt.Errorf("encrypt failed (%v) and restore failed: %w", err, restoreErr)
			}
		} else {
			if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
				return fmt.Errorf("encrypt failed (%v) and cleanup failed: %w", err, rmErr)
			}
		}
		return err
	}
	if err := cleanupDrafts(path); err != nil {
		return fmt.Errorf("cleanup drafts: %w", err)
	}
	return nil
}

func writeDraft(targetPath string, plaintext []byte) (string, error) {
	if err := os.MkdirAll("tmp", 0700); err != nil {
		return "", err
	}
	base := filepath.Base(targetPath)
	ts := time.Now().Format("20060102-150405")
	draftPath := filepath.Join("tmp", fmt.Sprintf("%s.draft.%s.yaml", base, ts))
	if err := os.WriteFile(draftPath, plaintext, 0600); err != nil {
		return "", err
	}
	return draftPath, nil
}

func cleanupDrafts(targetPath string) error {
	base := filepath.Base(targetPath)
	pattern := filepath.Join("tmp", fmt.Sprintf("%s.draft.*.yaml", base))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, p := range matches {
		if rmErr := os.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
			return rmErr
		}
	}
	return nil
}

// startDraftInterruptHandler saves plaintext drafts on Ctrl+C and asks whether to keep them.
func startDraftInterruptHandler(targetPath, draftPath string, dataFn func() ([]byte, bool)) func() {
	localSigCh := make(chan os.Signal, 1)
	signal.Stop(mainSigCh)
	signal.Notify(localSigCh, os.Interrupt)
	go func() {
		<-localSigCh
		if data, ok := dataFn(); ok {
			path := draftPath
			if path == "" {
				path, _ = writeDraft(targetPath, data)
			} else {
				_ = os.MkdirAll("tmp", 0700)
				_ = os.WriteFile(path, data, 0600)
			}
			if path != "" {
				fmt.Printf("\n\033[33m⚠ Interrupted\033[0m\n")
				fmt.Printf("  Draft saved (plaintext): %s (delete after use)\n", path)
			}
		}
		fmt.Println("\nCancelled.")
		restoreTTYOnExit()
		os.Exit(0)
	}()
	return func() {
		signal.Stop(localSigCh)
		signal.Notify(mainSigCh, os.Interrupt)
	}
}

// ─── Create new VM wizard ─────────────────────────────────────────────────────

func runCreateWizard() error {
	return runCreateWizardWithSeed("", "")
}

func runCreateWizardWithDraft(outputFile, draftPath string) error {
	return runCreateWizardWithSeed(outputFile, draftPath)
}

func runCreateWizardWithSeed(outputFile, draftPath string) error {
	if _, err := os.Stat(vcenterConfigFile); os.IsNotExist(err) {
		return &userError{
			msg:  "vCenter config not found",
			hint: "Run: make config → Create vcenter.sops.yaml",
		}
	}
	fmt.Printf("\n\033[1mCreate new VM\033[0m\n")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Println()

	var out VMWizardOutput
	session := NewWizardSession(outputFile, draftPath, &out, nil)
	if loaded, err := session.LoadDraft(); err == nil && loaded {
		fmt.Printf("\033[33m⚠ Resuming draft: %s\033[0m\n\n", filepath.Base(draftPath))
	}

	// OS selector must be the first wizard question for new VM configs.
	if runVMOSProfileStep(&out) {
		return nil
	}

	if outputFile == "" {
		// Config file slug
		var slug string
		for {
			slug = strings.TrimSpace(readLine("Config name (e.g. 'vm1' → configs/vm.vm1.sops.yaml)", ""))
			if wasPromptInterrupted() {
				fmt.Println("  Cancelled.")
				return nil
			}
			if slug != "" {
				break
			}
			fmt.Println("  Config name is required")
		}
		outputFile = fmt.Sprintf("configs/vm.%s.sops.yaml", slug)
	}

	if _, err := os.Stat(outputFile); err == nil {
		if !readYesNoDanger(fmt.Sprintf("%s already exists. Overwrite?", outputFile)) {
			fmt.Println("  Cancelled.")
			return nil
		}
	}

	session.TargetPath = outputFile
	session.Start()
	defer session.Stop()

	// Connect to vCenter and fetch all resources upfront (before wizard questions).
	// This avoids context deadline issues caused by the user taking time to answer prompts.
	fmt.Print("  Connecting to vCenter... ")
	vcCfg, err := loadVCenterConfig(vcenterConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load vCenter config: %w", err)
	}

	resources, err := fetchVCenterResources(vcCfg, 60*time.Second)
	if err != nil {
		return fmt.Errorf("vCenter connection failed: %w", err)
	}
	fmt.Printf("\033[32m✓\033[0m  (%d datastores, %d networks, %d folders, %d pools)\n",
		len(resources.Datastores), len(resources.Networks), len(resources.Folders), len(resources.Pools))
	fmt.Println()

	// === [2] VM Specs ===
	fmt.Println()
	runVMSpecsStep(&out, vmSpecsStepOptions{
		NamePrompt:             "VM name in vCenter",
		AutoNameFromOutputFile: outputFile,
	})

	// === [3] Placement & Storage ===
	runVMPlacementStorageStep(&out, resources, vmPlacementStepDefaults{
		Folder:       vcCfg.VCenter.Folder,
		ResourcePool: vcCfg.VCenter.ResourcePool,
		ShowWarnings: true,
	})

	// === [4] Network (cached from upfront fetch) ===
	runVMNetworkStep(&out, resources, vmNetworkStepDefaults{
		NetworkName:  vcCfg.VCenter.Network,
		Gateway:      autoGateway(out.VM.IPAddress),
		DNS:          autoFirstDNS(out.VM.Gateway),
		ShowWarnings: true,
	})

	// === [5] Access / Node Options ===
	runVMAccessStep(&out, vmAccessStepOptions{
		SSHKeyPathDefaultWhenEmpty: os.ExpandEnv("$HOME/.ssh/id_ed25519.pub"),
		SetPasswordDefault:         true,
		AllowPasswordDefault:       false,
	})

	out.VM.TimeoutMinutes = 45

	if err := runVMSummaryAndSave(outputFile, out, vmSummarySaveOptions{
		ConfirmPrompt:   fmt.Sprintf("Save to %s?", outputFile),
		CancelMessage:   "\033[33mCancelled — configuration not saved.\033[0m",
		SaveErrorPrefix: "failed to save config",
		DraftPath:       draftPath,
		SuccessLabel:    outputFile,
		PostSave: func(target string) {
			fmt.Printf("\n  To bootstrap this VM:\n")
			fmt.Printf("    make run VM=%s\n\n", target)
		},
	}); err != nil {
		return err
	}
	_ = session.Finalize()
	return nil
}

// ─── Datastore scoring (matches Python resource_selector.py) ─────────────────

func scoreDatastores(datastores []vcenter.DatastoreInfo, requiredGB float64) []datastoreCandidate {
	const minFreePct = 20.0
	const weightSpace = 0.6
	const weightLatency = 0.4

	var eligible []datastoreCandidate
	for _, ds := range datastores {
		if ds.Type != "SSD" || !ds.Accessible || ds.CapacityGB == 0 {
			continue
		}
		freeAfter := ds.FreeSpaceGB - requiredGB
		freePctAfter := freeAfter / ds.CapacityGB * 100
		if freePctAfter < minFreePct {
			continue
		}
		eligible = append(eligible, datastoreCandidate{
			Info:         ds,
			FreeAfterGB:  math.Round(freeAfter*100) / 100,
			FreePctAfter: math.Round(freePctAfter*100) / 100,
			LatencyMs:    2.0, // SSD heuristic
		})
	}

	for i := range eligible {
		ds := eligible[i].Info
		freePct := ds.FreeSpaceGB / ds.CapacityGB * 100
		spaceScore := math.Min(freePct, 100)
		latencyScore := math.Max(0, math.Min(100, (10-eligible[i].LatencyMs)*11.11))
		eligible[i].Score = math.Round((spaceScore*weightSpace+latencyScore*weightLatency)*100) / 100
		eligible[i].Rationale = buildRationale(eligible[i])
	}

	sort.Slice(eligible, func(i, j int) bool { return eligible[i].Score > eligible[j].Score })
	if len(eligible) > 3 {
		eligible = eligible[:3]
	}
	return eligible
}

func buildRationale(c datastoreCandidate) string {
	var parts []string
	switch {
	case c.FreePctAfter > 50:
		parts = append(parts, fmt.Sprintf("plenty of free space (%.1f%% after allocation)", c.FreePctAfter))
	case c.FreePctAfter > 30:
		parts = append(parts, fmt.Sprintf("adequate free space (%.1f%% after allocation)", c.FreePctAfter))
	default:
		parts = append(parts, fmt.Sprintf("sufficient free space (%.1f%% after allocation)", c.FreePctAfter))
	}
	switch {
	case c.LatencyMs < 3:
		parts = append(parts, fmt.Sprintf("excellent performance (%.1fms)", c.LatencyMs))
	case c.LatencyMs < 5:
		parts = append(parts, fmt.Sprintf("good performance (%.1fms)", c.LatencyMs))
	default:
		parts = append(parts, fmt.Sprintf("acceptable performance (%.1fms)", c.LatencyMs))
	}
	return strings.Join(parts, ", ")
}

// selectDatastore shows a unified survey.Select with top-scored datastores marked ★.
// The score and free-space info are embedded directly in the option labels so
// recommendations and the selection list are shown only once (no duplicate display).
func selectDatastore(datastores []vcenter.DatastoreInfo, requiredGB float64, defaultDS string) string {
	recs := scoreDatastores(datastores, requiredGB)
	recSet := make(map[string]datastoreCandidate, len(recs))
	for _, r := range recs {
		recSet[r.Info.Name] = r
	}

	var opts []string
	var dsNames []string

	// Top-scored datastores first, with ★ prefix and score.
	for _, r := range recs {
		label := fmt.Sprintf("★ %s  (score: %.0f · %.0f/%.0f GB free)",
			r.Info.Name, r.Score, r.Info.FreeSpaceGB, r.Info.CapacityGB)
		opts = append(opts, label)
		dsNames = append(dsNames, r.Info.Name)
	}

	// Remaining SSD datastores (not in top-scored list).
	for _, ds := range datastores {
		if _, isRec := recSet[ds.Name]; isRec {
			continue
		}
		if ds.Type == "SSD" && ds.Accessible {
			label := fmt.Sprintf("  %s  (%.0f/%.0f GB free)", ds.Name, ds.FreeSpaceGB, ds.CapacityGB)
			opts = append(opts, label)
			dsNames = append(dsNames, ds.Name)
		}
	}

	// Fallback: any accessible datastore if no SSD found.
	if len(opts) == 0 {
		for _, ds := range datastores {
			if ds.Accessible {
				opts = append(opts, fmt.Sprintf("  %s  (%.0f GB free)", ds.Name, ds.FreeSpaceGB))
				dsNames = append(dsNames, ds.Name)
			}
		}
	}

	if len(opts) == 0 {
		return defaultDS
	}

	// Pre-select the current datastore if it appears in the list.
	defaultOpt := opts[0]
	for i, name := range dsNames {
		if name == defaultDS {
			defaultOpt = opts[i]
			break
		}
	}

	var choice string
	surveySelect(&survey.Select{Message: "Select datastore:", Options: opts, Default: defaultOpt}, &choice)
	for i, opt := range opts {
		if opt == choice {
			return dsNames[i]
		}
	}
	return defaultDS
}

// selectFolder shows all VM folders with survey.Select. Returns empty string for root.
func selectFolder(folders []vcenter.FolderInfo, defaultFolder, message string) string {
	const rootLabel = "  / (root vm folder)"
	opts := []string{rootLabel}
	names := []string{""}

	defaultOpt := rootLabel
	for _, f := range folders {
		opts = append(opts, f.Name)
		names = append(names, f.Name)
		if f.Name == defaultFolder {
			defaultOpt = f.Name
		}
	}

	var choice string
	surveySelect(&survey.Select{
		Message: message,
		Options: opts,
		Default: defaultOpt,
	}, &choice)
	for i, opt := range opts {
		if opt == choice {
			return names[i]
		}
	}
	return defaultFolder
}

// selectResourcePool shows all resource pools with survey.Select.
func selectResourcePool(pools []vcenter.ResourcePoolInfo, defaultPool, message string) string {
	if len(pools) == 0 {
		return defaultPool
	}

	var opts []string
	defaultOpt := pools[0].Name
	for _, p := range pools {
		opts = append(opts, p.Name)
		if p.Name == defaultPool {
			defaultOpt = p.Name
		}
	}

	var choice string
	surveySelect(&survey.Select{
		Message: message,
		Options: opts,
		Default: defaultOpt,
	}, &choice)
	return choice
}

// interactiveSelect renders a navigable list in raw terminal mode.
// ↑/↓ arrows move the selection; Enter confirms.
// Does NOT send cursor-position queries (no \033[6n), so it leaves no CPR bytes
// in stdin — immune to the issue that affects consecutive survey.Select calls.
func interactiveSelect(items []string, defaultItem, message string) string {
	if len(items) == 0 {
		return defaultItem
	}

	sel := 0
	for i, item := range items {
		if item == defaultItem {
			sel = i
			break
		}
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Fallback: numbered list with readline input.
		return selectFromList(items, defaultItem, message)
	}

	const maxVisible = 10
	nVis := len(items)
	if nVis > maxVisible {
		nVis = maxVisible
	}
	offset := 0

	clamp := func() {
		if sel < offset {
			offset = sel
		} else if sel >= offset+nVis {
			offset = sel - nVis + 1
		}
	}

	// Lines rendered: 1 header + nVis items + 1 footer = nVis+2
	total := nVis + 2

	draw := func(initial bool) {
		if !initial {
			fmt.Printf("\033[%dA", total) // move cursor back to top of block
		}
		clamp()
		fmt.Printf("\r  \033[1m%s\033[0m\033[K\r\n", message)
		for i := offset; i < offset+nVis; i++ {
			if i == sel {
				fmt.Printf("\r  \033[36m❯ %s\033[0m\033[K\r\n", items[i])
			} else {
				fmt.Printf("\r    %s\033[K\r\n", items[i])
			}
		}
		if len(items) > nVis {
			fmt.Printf("\r  \033[2m%d/%d · ↑↓ arrows · Enter\033[0m\033[K\r\n", sel+1, len(items))
		} else {
			fmt.Printf("\r  \033[2m↑↓ arrows · Enter\033[0m\033[K\r\n")
		}
	}

	draw(true)

	buf := make([]byte, 8)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			break
		}
		if n == 1 {
			switch buf[0] {
			case '\r', '\n': // Enter
				result := items[sel]
				_ = term.Restore(fd, oldState)
				stdinReader.Reset(os.Stdin)
				fmt.Printf("\033[%dA", total)
				fmt.Printf("\r  \033[32m❯\033[0m %s \033[36m%s\033[0m\r\n", message, result)
				fmt.Printf("\033[J") // clear everything below
				return result
			case 3: // Ctrl+C
				_ = term.Restore(fd, oldState)
				stdinReader.Reset(os.Stdin)
				fmt.Printf("\r\n")
				return defaultItem
			}
		} else if n >= 3 && buf[0] == '\033' && buf[1] == '[' {
			switch buf[2] {
			case 'A': // up
				if sel > 0 {
					sel--
				} else {
					sel = len(items) - 1
				}
				draw(false)
			case 'B': // down
				if sel < len(items)-1 {
					sel++
				} else {
					sel = 0
				}
				draw(false)
			}
		}
	}

	_ = term.Restore(fd, oldState)
	stdinReader.Reset(os.Stdin)
	return items[sel]
}

// selectFromList is the fallback when raw mode is unavailable.
// Shows a numbered list and reads the selection via readline.
func selectFromList(items []string, defaultItem, label string) string {
	if len(items) == 0 {
		return defaultItem
	}
	fmt.Printf("  %s\n", label)
	defaultIdx := 1
	for i, item := range items {
		marker := "  "
		if item == defaultItem {
			marker = "» "
			defaultIdx = i + 1
		}
		fmt.Printf("   %s%d. %s\n", marker, i+1, item)
	}

	prompt := fmt.Sprintf("  Select [1-%d] [\033[36m%d\033[0m]: ", len(items), defaultIdx)
	rl, err := readline.NewEx(&readline.Config{Prompt: prompt})
	if err != nil {
		n := readInt(fmt.Sprintf("Select [1-%d]", len(items)), defaultIdx, 1, len(items))
		return items[n-1]
	}
	defer func() {
		_ = rl.Close()
		stdinReader.Reset(os.Stdin)
	}()

	for {
		line, err := rl.Readline()
		if err != nil {
			return items[defaultIdx-1]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return items[defaultIdx-1]
		}
		v, err := strconv.Atoi(line)
		if err != nil || v < 1 || v > len(items) {
			fmt.Printf("  Must be a number between 1 and %d\n", len(items))
			continue
		}
		return items[v-1]
	}
}

// selectISODatastore shows all datastores with ★ for the top HDD candidates
// (ISOs are written once and rarely accessed, so HDD is the right choice).
func selectISODatastore(datastores []vcenter.DatastoreInfo, defaultDS string) string {
	// Collect and rank HDD datastores by free space.
	var hddByFree []vcenter.DatastoreInfo
	for _, ds := range datastores {
		if ds.Type == "HDD" && ds.Accessible {
			hddByFree = append(hddByFree, ds)
		}
	}
	sort.Slice(hddByFree, func(i, j int) bool {
		return hddByFree[i].FreeSpaceGB > hddByFree[j].FreeSpaceGB
	})
	topN := 2
	if len(hddByFree) < topN {
		topN = len(hddByFree)
	}
	topHDD := make(map[string]bool, topN)
	for _, ds := range hddByFree[:topN] {
		topHDD[ds.Name] = true
	}

	var opts []string
	var dsNames []string

	// Top HDD datastores first (★).
	for _, ds := range hddByFree[:topN] {
		label := fmt.Sprintf("★ %s  [HDD] (%.0f/%.0f GB free)",
			ds.Name, ds.FreeSpaceGB, ds.CapacityGB)
		opts = append(opts, label)
		dsNames = append(dsNames, ds.Name)
	}

	// Remaining datastores (lower-ranked HDD + SSD).
	for _, ds := range datastores {
		if !ds.Accessible || topHDD[ds.Name] {
			continue
		}
		label := fmt.Sprintf("  %s  [%s] (%.0f/%.0f GB free)",
			ds.Name, ds.Type, ds.FreeSpaceGB, ds.CapacityGB)
		opts = append(opts, label)
		dsNames = append(dsNames, ds.Name)
	}

	if len(opts) == 0 {
		return defaultDS
	}

	var choice string
	surveySelect(&survey.Select{Message: "Select ISO datastore:", Options: opts}, &choice)
	for i, opt := range opts {
		if opt == choice {
			return dsNames[i]
		}
	}
	return defaultDS
}

func buildUbuntuOptions() []string {
	releases := configs.UbuntuReleases.Releases
	var vers []string
	for ver := range releases {
		vers = append(vers, ver)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(vers)))
	names := map[string]string{
		"24.04": "24.04 LTS (Noble)",
		"22.04": "22.04 LTS (Jammy)",
		"20.04": "20.04 LTS (Focal)",
	}
	var out []string
	for _, v := range vers {
		if n, ok := names[v]; ok {
			out = append(out, n)
		} else {
			out = append(out, v)
		}
	}
	return out
}

func selectOSProfile(defaultProfile string) string {
	options := []string{"ubuntu", "talos"}
	var profileChoice string
	surveySelect(&survey.Select{
		Message: "OS profile:",
		Options: options,
		Default: defaultProfile,
	}, &profileChoice)
	return profileChoice
}

func selectUbuntuVersion(target *string) {
	ubuntuOptions := buildUbuntuOptions()
	defaultUbuntu := ubuntuOptions[0]
	for _, opt := range ubuntuOptions {
		if strings.HasPrefix(opt, *target) {
			defaultUbuntu = opt
			break
		}
	}
	var ubuntuChoice string
	surveySelect(&survey.Select{
		Message: "Ubuntu version:",
		Options: ubuntuOptions,
		Default: defaultUbuntu,
	}, &ubuntuChoice)
	*target = strings.Split(ubuntuChoice, " ")[0]
}

func selectTalosVersion(current string) string {
	defaultVersion := strings.TrimSpace(current)
	if defaultVersion == "" {
		defaultVersion = strings.TrimSpace(configs.Defaults.Talos.DefaultVersion)
	}
	if defaultVersion == "" && len(configs.TalosReleases.Versions) > 0 {
		defaultVersion = strings.TrimSpace(configs.TalosReleases.Versions[0])
	}
	if defaultVersion != "" && !strings.HasPrefix(defaultVersion, "v") {
		defaultVersion = "v" + defaultVersion
	}

	var versions []string
	seen := map[string]struct{}{}
	for _, v := range configs.TalosReleases.Versions {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !strings.HasPrefix(v, "v") {
			v = "v" + v
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		versions = append(versions, v)
	}
	if len(versions) == 0 {
		return readLine("Talos version (e.g. v1.12.0)", defaultVersion)
	}

	options := append([]string{}, versions...)
	options = append(options, "Custom version...")
	defaultOption := options[0]
	for _, v := range versions {
		if v == defaultVersion {
			defaultOption = v
			break
		}
	}
	var choice string
	surveySelect(&survey.Select{
		Message: "Talos version:",
		Options: options,
		Default: defaultOption,
	}, &choice)
	if choice == "Custom version..." {
		return readLine("Talos version (e.g. v1.12.0)", defaultVersion)
	}
	return choice
}

func autoGateway(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) == 4 {
		return strings.Join(parts[:3], ".") + ".1"
	}
	return ""
}

func autoFirstDNS(gateway string) string {
	return gateway
}

// ─── Output helpers ───────────────────────────────────────────────────────────

func printSummary(out VMWizardOutput) {
	v := out.VM
	fmt.Printf("  %-20s %s\n", "Name:", v.Name)
	fmt.Printf("  %-20s %d cores / %d MB RAM\n", "Specs:", v.CPUs, v.MemoryMB)
	fmt.Printf("  %-20s OS: %d GB", "Disks:", v.DiskSizeGB)
	if v.DataDiskSizeGB > 0 {
		fmt.Printf("  Data: %d GB", v.DataDiskSizeGB)
	}
	fmt.Println()
	if v.SwapSizeGB != nil {
		fmt.Printf("  %-20s %d GB\n", "Swap:", *v.SwapSizeGB)
	}
	fmt.Printf("  %-20s %s\n", "OS profile:", strOrDefault(v.Profile, "ubuntu"))
	switch v.Profile {
	case "talos":
		fmt.Printf("  %-20s %s\n", "Talos:", v.Profiles.Talos.Version)
		if v.Profiles.Talos.SchematicID != "" {
			fmt.Printf("  %-20s %s\n", "Schematic ID:", v.Profiles.Talos.SchematicID)
		}
	default:
		fmt.Printf("  %-20s %s\n", "Ubuntu:", v.Profiles.Ubuntu.Version)
	}
	fmt.Printf("  %-20s %s\n", "Datastore:", v.Datastore)
	fmt.Printf("  %-20s %s\n", "Network:", v.NetworkName)
	if v.NetworkInterface != "" {
		fmt.Printf("  %-20s %s\n", "NIC name:", v.NetworkInterface)
	}
	if v.Folder != "" {
		fmt.Printf("  %-20s %s\n", "Folder:", v.Folder)
	}
	if v.ResourcePool != "" {
		fmt.Printf("  %-20s %s\n", "Resource pool:", v.ResourcePool)
	}
	dns := v.DNS
	if v.DNS2 != "" {
		dns += ", " + v.DNS2
	}
	fmt.Printf("  %-20s %s / %s / gw %s / dns %s\n", "Network config:", v.IPAddress, v.Netmask, v.Gateway, dns)
	fmt.Printf("  %-20s %s\n", "User:", v.Username)
	if v.SSHKeyPath != "" {
		fmt.Printf("  %-20s %s\n", "SSH key:", v.SSHKeyPath)
	}
	if v.SSHPort > 0 {
		fmt.Printf("  %-20s %d\n", "SSH port:", v.SSHPort)
	}
	if v.Password != "" {
		fmt.Printf("  %-20s (set)\n", "Password:")
		fmt.Printf("  %-20s %v\n", "SSH password auth:", v.AllowPasswordSSH)
	}
	fmt.Println()
}

// ─── vCenter config loading ───────────────────────────────────────────────────

func loadVCenterConfig(path string) (*vcenterFileConfig, error) {
	data, err := sopsDecrypt(path)
	if err != nil {
		return nil, err
	}
	var cfg vcenterFileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return &cfg, nil
}

// ─── File path input with Tab completion ─────────────────────────────────────

// filePathCompleter implements readline.AutoCompleter for filesystem paths.
// Handles ~ expansion for display and supports Tab completion of file/dir names.
type filePathCompleter struct{}

func (c *filePathCompleter) Do(line []rune, pos int) (newLine [][]rune, length int) {
	typed := string(line[:pos])

	// Expand ~ for filesystem lookup only (keep original form for display).
	expanded := typed
	if strings.HasPrefix(typed, "~/") {
		home, _ := os.UserHomeDir()
		expanded = home + typed[1:]
	} else if typed == "~" {
		home, _ := os.UserHomeDir()
		expanded = home
	}

	var dir, partial string
	if strings.HasSuffix(expanded, "/") || expanded == "" {
		dir = expanded
		if dir == "" {
			dir = "."
		}
		partial = ""
	} else {
		dir = filepath.Dir(expanded)
		partial = filepath.Base(expanded)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0
	}

	var matches [][]rune
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, partial) {
			continue
		}
		// readline appends the returned string at the cursor — return only the
		// suffix after what the user already typed, not the full name.
		suffix := name[len(partial):]
		if e.IsDir() {
			suffix += "/"
		}
		matches = append(matches, []rune(suffix))
	}
	return matches, len([]rune(partial))
}

// readFilePath reads a file path with Tab completion support.
// Falls back to plain readLine if readline cannot be initialized.
func readFilePath(field, current string) string {
	prompt := fmt.Sprintf("  %s: ", field)
	if current != "" {
		prompt = fmt.Sprintf("  %s [\033[36m%s\033[0m]: ", field, current)
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:       prompt,
		AutoComplete: &filePathCompleter{},
	})
	if err != nil {
		return readLine(field, current) // fallback to plain input
	}
	defer func() {
		_ = rl.Close()
		stdinReader.Reset(os.Stdin) // resync bufio reader after readline
	}()

	line, err := rl.Readline()
	if err != nil {
		return current // Ctrl+C or EOF → keep current
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return current
	}
	// Expand ~ in result so callers receive a usable path.
	if strings.HasPrefix(line, "~/") {
		home, _ := os.UserHomeDir()
		line = home + line[1:]
	}
	return line
}

// ─── Plain I/O helpers (no survey — avoids terminal cursor-position queries) ──

// stdinReader is the single shared buffered reader over os.Stdin.
// One instance is required — multiple buffered readers over the same fd
// would each buffer ahead and consume each other's input.
var stdinReader = bufio.NewReader(os.Stdin)
var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var caretEscapeRE = regexp.MustCompile(`\^\[\[[0-9;?]*[ -/]*[@-~]`)
var promptInterrupted atomic.Bool

// readLine prints "  Field [current]: " and reads a line.
// Returns current if the user presses Enter without typing anything.
func readLine(field, current string) string {
	prompt := ""
	if current != "" {
		prompt = fmt.Sprintf("  %s [\033[36m%s\033[0m]: ", field, current)
	} else {
		prompt = fmt.Sprintf("  %s: ", field)
	}
	s := readPromptLine(prompt)
	if s == "" {
		return current
	}
	return s
}

// readIPLine reads a line and validates it as an IPv4 address.
func readIPLine(field, current string) string {
	for {
		s := readLine(field, current)
		if isValidIP(s) {
			return s
		}
		fmt.Println("  Invalid IP address — use dotted decimal (e.g. 192.168.1.10)")
	}
}

// readPassword reads a password without echoing. Returns empty string if blank.
func readPassword(field string) string {
	fmt.Printf("  %s: ", field)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return ""
	}
	return string(pw)
}

// readInt prints the prompt and reads a validated integer.
func readInt(field string, current, min, max int) int {
	for {
		s := readPromptLine(fmt.Sprintf("  %s [\033[36m%d\033[0m]: ", field, current))
		if s == "" {
			return current
		}
		v, err := parseInt(s)
		if err != nil || v < min || v > max {
			fmt.Printf("  Must be a number between %d and %d\n", min, max)
			continue
		}
		return v
	}
}

// readYesNo prints "  msg [Y/n]: " and returns true for y/yes, false for n/no.
func readYesNo(msg string, defaultYes bool) bool {
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}
	for {
		s := strings.ToLower(readPromptLine(fmt.Sprintf("  %s %s: ", msg, hint)))
		if s == "" {
			return defaultYes
		}
		if s == "y" || s == "yes" {
			return true
		}
		if s == "n" || s == "no" {
			return false
		}
		fmt.Println("  Enter y or n")
	}
}

// readYesNoDanger is for destructive actions.
// It highlights the prompt in red and defaults to No.
func readYesNoDanger(msg string) bool {
	return readYesNo("\033[31m"+msg+"\033[0m", false)
}

func readPromptLine(prompt string) string {
	promptInterrupted.Store(false)
	rl, err := readline.NewEx(&readline.Config{Prompt: prompt})
	if err == nil {
		cleanup := func() {
			_ = rl.Close()
			stdinReader.Reset(os.Stdin)
		}
		line, err := rl.Readline()
		if err == nil {
			cleanup()
			return strings.TrimSpace(line)
		}
		if errors.Is(err, readline.ErrInterrupt) {
			// Restore terminal before signal handler (it may os.Exit immediately).
			cleanup()
			restoreTTYOnExit()
			promptInterrupted.Store(true)
			if p, findErr := os.FindProcess(os.Getpid()); findErr == nil {
				_ = p.Signal(os.Interrupt)
			}
			return ""
		}
		cleanup()
		return ""
	}

	fmt.Print(prompt)
	line, _ := stdinReader.ReadString('\n')
	if strings.ContainsRune(line, '\x03') {
		promptInterrupted.Store(true)
		if p, findErr := os.FindProcess(os.Getpid()); findErr == nil {
			_ = p.Signal(os.Interrupt)
		}
	}
	return sanitizeConsoleInput(line)
}

func wasPromptInterrupted() bool {
	if !promptInterrupted.Load() {
		return false
	}
	promptInterrupted.Store(false)
	return true
}

func sanitizeConsoleInput(raw string) string {
	raw = ansiEscapeRE.ReplaceAllString(raw, "")
	raw = caretEscapeRE.ReplaceAllString(raw, "")
	raw = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)
	return strings.TrimSpace(raw)
}

// surveySelect wraps survey.AskOne for a Select prompt and calls drainStdin()
// afterward to discard any CPR responses (\033[row;colR) that the terminal
// may have queued in stdin in response to survey's \033[6n cursor queries.
// Without this drain, those responses appear as garbage in subsequent readLine calls.
func surveySelect(q *survey.Select, response *string) {
	_ = survey.AskOne(q, response)
	drainStdin()
}

// ─── Small helpers ────────────────────────────────────────────────────────────

func intOrDefault(v, d int) int {
	if v != 0 {
		return v
	}
	return d
}

func strOrDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

func parseInt(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}

func isValidIP(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || v > 255 {
			return false
		}
	}
	return true
}
