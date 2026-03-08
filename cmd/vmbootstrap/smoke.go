package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/bootstrap"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter"
	"gopkg.in/yaml.v3"
)

// smokeVM bootstraps a VM and runs a minimal validation, then optionally cleans up.
func smokeVM(vmConfigPath string, cleanup bool) error {
	fmt.Printf("\033[1mSmoke Test\033[0m — %s\n", vmConfigPath)
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

	v := vmFile.VM
	profile := strings.TrimSpace(v.Profile)
	if profile == "" {
		profile = "ubuntu"
	}
	if profile != "ubuntu" {
		return &userError{
			msg:  fmt.Sprintf("smoke is supported only for ubuntu profile (got %q)", profile),
			hint: "Use 'make vm-deploy' with ubuntu profile for smoke tests.",
		}
	}

	// Load SSH key
	sshKey, err := loadSSHKey(v.SSHKeyPath, v.SSHKey)
	if err != nil {
		return err
	}
	sshKeyPath, cleanupKey, err := prepareSSHKeyPath(v.SSHKeyPath, sshKey)
	if err != nil {
		return err
	}
	if cleanupKey != nil {
		defer cleanupKey()
	}

	cfg := &bootstrap.VMConfig{
		VCenterHost:     vcCfg.VCenter.Host,
		VCenterUsername: vcCfg.VCenter.Username,
		VCenterPassword: vcCfg.VCenter.Password,
		VCenterPort:     vcCfg.VCenter.Port,
		VCenterInsecure: vcCfg.VCenter.Insecure,

		Name:               v.Name,
		Profile:            v.Profile,
		CPUs:               v.CPUs,
		MemoryMB:           v.MemoryMB,
		DiskSizeGB:         v.DiskSizeGB,
		Username:           v.Username,
		SSHPublicKeys:      []string{sshKey},
		Password:           v.Password,
		AllowPasswordSSH:   v.AllowPasswordSSH,
		SkipSSHVerify:      true,
		SkipCleanupOnError: true,

		NetworkName: v.NetworkName,
		IPAddress:   v.IPAddress,
		Netmask:     v.Netmask,
		Gateway:     v.Gateway,
		DNS:         buildDNS(v.DNS, v.DNS2),

		Datacenter:       vcCfg.VCenter.Datacenter,
		Folder:           v.Folder,
		ResourcePool:     v.ResourcePool,
		Datastore:        v.Datastore,
		ISODatastore:     vcCfg.VCenter.ISODatastore,
		ContentLibrary:   vcCfg.VCenter.ContentLibrary,
		ContentLibraryID: vcCfg.VCenter.ContentLibraryID,
	}
	if cfg.Profile == "" {
		cfg.Profile = "ubuntu"
	}
	cfg.Profiles.Ubuntu.Version = v.Profiles.Ubuntu.Version
	cfg.Profiles.Talos.Version = v.Profiles.Talos.Version
	cfg.Profiles.Talos.SchematicID = v.Profiles.Talos.SchematicID

	if v.DataDiskSizeGB > 0 {
		size := v.DataDiskSizeGB
		cfg.DataDiskSizeGB = &size
		cfg.DataDiskMountPath = v.DataDiskMountPath
	}
	if v.SwapSizeGB > 0 {
		size := v.SwapSizeGB
		cfg.SwapSizeGB = &size
	}

	// If VM already exists, ask whether to reuse or recreate.
	if exists, err := vmExists(cfg); err == nil && exists {
		fmt.Printf("\n\033[33m⚠ VM already exists: %s\033[0m\n", cfg.Name)
		choice := interactiveSelect(
			[]string{
				"Reuse existing VM",
				"Create new VM (delete existing)",
				"Cancel",
			},
			"Reuse existing VM",
			"Select action:",
		)
		fmt.Println()
		switch choice {
		case "Reuse existing VM":
			return runSmokeChecksOnly(cfg, v.DataDiskMountPath, v.SwapSizeGB, sshKeyPath, v.SSHPort, cleanup, 0)
		case "Create new VM (delete existing)":
			if !readYesNoDanger("Delete existing VM before creating new?") {
				fmt.Println("  Cancelled.")
				return nil
			}
			if err := cleanupVM(cfg, cfg.Name); err != nil {
				return err
			}
		default:
			fmt.Println("  Cancelled.")
			return nil
		}
	}

	// Bootstrap
	logger := getLogger()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(45)*time.Minute)
	defer cancel()

	// Handle Ctrl+C locally for cleanup
	signal.Stop(mainSigCh)
	localSigCh := make(chan os.Signal, 1)
	signal.Notify(localSigCh, os.Interrupt)
	interrupted := false
	go func() {
		select {
		case <-localSigCh:
			fmt.Println("\n\n\033[33m⚠ Interrupted — stopping smoke test...\033[0m")
			interrupted = true
			cancel()
		case <-ctx.Done():
		}
	}()

	vm, err := bootstrap.BootstrapWithLogger(ctx, cfg, logger)

	// Restore global handler
	signal.Stop(localSigCh)
	signal.Notify(mainSigCh, os.Interrupt)

	if err != nil {
		if ctx.Err() == context.Canceled {
			if interrupted {
				if !cleanup {
					cleanup = readYesNoDanger("Cleanup (delete VM)?")
				}
				if cleanup {
					fmt.Println()
					fmt.Println("Cleanup")
					_ = cleanupVM(cfg, cfg.Name)
				}
			}
			fmt.Println("\nCancelled.")
			os.Exit(0)
		}
		fmt.Printf("\n\033[31m✗ Bootstrap failed: %v\033[0m\n", err)
		fmt.Printf("  VM may still be running: %s\n", cfg.Name)
		fmt.Printf("  Inspect with: \033[36mssh %s@%s\033[0m\n\n", cfg.Username, cfg.IPAddress)
		if !cleanup {
			cleanup = readYesNoDanger("Cleanup (delete VM)?")
		}
		if cleanup {
			fmt.Println()
			fmt.Println("Cleanup")
			return cleanupVM(cfg, cfg.Name)
		}
		return nil
	}

	fmt.Println()
	fmt.Println("\033[32m✓ VM bootstrapped successfully!\033[0m")
	fmt.Printf("  Name:      %s\n", vm.Name)
	fmt.Printf("  IP:        %s\n", vm.IPAddress)
	fmt.Printf("  SSH ready: %v\n", vm.SSHReady)

	return runSmokeChecksOnly(cfg, v.DataDiskMountPath, v.SwapSizeGB, sshKeyPath, v.SSHPort, cleanup, 60)
}

