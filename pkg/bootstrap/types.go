// Package bootstrap provides the main public API for VM bootstrapping.
package bootstrap

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Bibi40k/vmware-vm-bootstrap/configs"
	"github.com/Bibi40k/vmware-vm-bootstrap/internal/utils"
	"github.com/vmware/govmomi/vim25/types"
)

// VMConfig defines the complete configuration for VM bootstrap.
type VMConfig struct {
	// === vCenter Connection ===
	VCenterHost     string // vCenter hostname or IP (e.g., "vcenter.example.com")
	VCenterUsername string // vCenter username (e.g., "administrator@vsphere.local")
	VCenterPassword string // vCenter password (encrypted/plain - user's responsibility)
	VCenterPort     int    // vCenter port (default: 443)
	VCenterInsecure bool   // Skip TLS verification (not recommended for production)

	// === VM Specifications ===
	Name              string // VM name (e.g., "web-server-01")
	CPUs              int    // Number of CPUs (e.g., 4)
	MemoryMB          int    // Memory in MB (e.g., 8192)
	DiskSizeGB        int    // OS disk size in GB (e.g., 40)
	DataDiskSizeGB    *int   // Optional data disk size in GB (e.g., 500) - nil = not created
	DataDiskMountPath string // Mount point for data disk (e.g., "/data") - required if DataDiskSizeGB set

	// === Network Configuration ===
	NetworkName      string   // Network name (e.g., "LAN_Management")
	NetworkInterface string   // Guest NIC name (e.g., "ens192")
	MACAddress       string   // Optional static MAC address (e.g., "00:50:56:aa:bb:cc")
	IPAddress        string   // Static IP address (e.g., "192.168.1.10")
	Netmask          string   // Network mask (e.g., "255.255.255.0")
	Gateway          string   // Default gateway (e.g., "192.168.1.1")
	DNS              []string // DNS servers (e.g., ["8.8.8.8", "8.8.4.4"])

	// === VM Placement ===
	Datacenter   string // Datacenter name (e.g., "DC1")
	Folder       string // VM folder path (e.g., "Production/WebServers")
	ResourcePool string // Resource pool path (e.g., "WebTier")
	Datastore    string // VM datastore name (e.g., "VMwareSSD01")
	ISODatastore string // Datastore for ISO uploads (e.g., "VMwareStorage01"); falls back to Datastore if empty
	// vCenter Content Library used for Talos OVA cache/deploy.
	// Name is used when ID is empty; ID is preferred when both are set.
	ContentLibrary   string
	ContentLibraryID string

	// === OS & User Configuration ===
	// OS profile used for VM provisioning (default: "ubuntu").
	Profile string
	// Profile-specific options.
	Profiles      VMProfiles
	Username      string   // SSH user to create (e.g., "sysadmin")
	SSHPublicKeys []string // SSH public keys (one or more)
	Password      string   // Optional plain text password (auto-hashed with bcrypt before use)
	PasswordHash  string   // Optional pre-computed password hash (bcrypt); overrides Password if both set
	// Allow SSH password authentication (default: false). Requires Password or PasswordHash.
	AllowPasswordSSH bool
	// Skip SSH verification during bootstrap (default: false).
	SkipSSHVerify bool
	// Keep VM/ISO on bootstrap failure for debugging (default: false).
	SkipCleanupOnError bool

	// === Advanced Options ===
	Timezone   string // System timezone (default: "UTC")
	Locale     string // System locale (default: "en_US.UTF-8")
	SwapSizeGB *int   // Swap size in GB (default from configs/defaults.yaml)
	Firmware   string // Firmware type: "bios" or "efi" (default: "bios")
}

// VMProfiles contains profile-specific settings.
type VMProfiles struct {
	Ubuntu UbuntuProfile
	Talos  TalosProfile
}

// UbuntuProfile contains Ubuntu-specific settings for profile mode.
type UbuntuProfile struct {
	Version string
}

// TalosProfile contains Talos-specific settings for profile mode.
type TalosProfile struct {
	Version     string
	SchematicID string
}

// VM represents a bootstrapped virtual machine.
type VM struct {
	Name          string                       // VM name
	IPAddress     string                       // Assigned IP address
	MACAddress    string                       // Assigned MAC address (auto or static)
	ManagedObject types.ManagedObjectReference // govmomi VM reference
	SSHReady      bool                         // SSH port 22 accessible
	Hostname      string                       // Configured hostname
	// vCenter connection data for post-create operations (Verify/PowerOn/PowerOff/Delete).
	// These fields are intentionally not serialized.
	VCenterHost     string `json:"-"`
	VCenterPort     int    `json:"-"`
	VCenterUser     string `json:"-"`
	VCenterPass     string `json:"-"`
	VCenterInsecure bool   `json:"-"`
}

