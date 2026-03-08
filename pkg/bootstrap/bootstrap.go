package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Bibi40k/vmware-vm-bootstrap/configs"
	"github.com/Bibi40k/vmware-vm-bootstrap/internal/utils"
	"github.com/Bibi40k/vmware-vm-bootstrap/pkg/iso"
	"github.com/Bibi40k/vmware-vm-bootstrap/pkg/profile"
	talosprofile "github.com/Bibi40k/vmware-vm-bootstrap/pkg/profile/talos"
	ubuntuprofile "github.com/Bibi40k/vmware-vm-bootstrap/pkg/profile/ubuntu"
	"github.com/Bibi40k/vmware-vm-bootstrap/pkg/vcenter"
	"github.com/Bibi40k/vmware-vm-bootstrap/pkg/vm"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// defaultLogger is used if no logger is provided.
var defaultLogger = slog.Default()

// sshVerifier is used by VM.Verify to allow tests to stub SSH checks.
var sshVerifier = verifySSHAccess

// bootstrapper holds injectable service factories.
// Production code uses defaultBootstrapper(); tests inject mocks.
type bootstrapper struct {
	connectVCenter func(ctx context.Context, cfg *VMConfig) (vcenter.ClientInterface, error)
	newVMCreator   func(ctx context.Context) vm.CreatorInterface
	newISOManager  func(ctx context.Context) iso.ManagerInterface
	resolveProfile func(profileName string) (profile.Provisioner, error)
	waitInstall    func(ctx context.Context, vmObj *object.VirtualMachine, cfg *VMConfig, logger *slog.Logger) error
	checkSSH       func(ctx context.Context, ipAddr string) error
}

// defaultBootstrapper returns a bootstrapper with real production implementations.
func defaultBootstrapper() *bootstrapper {
	return &bootstrapper{
		connectVCenter: func(ctx context.Context, cfg *VMConfig) (vcenter.ClientInterface, error) {
			return vcenter.NewClient(ctx, &vcenter.Config{
				Host:     cfg.VCenterHost,
				Username: cfg.VCenterUsername,
				Password: cfg.VCenterPassword,
				Port:     cfg.VCenterPort,
				Insecure: cfg.VCenterInsecure,
			})
		},
		newVMCreator: func(ctx context.Context) vm.CreatorInterface {
			return vm.NewCreator(ctx)
		},
		newISOManager: func(ctx context.Context) iso.ManagerInterface {
			return iso.NewManager(ctx)
		},
		resolveProfile: func(profileName string) (profile.Provisioner, error) {
			if profileName == "" || profileName == "ubuntu" {
				return ubuntuprofile.New(), nil
			}
			if profileName == "talos" {
				return talosprofile.New(), nil
			}
			return nil, fmt.Errorf("unsupported profile %q", profileName)
		},
		waitInstall: waitForInstallation,
		checkSSH:    verifySSHAccess,
	}
}

// Bootstrap creates and configures a complete VM in vCenter.
// Returns VM object ONLY after:
// - VM created in vCenter
// - Profile provisioning completed
// - Optional SSH verification completed (Ubuntu profile)
func Bootstrap(ctx context.Context, cfg *VMConfig) (*VM, error) {
	return BootstrapWithLogger(ctx, cfg, defaultLogger)
}

// BootstrapWithLogger creates and configures a VM with custom logger.
func BootstrapWithLogger(ctx context.Context, cfg *VMConfig, logger *slog.Logger) (*VM, error) {
	return defaultBootstrapper().run(ctx, cfg, logger)
}