func loadSSHKey(path, raw string) (string, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read SSH key %s: %w", path, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	if raw == "" {
		return "", fmt.Errorf("either vm.ssh_key or vm.ssh_key_path is required")
	}
	return raw, nil
}

func prepareSSHKeyPath(path, raw string) (string, func(), error) {
	if path != "" {
		// If a public key is provided, try matching private key next to it.
		if strings.HasSuffix(path, ".pub") {
			priv := strings.TrimSuffix(path, ".pub")
			if _, err := os.Stat(priv); err == nil {
				return priv, nil, nil
			}
		}
		return path, nil, nil
	}
	if raw == "" {
		return "", nil, fmt.Errorf("missing SSH key")
	}
	if err := os.MkdirAll("tmp", 0700); err != nil {
		return "", nil, err
	}
	tmpPath := filepath.Join("tmp", fmt.Sprintf("smoke-ssh-key-%d", time.Now().UnixNano()))
	if err := os.WriteFile(tmpPath, []byte(raw+"\n"), 0600); err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(tmpPath) }
	return tmpPath, cleanup, nil
}

func cleanupVM(cfg *bootstrap.VMConfig, name string) error {
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
		fmt.Printf("\033[31m✗ %v\033[0m\n", err)
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = vclient.Disconnect() }()
	fmt.Println("\033[32m✓\033[0m")

	vmObj, err := vclient.FindVM(cfg.Datacenter, name)
	if err != nil || vmObj == nil {
		fmt.Println("  VM not found in vCenter (may already be deleted).")
		return nil
	}

	fmt.Print("  Powering off... ")
	if task, err := vmObj.PowerOff(ctx); err == nil {
		_ = task.Wait(ctx)
	}
	fmt.Println("\033[32m✓\033[0m")

	fmt.Print("  Deleting VM... ")
	task, err := vmObj.Destroy(ctx)
	if err != nil {
		fmt.Printf("\033[31m✗ %v\033[0m\n", err)
		return fmt.Errorf("destroy: %w", err)
	}
	if err := task.Wait(ctx); err != nil {
		fmt.Printf("\033[31m✗ %v\033[0m\n", err)
		return fmt.Errorf("destroy wait: %w", err)
	}
	fmt.Println("\033[32m✓ VM deleted.\033[0m")
	return nil
}

func vmExists(cfg *bootstrap.VMConfig) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	vclient, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     cfg.VCenterHost,
		Username: cfg.VCenterUsername,
		Password: cfg.VCenterPassword,
		Port:     cfg.VCenterPort,
		Insecure: cfg.VCenterInsecure,
	})
	if err != nil {
		return false, err
	}
	defer func() { _ = vclient.Disconnect() }()
	vmObj, err := vclient.FindVM(cfg.Datacenter, cfg.Name)
	if err != nil {
		return false, err
	}
	return vmObj != nil, nil
}

func runSmokeChecksOnly(cfg *bootstrap.VMConfig, dataMount string, swapSizeGB int, keyPath string, sshPort int, cleanup bool, settleSeconds int) error {
	if settleSeconds > 0 {
		fmt.Printf("  Waiting %ds for services to settle...\n", settleSeconds)
		time.Sleep(time.Duration(settleSeconds) * time.Second)
	}

	fmt.Println("  Running SSH checks...")
	if err := smokeSSHChecks(cfg.Username, cfg.IPAddress, keyPath, sshPort, dataMount, swapSizeGB); err != nil {
		fmt.Printf("\033[31m✗ Smoke checks failed: %v\033[0m\n", err)
	} else {
		fmt.Println("\033[32m✓ Smoke checks passed\033[0m")
	}

	if !cleanup {
		cleanup = readYesNoDanger("Cleanup (delete VM)?")
	}
	if cleanup {
		fmt.Println()
		fmt.Println("Cleanup")
		return cleanupVM(cfg, cfg.Name)
	}

	fmt.Printf("\n  Connect: \033[36mssh %s@%s\033[0m\n\n", cfg.Username, cfg.IPAddress)
	return nil
}