// Validate checks if the VM configuration is valid.
func (cfg *VMConfig) Validate() error {
	if cfg.VCenterHost == "" {
		return fmt.Errorf("VCenterHost is required")
	}
	if cfg.VCenterUsername == "" {
		return fmt.Errorf("VCenterUsername is required")
	}
	if cfg.VCenterPassword == "" {
		return fmt.Errorf("VCenterPassword is required")
	}
	if cfg.Name == "" {
		return fmt.Errorf("name is required")
	}
	profile := cfg.effectiveProfile()
	if profile != "ubuntu" && profile != "talos" {
		return fmt.Errorf("unsupported Profile %q (supported: ubuntu, talos)", profile)
	}
	if profile == "ubuntu" {
		if cfg.Username == "" {
			return fmt.Errorf("username is required")
		}
		if len(cfg.SSHPublicKeys) == 0 && cfg.Password == "" && cfg.PasswordHash == "" {
			return fmt.Errorf("at least one of SSHPublicKeys, Password, or PasswordHash is required")
		}
		if cfg.AllowPasswordSSH && cfg.Password == "" && cfg.PasswordHash == "" {
			return fmt.Errorf("AllowPasswordSSH requires Password or PasswordHash")
		}
	}
	if cfg.IPAddress == "" {
		return fmt.Errorf("IPAddress is required")
	}
	if cfg.Netmask == "" {
		return fmt.Errorf("netmask is required")
	}
	if cfg.Gateway == "" {
		return fmt.Errorf("gateway is required")
	}
	if len(cfg.DNS) == 0 {
		return fmt.Errorf("at least one DNS server is required")
	}
	if cfg.DiskSizeGB < 10 {
		return fmt.Errorf("DiskSizeGB must be at least 10 (got %d)", cfg.DiskSizeGB)
	}
	if cfg.DataDiskSizeGB != nil && cfg.DataDiskMountPath == "" {
		return fmt.Errorf("DataDiskMountPath is required when DataDiskSizeGB is set")
	}
	if profile == "ubuntu" && cfg.effectiveUbuntuVersion() == "" {
		return fmt.Errorf("Profiles.Ubuntu.Version is required for ubuntu profile")
	}
	if profile == "talos" && cfg.effectiveTalosVersion() == "" {
		return fmt.Errorf("Profiles.Talos.Version is required for talos profile")
	}
	if profile == "talos" && cfg.EffectiveOSSchematicID() == "" {
		return fmt.Errorf("Profiles.Talos.SchematicID is required for talos profile")
	}
	if cfg.Datacenter == "" {
		return fmt.Errorf("datacenter is required")
	}
	if cfg.Datastore == "" {
		return fmt.Errorf("datastore is required")
	}
	if cfg.NetworkName == "" {
		return fmt.Errorf("NetworkName is required")
	}
	// Validate network config using utils
	if err := utils.ValidateNetworkConfig(cfg.IPAddress, cfg.Netmask, cfg.Gateway, cfg.DNS); err != nil {
		return err
	}
	if mac := strings.TrimSpace(cfg.MACAddress); mac != "" && !macAddressRe.MatchString(strings.ToLower(mac)) {
		return fmt.Errorf("invalid MACAddress format: %q (expected aa:bb:cc:dd:ee:ff)", cfg.MACAddress)
	}
	return nil
}

var macAddressRe = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

// SetDefaults sets default values for optional fields from configs/defaults.yaml.
func (cfg *VMConfig) SetDefaults() {
	d := configs.Defaults
	if cfg.Profile == "" {
		cfg.Profile = "ubuntu"
	}
	if cfg.VCenterPort == 0 {
		cfg.VCenterPort = d.VCenter.Port
	}
	if cfg.Timezone == "" {
		cfg.Timezone = d.CloudInit.Timezone
	}
	if cfg.Locale == "" {
		cfg.Locale = d.CloudInit.Locale
	}
	if cfg.Firmware == "" {
		cfg.Firmware = d.VM.Firmware
	}
	if cfg.NetworkInterface == "" {
		cfg.NetworkInterface = d.Network.Interface
	}
}

// EffectiveProfile returns normalized profile name.
func (cfg *VMConfig) EffectiveProfile() string {
	return cfg.effectiveProfile()
}

func (cfg *VMConfig) effectiveProfile() string {
	if cfg.Profile == "" {
		return "ubuntu"
	}
	return cfg.Profile
}

// EffectiveUbuntuVersion returns Ubuntu version from profile config.
func (cfg *VMConfig) EffectiveUbuntuVersion() string {
	return cfg.effectiveUbuntuVersion()
}

func (cfg *VMConfig) effectiveUbuntuVersion() string { return cfg.Profiles.Ubuntu.Version }

// EffectiveTalosVersion returns Talos version from profile config.
func (cfg *VMConfig) EffectiveTalosVersion() string {
	return cfg.effectiveTalosVersion()
}

func (cfg *VMConfig) effectiveTalosVersion() string {
	return cfg.Profiles.Talos.Version
}

// EffectiveOSVersion returns OS version for the selected profile.
func (cfg *VMConfig) EffectiveOSVersion() string {
	switch cfg.effectiveProfile() {
	case "talos":
		return cfg.effectiveTalosVersion()
	default:
		return cfg.effectiveUbuntuVersion()
	}
}

// EffectiveOSSchematicID returns schematic/build identifier for selected OS profile.
func (cfg *VMConfig) EffectiveOSSchematicID() string {
	switch cfg.effectiveProfile() {
	case "talos":
		return cfg.Profiles.Talos.SchematicID
	default:
		return ""
	}
}