// run is the internal implementation, testable via injected dependencies.
func (b *bootstrapper) run(ctx context.Context, cfg *VMConfig, logger *slog.Logger) (*VM, error) {
	// STEP 1: Validate and set defaults
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	logger.Info("Starting VM bootstrap",
		"name", cfg.Name,
		"vcenter", cfg.VCenterHost,
		"datacenter", cfg.Datacenter,
	)

	// STEP 2: Connect to vCenter
	vclient, err := b.connectVCenter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("vCenter connection failed: %w", err)
	}
	defer func() {
		_ = vclient.Disconnect()
	}()

	logger.Info("Connected to vCenter", "host", cfg.VCenterHost)

	// STEP 3: Check if VM already exists (idempotency)
	existingVM, err := vclient.FindVM(cfg.Datacenter, cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing VM: %w", err)
	}
	if existingVM != nil {
		return nil, fmt.Errorf("VM %q already exists", cfg.Name)
	}

	// STEP 4: Find vCenter objects
	folder, err := vclient.FindFolder(cfg.Datacenter, cfg.Folder)
	if err != nil {
		return nil, fmt.Errorf("failed to find folder: %w", err)
	}

	resourcePool, err := vclient.FindResourcePool(cfg.Datacenter, cfg.ResourcePool)
	if err != nil {
		return nil, fmt.Errorf("failed to find resource pool: %w", err)
	}

	datastore, err := vclient.FindDatastore(cfg.Datacenter, cfg.Datastore)
	if err != nil {
		return nil, fmt.Errorf("failed to find datastore: %w", err)
	}

	// isoDatastore is where profile boot artifacts are uploaded.
	// Defaults to the VM datastore when ISODatastore is not configured.
	isoDatastoreName := cfg.ISODatastore
	if isoDatastoreName == "" {
		isoDatastoreName = cfg.Datastore
	}
	isoDatastore, err := vclient.FindDatastore(cfg.Datacenter, isoDatastoreName)
	if err != nil {
		return nil, fmt.Errorf("failed to find ISO datastore %q: %w", isoDatastoreName, err)
	}

	network, err := vclient.FindNetwork(cfg.Datacenter, cfg.NetworkName)
	if err != nil {
		return nil, fmt.Errorf("failed to find network: %w", err)
	}

	logger.Info("vCenter objects located",
		"folder", cfg.Folder,
		"datastore", cfg.Datastore,
		"iso_datastore", isoDatastoreName,
		"network", cfg.NetworkName,
	)

	// STEP 5: Create VM hardware
	creator := b.newVMCreator(ctx)

	vmConfig := &vm.Config{
		Name:         cfg.Name,
		CPUs:         int32(cfg.CPUs),
		MemoryMB:     int64(cfg.MemoryMB),
		DiskSizeGB:   int64(cfg.DiskSizeGB),
		NetworkName:  cfg.NetworkName,
		Datacenter:   cfg.Datacenter,
		Folder:       cfg.Folder,
		ResourcePool: cfg.ResourcePool,
		Datastore:    cfg.Datastore,
		Firmware:     cfg.Firmware,
	}

	if cfg.DataDiskSizeGB != nil {
		size := int64(*cfg.DataDiskSizeGB)
		vmConfig.DataDiskSizeGB = &size
	}

	spec := creator.CreateSpec(vmConfig)
	createdVM, err := creator.Create(folder, resourcePool, datastore, spec)
	if err != nil {
		return nil, fmt.Errorf("VM creation failed: %w", err)
	}

	// Initialize ISO manager early (needed in defer cleanup)
	isoMgr := b.newISOManager(ctx)

	// Cleanup partial VM and uploaded ISOs on failure (idempotency)
	var bootstrapSuccess bool
	var profileResult profile.Result
	defer func() {
		if !bootstrapSuccess && !cfg.SkipCleanupOnError {
			if createdVM != nil {
				logger.Warn("Bootstrap failed - cleaning up partial VM", "name", cfg.Name)
				if deleteErr := creator.Delete(createdVM); deleteErr != nil {
					logger.Error("Failed to cleanup partial VM", "error", deleteErr)
				}
			}
			if profileResult.NoCloudUploadPath != "" {
				if deleteErr := isoMgr.DeleteFromDatastore(isoDatastoreName, profileResult.NoCloudUploadPath,
					cfg.VCenterHost, cfg.VCenterUsername, cfg.VCenterPassword, cfg.VCenterInsecure); deleteErr != nil {
					logger.Warn("Failed to cleanup NoCloud ISO from datastore", "error", deleteErr)
				} else {
					logger.Info("NoCloud ISO cleaned up from datastore", "path", profileResult.NoCloudUploadPath)
				}
			}
		}
	}()

	logger.Info("VM hardware created", "name", cfg.Name)

	// STEP 6: Add SCSI controller
	scsiKey, err := creator.EnsureSCSIController(createdVM)
	if err != nil {
		return nil, fmt.Errorf("failed to add SCSI controller: %w", err)
	}

	// STEP 7: Add OS disk
	if err := creator.AddDisk(createdVM, datastore, int64(cfg.DiskSizeGB), scsiKey); err != nil {
		return nil, fmt.Errorf("failed to add OS disk: %w", err)
	}

	// STEP 8: Add data disk (if specified)
	if cfg.DataDiskSizeGB != nil {
		if err := creator.AddDisk(createdVM, datastore, int64(*cfg.DataDiskSizeGB), scsiKey); err != nil {
			return nil, fmt.Errorf("failed to add data disk: %w", err)
		}
		logger.Info("Data disk added", "size_gb", *cfg.DataDiskSizeGB)
	}

	// STEP 9: Add network adapter
	if err := creator.AddNetworkAdapter(createdVM, network); err != nil {
		return nil, fmt.Errorf("failed to add network adapter: %w", err)
	}

	// STEP 9b: Apply static MAC address (if specified) or extract auto-assigned MAC
	var assignedMAC string
	if mac := strings.TrimSpace(cfg.MACAddress); mac != "" {
		applied, err := creator.SetMACAddress(createdVM, mac)
		if err != nil {
			return nil, fmt.Errorf("failed to set MAC address: %w", err)
		}
		assignedMAC = applied
		logger.Info("Static MAC address applied", "mac", assignedMAC)
	} else {
		// Extract auto-assigned MAC for persistence/recreation
		got, err := creator.GetMACAddress(createdVM)
		if err != nil {
			logger.Warn("Could not read auto-assigned MAC address", "error", err)
		} else {
			assignedMAC = got
			logger.Info("Auto-assigned MAC address captured", "mac", assignedMAC)
		}
	}

	logger.Info("VM hardware configuration complete")

	provisioner, err := b.resolveProfile(cfg.Profile)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve profile: %w", err)
	}

	profileResult, err = provisioner.ProvisionAndBoot(ctx, profile.Input{
		VMName:            cfg.Name,
		Username:          cfg.Username,
		Password:          cfg.Password,
		PasswordHash:      cfg.PasswordHash,
		SSHPublicKeys:     cfg.SSHPublicKeys,
		AllowPasswordSSH:  cfg.AllowPasswordSSH,
		Timezone:          cfg.Timezone,
		Locale:            cfg.Locale,
		NetworkInterface:  cfg.NetworkInterface,
		IPAddress:         cfg.IPAddress,
		Netmask:           cfg.Netmask,
		Gateway:           cfg.Gateway,
		DNS:               cfg.DNS,
		DataDiskMountPath: cfg.DataDiskMountPath,
		SwapSizeGB:        cfg.SwapSizeGB,
		OSVersion:         cfg.EffectiveOSVersion(),
		OSSchematicID:     cfg.EffectiveOSSchematicID(),
		VCenterHost:       cfg.VCenterHost,
		VCenterUsername:   cfg.VCenterUsername,
		VCenterPassword:   cfg.VCenterPassword,
		VCenterInsecure:   cfg.VCenterInsecure,
	}, profile.Runtime{
		CreatedVM:        createdVM,
		Creator:          creator,
		ISOManager:       isoMgr,
		ISODatastore:     isoDatastore,
		ISODatastoreName: isoDatastoreName,
		Logger:           logger,
	})
	if err != nil {
		return nil, err
	}

	// STEP 16: Wait for installation for profiles that use Ubuntu autoinstall flow.
	if cfg.EffectiveProfile() == "ubuntu" {
		if err := b.waitInstall(ctx, createdVM, cfg, logger); err != nil {
			return nil, fmt.Errorf("installation failed: %w", err)
		}
		logger.Info("Installation complete")
	}

	// STEP 16.5: Profile post-install actions
	if err := provisioner.PostInstall(ctx, profile.Input{
		VMName:            cfg.Name,
		Username:          cfg.Username,
		Password:          cfg.Password,
		PasswordHash:      cfg.PasswordHash,
		SSHPublicKeys:     cfg.SSHPublicKeys,
		AllowPasswordSSH:  cfg.AllowPasswordSSH,
		Timezone:          cfg.Timezone,
		Locale:            cfg.Locale,
		NetworkInterface:  cfg.NetworkInterface,
		IPAddress:         cfg.IPAddress,
		Netmask:           cfg.Netmask,
		Gateway:           cfg.Gateway,
		DNS:               cfg.DNS,
		DataDiskMountPath: cfg.DataDiskMountPath,
		SwapSizeGB:        cfg.SwapSizeGB,
		OSVersion:         cfg.EffectiveOSVersion(),
		OSSchematicID:     cfg.EffectiveOSSchematicID(),
		VCenterHost:       cfg.VCenterHost,
		VCenterUsername:   cfg.VCenterUsername,
		VCenterPassword:   cfg.VCenterPassword,
		VCenterInsecure:   cfg.VCenterInsecure,
	}, profile.Runtime{
		CreatedVM:        createdVM,
		Creator:          creator,
		ISOManager:       isoMgr,
		ISODatastore:     isoDatastore,
		ISODatastoreName: isoDatastoreName,
		Logger:           logger,
	}, profileResult); err != nil {
		return nil, err
	}

	// STEP 17: Verify SSH access for Ubuntu profile.
	skipSSHVerify := cfg.SkipSSHVerify || cfg.EffectiveProfile() != "ubuntu"
	if skipSSHVerify {
		logger.Warn("Skipping SSH verification", "profile", cfg.EffectiveProfile(), "skip_ssh_verify", cfg.SkipSSHVerify)
	} else {
		if err := b.checkSSH(ctx, cfg.IPAddress); err != nil {
			return nil, fmt.Errorf("SSH verification failed: %w", err)
		}
		logger.Info("SSH access verified")
	}

	// Mark bootstrap as successful (prevents defer cleanup)
	bootstrapSuccess = true

	return &VM{
		Name:            cfg.Name,
		IPAddress:       cfg.IPAddress,
		MACAddress:      assignedMAC,
		ManagedObject:   createdVM.Reference(),
		SSHReady:        !skipSSHVerify,
		Hostname:        cfg.Name,
		VCenterHost:     cfg.VCenterHost,
		VCenterPort:     cfg.VCenterPort,
		VCenterUser:     cfg.VCenterUsername,
		VCenterPass:     cfg.VCenterPassword,
		VCenterInsecure: cfg.VCenterInsecure,
	}, nil
}

