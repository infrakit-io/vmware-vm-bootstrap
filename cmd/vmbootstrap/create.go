package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/bootstrap"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter"
	"gopkg.in/yaml.v3"
)

// vmFileConfig is the YAML structure for vm.*.sops.yaml (runtime fields).
type vmFileConfig struct {
	VM struct {
		Name              string      `yaml:"name"`
		Profile           string      `yaml:"profile,omitempty"`
		CPUs              int         `yaml:"cpus"`
		MemoryMB          int         `yaml:"memory_mb"`
		DiskSizeGB        int         `yaml:"disk_size_gb"`
		DataDiskSizeGB    int         `yaml:"data_disk_size_gb"`
		DataDiskMountPath string      `yaml:"data_disk_mount_path"`
		DataDiskDatastore string      `yaml:"data_disk_datastore,omitempty"`
		CleanupOnError    bool        `yaml:"cleanup_on_error,omitempty"`
		CPUHotAdd         *bool       `yaml:"cpu_hot_add,omitempty"`
		MemoryHotAdd      *bool       `yaml:"memory_hot_add,omitempty"`
		ExtraNICs         []vmNICFile `yaml:"extra_nics,omitempty"`
		Tags              []vmTagFile `yaml:"tags,omitempty"`
		SwapSizeGB        int         `yaml:"swap_size_gb"`
		Username          string      `yaml:"username"`
		SSHKeyPath        string      `yaml:"ssh_key_path"`
		SSHKey            string      `yaml:"ssh_key"`
		Password          string      `yaml:"password"`
		SSHPort           int         `yaml:"ssh_port"`
		AllowPasswordSSH  bool        `yaml:"allow_password_ssh"`
		IPAddress         string      `yaml:"ip_address"`
		Netmask           string      `yaml:"netmask"`
		Gateway           string      `yaml:"gateway"`
		DNS               string      `yaml:"dns"`
		DNS2              string      `yaml:"dns2"`
		Datastore         string      `yaml:"datastore"`
		NetworkName       string      `yaml:"network_name"`
		NetworkInterface  string      `yaml:"network_interface"`
		Folder            string      `yaml:"folder"`
		ResourcePool      string      `yaml:"resource_pool"`
		TimeoutMinutes    int         `yaml:"timeout_minutes"`
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

// vmNICFile is an additional static-IP NIC (no gateway) in vm.*.sops.yaml.
type vmNICFile struct {
	Network   string `yaml:"network"`
	IP        string `yaml:"ip"`
	Netmask   string `yaml:"netmask"`
	Connected *bool  `yaml:"connected,omitempty"` // default true when omitted
	// AllowGuestControl lets the guest connect/disconnect the NIC; default false (explicit opt-in).
	AllowGuestControl *bool `yaml:"allow_guest_control,omitempty"`
}

// vmTagFile is a vSphere tag (category + tag) in vm.*.sops.yaml.
type vmTagFile struct {
	Category string `yaml:"category"`
	Tag      string `yaml:"tag"`
}

// applyVMExtras copies the optional hardware/tagging fields of a VM file into cfg.
func applyVMExtras(cfg *bootstrap.VMConfig, dataDiskDatastore string, cpuHotAdd, memoryHotAdd *bool, nics []vmNICFile, tags []vmTagFile) {
	cfg.DataDiskDatastore = dataDiskDatastore
	cfg.CPUHotAdd = cpuHotAdd
	cfg.MemoryHotAdd = memoryHotAdd
	for _, n := range nics {
		connected := n.Connected == nil || *n.Connected
		cfg.ExtraNICs = append(cfg.ExtraNICs, bootstrap.NIC{Network: n.Network, IP: n.IP, Netmask: n.Netmask, Connected: connected, AllowGuestControl: n.AllowGuestControl != nil && *n.AllowGuestControl})
	}
	for _, t := range tags {
		cfg.Tags = append(cfg.Tags, bootstrap.Tag{Category: t.Category, Tag: t.Tag})
	}
}

func buildDNS(primary, secondary string) []string {
	if secondary != "" {
		return []string{primary, secondary}
	}
	return []string{primary}
}

// builtVM is the resolved bootstrap input derived from vm.*.sops.yaml and the vCenter config.
type builtVM struct {
	cfg        *bootstrap.VMConfig
	profile    string
	sshKey     string
	resultPath string
}

// buildVMConfig maps the decrypted VM file + vCenter config to a bootstrap.VMConfig.
// Shared by `run` (bootstrapVM) and `create`.
func buildVMConfig(vcCfg *vcenterFileConfig, vmFile *vmFileConfig, resultPath string) (*builtVM, error) {
	v := vmFile.VM
	profile := strings.TrimSpace(v.Profile)
	if profile == "" {
		profile = "ubuntu"
	}
	resultPath = resolveBootstrapResultPath(resultPath, v.Name)

	printConfigWarnings(profile, v.DataDiskSizeGB, v.DataDiskMountPath, v.SwapSizeGB, v.SSHKeyPath, v.SSHKey, v.Password, v.SSHPort)

	// Load SSH key (required only for ubuntu profile).
	var sshKey string
	if profile == "ubuntu" {
		if v.SSHKeyPath != "" {
			data, err := os.ReadFile(v.SSHKeyPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read SSH key %s: %w", v.SSHKeyPath, err)
			}
			sshKey = strings.TrimSpace(string(data))
		} else if v.SSHKey != "" {
			sshKey = v.SSHKey
		} else {
			return nil, fmt.Errorf("either vm.ssh_key or vm.ssh_key_path is required for ubuntu profile")
		}
	}

	cfg := &bootstrap.VMConfig{
		VCenterHost:     vcCfg.VCenter.Host,
		VCenterUsername: vcCfg.VCenter.Username,
		VCenterPassword: vcCfg.VCenter.Password,
		VCenterPort:     vcCfg.VCenter.Port,
		VCenterInsecure: vcCfg.VCenter.Insecure,

		Name:       v.Name,
		CPUs:       v.CPUs,
		MemoryMB:   v.MemoryMB,
		DiskSizeGB: v.DiskSizeGB,
		Profile:    profile,

		NetworkName:      v.NetworkName,
		NetworkInterface: v.NetworkInterface,
		IPAddress:        v.IPAddress,
		Netmask:          v.Netmask,
		Gateway:          v.Gateway,
		DNS:              buildDNS(v.DNS, v.DNS2),

		Datacenter:       vcCfg.VCenter.Datacenter,
		Folder:           v.Folder,
		ResourcePool:     v.ResourcePool,
		Datastore:        v.Datastore,
		ISODatastore:     vcCfg.VCenter.ISODatastore,
		ContentLibrary:   vcCfg.VCenter.ContentLibrary,
		ContentLibraryID: vcCfg.VCenter.ContentLibraryID,
	}
	if profile == "ubuntu" {
		cfg.Username = v.Username
		cfg.SSHPublicKeys = []string{sshKey}
		cfg.Password = v.Password
		cfg.AllowPasswordSSH = v.AllowPasswordSSH
	}
	cfg.Profiles.Ubuntu.Version = v.Profiles.Ubuntu.Version
	cfg.Profiles.Talos.Version = v.Profiles.Talos.Version
	cfg.Profiles.Talos.SchematicID = v.Profiles.Talos.SchematicID
	applyVMExtras(cfg, v.DataDiskDatastore, v.CPUHotAdd, v.MemoryHotAdd, v.ExtraNICs, v.Tags)
	cfg.CleanupOnError = v.CleanupOnError // opt-in: destroys the VM on failure

	if v.DataDiskSizeGB > 0 {
		size := v.DataDiskSizeGB
		cfg.DataDiskSizeGB = &size
		cfg.DataDiskMountPath = v.DataDiskMountPath
	}

	if v.SwapSizeGB > 0 {
		size := v.SwapSizeGB
		cfg.SwapSizeGB = &size
	}
	return &builtVM{cfg: cfg, profile: profile, sshKey: sshKey, resultPath: resultPath}, nil
}

// bootstrapVM decrypts vmConfigPath, merges with vcenter config, and runs bootstrap.
func bootstrapVM(vmConfigPath string, resultPath string) error {
	fmt.Printf("\033[1mBootstrap VM\033[0m — %s\n", vmConfigPath)
	fmt.Println(strings.Repeat("─", 50))
	fmt.Println()

	// Load vCenter config
	vcCfg, err := loadVCenterConfig(vcenterConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load vCenter config: %w", err)
	}

	// Decrypt VM config
	vmOut, err := sopsDecrypt(vmConfigPath)
	if err != nil {
		return err
	}

	var vmFile vmFileConfig
	if err := yaml.Unmarshal(vmOut, &vmFile); err != nil {
		return fmt.Errorf("failed to parse VM config: %w", err)
	}

	built, err := buildVMConfig(vcCfg, &vmFile, resultPath)
	if err != nil {
		return err
	}
	v := vmFile.VM
	cfg, profile, sshKey, resultPath := built.cfg, built.profile, built.sshKey, built.resultPath

	// If VM already exists, warn and offer options.
	if exists, err := vmExists(cfg); err == nil && exists {
		keyPath, cleanupKey, keyErr := prepareSSHKeyPath(v.SSHKeyPath, sshKey)
		if keyErr == nil {
			if cleanupKey != nil {
				defer cleanupKey()
			}
			if cfg.EffectiveProfile() == "ubuntu" {
				if currentVer, err := detectUbuntuVersion(cfg.Username, cfg.IPAddress, keyPath, v.SSHPort); err == nil {
					configUbuntu := cfg.EffectiveUbuntuVersion()
					if configUbuntu != "" && currentVer != "" && currentVer != configUbuntu {
						fmt.Printf("\n\033[33m⚠ OS version mismatch: VM=%s, config=%s\033[0m\n", currentVer, configUbuntu)
						fmt.Println("  Recommended: delete and recreate VM for version changes.")
					}
				}
			}
		}
		fmt.Printf("\n\033[33m⚠ VM already exists: %s\033[0m\n", cfg.Name)
		choice := sel.Select(
			[]string{
				"Cancel",
				"Delete existing VM and recreate",
			},
			"Cancel",
			"Select action:",
		)
		fmt.Println()
		if choice == "Cancel" {
			fmt.Println("  Cancelled.")
			return nil
		}
		if !readYesNoDanger("Delete existing VM now?") {
			fmt.Println("  Cancelled.")
			return nil
		}
		if err := cleanupVM(cfg, cfg.Name); err != nil {
			return err
		}
	}

	timeoutMinutes := v.TimeoutMinutes
	if timeoutMinutes == 0 {
		timeoutMinutes = 45
	}

	fmt.Printf("  VM name:    %s\n", cfg.Name)
	fmt.Printf("  vCenter:    %s\n", cfg.VCenterHost)
	fmt.Printf("  IP:         %s\n", cfg.IPAddress)
	fmt.Printf("  Datastore:  %s\n", cfg.Datastore)
	fmt.Printf("  Network:    %s\n", cfg.NetworkName)
	fmt.Println()
	fmt.Println("\033[33m⚠ This will create a real VM in vCenter. Press Ctrl+C to abort.\033[0m")
	for i := 5; i > 0; i-- {
		fmt.Printf("\r  Starting in %d seconds...  ", i)
		time.Sleep(1 * time.Second)
	}
	fmt.Print("\r\033[K")
	fmt.Println()

	logger := getLogger()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMinutes)*time.Minute+configs.Defaults.Timeouts.Download())
	defer cancel()

	// Take over Ctrl+C from the global handler so we can offer VM cleanup on interrupt.
	signal.Stop(mainSigCh)
	localSigCh := make(chan os.Signal, 1)
	signal.Notify(localSigCh, os.Interrupt)
	go func() {
		select {
		case <-localSigCh:
			fmt.Println("\n\n\033[33m⚠ Interrupted — stopping bootstrap...\033[0m")
			cancel()
		case <-ctx.Done():
		}
	}()

	var vm *bootstrap.VM
	if cfg.EffectiveProfile() == "talos" {
		vm, err = bootstrap.CreateTalosNodeFromOVA(ctx, cfg, logger)
	} else {
		vm, err = bootstrap.BootstrapWithLogger(ctx, cfg, logger)
	}

	// Restore global handler before any prompts.
	signal.Stop(localSigCh)
	signal.Notify(mainSigCh, os.Interrupt)

	if err != nil {
		if ctx.Err() == context.Canceled {
			offerVMCleanup(cfg)
			fmt.Println("\nCancelled.")
			os.Exit(0)
		}
		var keptErr *bootstrap.KeptVMError
		if errors.As(err, &keptErr) {
			fmt.Println()
			fmt.Println(keptVMMessage(keptErr))
		}
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	fmt.Println()
	fmt.Println("\033[32m✓ VM bootstrapped successfully!\033[0m")
	fmt.Printf("  Name:      %s\n", vm.Name)
	fmt.Printf("  IP:        %s\n", vm.IPAddress)
	fmt.Printf("  SSH ready: %v\n", vm.SSHReady)
	if profile == "ubuntu" && cfg.Username != "" {
		fmt.Printf("\n  Connect: \033[36mssh %s@%s\033[0m\n\n", cfg.Username, vm.IPAddress)
	}

	if resultPath != "" {
		if profile == "ubuntu" {
			if err := writeBootstrapResult(resultPath, cfg, v.SSHKeyPath, v.SSHPort, vm); err != nil {
				return err
			}
			fmt.Printf("  Bootstrap result saved: %s\n\n", resultPath)
		} else {
			fmt.Printf("  Bootstrap result skipped for profile %q (no SSH bootstrap data)\n\n", profile)
		}
	}

	return nil
}

func printConfigWarnings(profile string, dataDiskSizeGB int, dataDiskMountPath string, swapSizeGB int, sshKeyPath, sshKey, password string, sshPort int) {
	var warnings []string
	if dataDiskMountPath != "" && dataDiskSizeGB == 0 {
		warnings = append(warnings, "Data disk mount path is set but data disk size is 0 (no data disk will be created).")
	}
	if swapSizeGB == 0 {
		warnings = append(warnings, "Swap size is 0 (no swap will be created).")
	}
	if profile == "ubuntu" && sshPort != 0 && sshPort != 22 {
		warnings = append(warnings, fmt.Sprintf("SSH port is %d (default is 22).", sshPort))
	}
	if profile == "ubuntu" && sshKeyPath == "" && sshKey == "" && password == "" {
		warnings = append(warnings, "No SSH key or password set (SSH access may fail).")
	}
	if len(warnings) > 0 {
		fmt.Println("\033[33m⚠ Configuration warnings:\033[0m")
		for _, w := range warnings {
			fmt.Printf("  - %s\n", w)
		}
		fmt.Println()
	}
}

// offerVMCleanup prompts the user to delete a partially-created VM after Ctrl+C.
func offerVMCleanup(cfg *bootstrap.VMConfig) {
	fmt.Printf("\n\033[33m⚠  VM '%s' may be partially created in vCenter.\033[0m\n\n", cfg.Name)

	if !readYesNoDanger(fmt.Sprintf("Delete partial VM '%s' from vCenter?", cfg.Name)) {
		fmt.Printf("  VM left in vCenter. Delete manually if needed: %s\n", cfg.Name)
		return
	}

	fmt.Print("  Connecting to vCenter... ")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	vclient, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     cfg.VCenterHost,
		Username: cfg.VCenterUsername,
		Password: cfg.VCenterPassword,
		Port:     cfg.VCenterPort,
		Insecure: cfg.VCenterInsecure,
	})
	if err != nil {
		fmt.Printf("\033[31m✗ %v\033[0m\n  Delete manually in vCenter: %s\n", err, cfg.Name)
		return
	}
	defer func() {
		_ = vclient.Disconnect()
	}()
	fmt.Println("\033[32m✓\033[0m")

	vmObj, err := vclient.FindVM(cfg.Datacenter, cfg.Name)
	if err != nil || vmObj == nil {
		fmt.Println("  VM not found in vCenter (may not have been created yet).")
		return
	}

	fmt.Print("  Powering off... ")
	if task, err := vmObj.PowerOff(ctx); err == nil {
		task.Wait(ctx) //nolint:errcheck
	}
	fmt.Println("\033[32m✓\033[0m")

	fmt.Print("  Deleting VM... ")
	task, err := vmObj.Destroy(ctx)
	if err != nil {
		fmt.Printf("\033[31m✗ %v\033[0m\n  Delete manually in vCenter: %s\n", err, cfg.Name)
		return
	}
	if err := task.Wait(ctx); err != nil {
		fmt.Printf("\033[31m✗ %v\033[0m\n", err)
		return
	}
	fmt.Printf("\033[32m✓ VM '%s' deleted.\033[0m\n", cfg.Name)
}

// keptVMMessage describes the state of a VM that bootstrap kept after a failure.
func keptVMMessage(e *bootstrap.KeptVMError) string {
	if e.PoweredOff {
		return fmt.Sprintf("\033[33m⚠ VM kept (powered off): %s\033[0m", e.Name)
	}
	return fmt.Sprintf("\033[31m⚠ VM kept (power-off FAILED, still running): %s\033[0m", e.Name)
}