// Verify performs a basic health check: VM powered on, VMware Tools running (if available),
// hostname matches (if available), and SSH port is reachable (if IP is set).
func (vm *VM) Verify(ctx context.Context) error {
	client, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     vm.VCenterHost,
		Port:     vm.VCenterPort,
		Username: vm.VCenterUser,
		Password: vm.VCenterPass,
		Insecure: vm.VCenterInsecure,
	})
	if err != nil {
		return fmt.Errorf("vCenter connection failed: %w", err)
	}
	defer func() {
		_ = client.Disconnect()
	}()

	vmObj := object.NewVirtualMachine(client.Client().Client, vm.ManagedObject)
	var moVM mo.VirtualMachine
	if err := vmObj.Properties(ctx, vmObj.Reference(), []string{"runtime", "guest"}, &moVM); err != nil {
		return fmt.Errorf("failed to fetch VM properties: %w", err)
	}

	if moVM.Runtime.PowerState != "poweredOn" {
		return fmt.Errorf("VM not powered on (state=%s)", moVM.Runtime.PowerState)
	}

	if moVM.Guest == nil {
		return fmt.Errorf("guest info unavailable (VMware Tools not reporting)")
	}

	if moVM.Guest.ToolsRunningStatus != "guestToolsRunning" {
		return fmt.Errorf("VMware Tools not running (status=%s)", moVM.Guest.ToolsRunningStatus)
	}

	if vm.Hostname != "" && moVM.Guest.HostName != "" && moVM.Guest.HostName != vm.Hostname {
		return fmt.Errorf("hostname mismatch (expected=%s got=%s)", vm.Hostname, moVM.Guest.HostName)
	}

	if vm.IPAddress == "" {
		return fmt.Errorf("IPAddress is required for SSH verification")
	}

	if err := sshVerifier(ctx, vm.IPAddress); err != nil {
		return fmt.Errorf("SSH verification failed: %w", err)
	}

	return nil
}

// PowerOff powers off the VM and waits for completion.
func (vm *VM) PowerOff(ctx context.Context) error {
	client, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     vm.VCenterHost,
		Port:     vm.VCenterPort,
		Username: vm.VCenterUser,
		Password: vm.VCenterPass,
		Insecure: vm.VCenterInsecure,
	})
	if err != nil {
		return fmt.Errorf("vCenter connection failed: %w", err)
	}
	defer func() {
		_ = client.Disconnect()
	}()

	vmObj := object.NewVirtualMachine(client.Client().Client, vm.ManagedObject)
	state, err := vmObj.PowerState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get power state: %w", err)
	}
	if state != types.VirtualMachinePowerStatePoweredOn {
		return nil
	}

	task, err := vmObj.PowerOff(ctx)
	if err != nil {
		return fmt.Errorf("power off failed: %w", err)
	}
	if err := task.Wait(ctx); err != nil {
		return fmt.Errorf("power off wait failed: %w", err)
	}
	return nil
}

// PowerOn powers on the VM and waits for completion.
func (vm *VM) PowerOn(ctx context.Context) error {
	client, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     vm.VCenterHost,
		Port:     vm.VCenterPort,
		Username: vm.VCenterUser,
		Password: vm.VCenterPass,
		Insecure: vm.VCenterInsecure,
	})
	if err != nil {
		return fmt.Errorf("vCenter connection failed: %w", err)
	}
	defer func() {
		_ = client.Disconnect()
	}()

	vmObj := object.NewVirtualMachine(client.Client().Client, vm.ManagedObject)
	state, err := vmObj.PowerState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get power state: %w", err)
	}
	if state == types.VirtualMachinePowerStatePoweredOn {
		return nil
	}

	task, err := vmObj.PowerOn(ctx)
	if err != nil {
		return fmt.Errorf("power on failed: %w", err)
	}
	if err := task.Wait(ctx); err != nil {
		return fmt.Errorf("power on wait failed: %w", err)
	}
	return nil
}

// Delete powers off the VM if needed and removes it from vCenter.
func (vm *VM) Delete(ctx context.Context) error {
	client, err := vcenter.NewClient(ctx, &vcenter.Config{
		Host:     vm.VCenterHost,
		Port:     vm.VCenterPort,
		Username: vm.VCenterUser,
		Password: vm.VCenterPass,
		Insecure: vm.VCenterInsecure,
	})
	if err != nil {
		return fmt.Errorf("vCenter connection failed: %w", err)
	}
	defer func() {
		_ = client.Disconnect()
	}()

	vmObj := object.NewVirtualMachine(client.Client().Client, vm.ManagedObject)
	state, err := vmObj.PowerState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get power state: %w", err)
	}
	if state == types.VirtualMachinePowerStatePoweredOn {
		task, err := vmObj.PowerOff(ctx)
		if err != nil {
			return fmt.Errorf("power off failed: %w", err)
		}
		if err := task.Wait(ctx); err != nil {
			return fmt.Errorf("power off wait failed: %w", err)
		}
	}
	task, err := vmObj.Destroy(ctx)
	if err != nil {
		return fmt.Errorf("delete failed: %w", err)
	}
	if err := task.Wait(ctx); err != nil {
		return fmt.Errorf("delete wait failed: %w", err)
	}
	return nil
}

// waitForInstallation monitors VM until OS installed.
// Matches Python _wait_for_installation_complete() exactly:
// Phase 1: Wait for VMware Tools running (installation started)
// Phase 2: Wait for VM to reboot (Tools stop = autoinstall complete)
// Phase 3: Wait for Tools running + hostname set (first boot complete)
func waitForInstallation(ctx context.Context, vmObj *object.VirtualMachine, cfg *VMConfig, logger *slog.Logger) error {
	timeout := configs.Defaults.Timeouts.Installation()
	ticker := time.NewTicker(configs.Defaults.Timeouts.Polling())
	defer ticker.Stop()

	started := time.Now()
	deadline := time.Now().Add(timeout)
	lastHeartbeat := started
	heartbeatEvery := 10 * time.Second
	estimatedInstall, estimateSamples := loadInstallDurationEstimate(cfg)

	toolsWasRunning := false
	rebootDetected := false
	hostnameCheckCount := 0
	requiredHostnameChecks := configs.Defaults.Timeouts.HostnameChecks

	logger.Info("Phase 1: Waiting for installation to start (VMware Tools)...")
	logger.Info("Installation progress",
		"phase", currentInstallPhase(toolsWasRunning, rebootDetected),
		"elapsed", 0,
		"remaining_timeout", time.Until(deadline).Truncate(time.Second),
		"tools_running", false,
		"hostname", "",
		"hostname_checks", fmt.Sprintf("%d/%d", hostnameCheckCount, requiredHostnameChecks))
	if estimatedInstall > 0 {
		logger.Info("Installation ETA baseline loaded",
			"eta_total", estimatedInstall.Truncate(time.Second),
			"samples", estimateSamples)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("installation timeout (%v)", timeout)
			}

			var moVM mo.VirtualMachine
			if err := vmObj.Properties(ctx, vmObj.Reference(), []string{"guest"}, &moVM); err != nil {
				if time.Since(lastHeartbeat) >= heartbeatEvery {
					lastHeartbeat = time.Now()
					logger.Info("Installation progress",
						"phase", currentInstallPhase(toolsWasRunning, rebootDetected),
						"elapsed", time.Since(started).Truncate(time.Second),
						"remaining_timeout", time.Until(deadline).Truncate(time.Second))
				}
				continue
			}
			if moVM.Guest == nil {
				if time.Since(lastHeartbeat) >= heartbeatEvery {
					lastHeartbeat = time.Now()
					logger.Info("Installation progress",
						"phase", currentInstallPhase(toolsWasRunning, rebootDetected),
						"elapsed", time.Since(started).Truncate(time.Second),
						"remaining_timeout", time.Until(deadline).Truncate(time.Second))
				}
				continue
			}

			toolsRunning := moVM.Guest.ToolsRunningStatus == "guestToolsRunning"
			hostname := moVM.Guest.HostName
			if time.Since(lastHeartbeat) >= heartbeatEvery {
				lastHeartbeat = time.Now()
				logger.Info("Installation progress",
					"phase", currentInstallPhase(toolsWasRunning, rebootDetected),
					"elapsed", time.Since(started).Truncate(time.Second),
					"remaining_timeout", time.Until(deadline).Truncate(time.Second),
					"tools_running", toolsRunning,
					"hostname", hostname,
					"hostname_checks", fmt.Sprintf("%d/%d", hostnameCheckCount, requiredHostnameChecks))
				if estimatedInstall > 0 {
					etaRemaining := estimatedInstall - time.Since(started)
					if etaRemaining < 0 {
						etaRemaining = 0
					}
					logger.Info("Installation ETA",
						"eta_remaining", etaRemaining.Truncate(time.Second),
						"eta_total", estimatedInstall.Truncate(time.Second),
						"samples", estimateSamples)
				}
			}

			if !toolsWasRunning && toolsRunning {
				if !rebootDetected {
					logger.Info("Phase 1 complete: Installation started (VMware Tools running)")
				} else {
					logger.Info("Phase 3: VMware Tools running again after reboot")
				}
				toolsWasRunning = true
			}

			if !toolsWasRunning {
				continue
			}

			if toolsWasRunning && !toolsRunning {
				if !rebootDetected {
					logger.Info("Phase 2: VM rebooting (VMware Tools stopped - autoinstall completing)...")
					rebootDetected = true
				}
				toolsWasRunning = false
				hostnameCheckCount = 0
				continue
			}

			if toolsRunning {
				toolsWasRunning = true
			}

			if rebootDetected && toolsRunning && hostname == cfg.Name {
				hostnameCheckCount++
				logger.Info("Phase 3: Installation may be complete",
					"hostname", hostname,
					"checks", hostnameCheckCount,
					"required", requiredHostnameChecks)
				if hostnameCheckCount >= requiredHostnameChecks {
					logger.Info("Installation complete, waiting for services to start...",
						"wait", configs.Defaults.Timeouts.ServiceStartup())
					time.Sleep(configs.Defaults.Timeouts.ServiceStartup())
					_ = recordInstallDuration(cfg, time.Since(started))
					return nil
				}
			} else if rebootDetected && toolsRunning && hostname != cfg.Name {
				hostnameCheckCount = 0
			}

			// Alternative: no reboot but hostname stable (Ubuntu 22.04 behavior)
			if !rebootDetected && toolsRunning && hostname == cfg.Name {
				hostnameCheckCount++
				logger.Info("Installation may be complete (no reboot)",
					"hostname", hostname,
					"checks", hostnameCheckCount)
				if hostnameCheckCount >= requiredHostnameChecks {
					logger.Info("Installation complete (stable hostname), waiting for services...",
						"wait", configs.Defaults.Timeouts.ServiceStartup())
					time.Sleep(configs.Defaults.Timeouts.ServiceStartup())
					_ = recordInstallDuration(cfg, time.Since(started))
					return nil
				}
			} else if !rebootDetected && toolsRunning && hostname != cfg.Name {
				hostnameCheckCount = 0
			}
		}
	}
}

func currentInstallPhase(toolsWasRunning, rebootDetected bool) string {
	if !toolsWasRunning && !rebootDetected {
		return "phase1-wait-tools-start"
	}
	if rebootDetected && !toolsWasRunning {
		return "phase2-wait-reboot"
	}
	return "phase3-wait-stable-hostname"
}

// verifySSHAccess verifies SSH port 22 is accessible.
func verifySSHAccess(ctx context.Context, ipAddr string) error {
	t := configs.Defaults.Timeouts
	for i := 0; i < t.SSHRetries; i++ {
		if utils.IsPortOpen(ipAddr, 22, t.SSHConnect()) {
			return nil
		}
		time.Sleep(t.SSHRetryDelay())
	}
	return fmt.Errorf("SSH port 22 not accessible at %s", ipAddr)
}
