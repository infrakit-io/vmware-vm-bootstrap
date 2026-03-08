package bootstrap

import (
	"testing"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
)

func TestSetDefaults_appliesDefaults(t *testing.T) {
	cfg := &VMConfig{}
	cfg.SetDefaults()

	if cfg.VCenterPort != configs.Defaults.VCenter.Port {
		t.Errorf("VCenterPort = %d, want %d", cfg.VCenterPort, configs.Defaults.VCenter.Port)
	}
	if cfg.Timezone != configs.Defaults.CloudInit.Timezone {
		t.Errorf("Timezone = %q, want %q", cfg.Timezone, configs.Defaults.CloudInit.Timezone)
	}
	if cfg.Locale != configs.Defaults.CloudInit.Locale {
		t.Errorf("Locale = %q, want %q", cfg.Locale, configs.Defaults.CloudInit.Locale)
	}
	if cfg.Firmware != configs.Defaults.VM.Firmware {
		t.Errorf("Firmware = %q, want %q", cfg.Firmware, configs.Defaults.VM.Firmware)
	}
}

func TestSetDefaults_preservesExistingValues(t *testing.T) {
	cfg := &VMConfig{
		VCenterPort: 8443,
		Timezone:    "Europe/Bucharest",
		Locale:      "ro_RO.UTF-8",
		Firmware:    "efi",
	}
	cfg.SetDefaults()

	if cfg.VCenterPort != 8443 {
		t.Errorf("VCenterPort overwritten: got %d, want 8443", cfg.VCenterPort)
	}
	if cfg.Timezone != "Europe/Bucharest" {
		t.Errorf("Timezone overwritten: got %q, want Europe/Bucharest", cfg.Timezone)
	}
	if cfg.Locale != "ro_RO.UTF-8" {
		t.Errorf("Locale overwritten: got %q, want ro_RO.UTF-8", cfg.Locale)
	}
	if cfg.Firmware != "efi" {
		t.Errorf("Firmware overwritten: got %q, want efi", cfg.Firmware)
	}
}

func TestSetDefaults_partialConfig(t *testing.T) {
	cfg := &VMConfig{
		Firmware: "efi", // explicit
		// VCenterPort, Timezone, Locale → should get defaults
	}
	cfg.SetDefaults()

	if cfg.Firmware != "efi" {
		t.Errorf("Firmware overwritten: got %q, want efi", cfg.Firmware)
	}
	if cfg.VCenterPort == 0 {
		t.Error("VCenterPort should have a default, got 0")
	}
	if cfg.Timezone == "" {
		t.Error("Timezone should have a default, got empty")
	}
	if cfg.Locale == "" {
		t.Error("Locale should have a default, got empty")
	}
}

func TestSetDefaults_idempotent(t *testing.T) {
	cfg := &VMConfig{}
	cfg.SetDefaults()
	port1, tz1, loc1, fw1 := cfg.VCenterPort, cfg.Timezone, cfg.Locale, cfg.Firmware

	cfg.SetDefaults() // second call
	if cfg.VCenterPort != port1 || cfg.Timezone != tz1 || cfg.Locale != loc1 || cfg.Firmware != fw1 {
		t.Error("SetDefaults() is not idempotent - values changed on second call")
	}
}

func TestValidate_MinimalValidConfig(t *testing.T) {
	cfg := &VMConfig{
		VCenterHost:     "vcenter.example.com",
		VCenterUsername: "admin",
		VCenterPassword: "secret",
		Name:            "vm1",
		Username:        "sysadmin",
		SSHPublicKeys:   []string{"ssh-ed25519 AAAA"},
		IPAddress:       "192.168.1.10",
		Netmask:         "255.255.255.0",
		Gateway:         "192.168.1.1",
		DNS:             []string{"8.8.8.8"},
		DiskSizeGB:      20,
		Profile:         "ubuntu",
		Profiles: VMProfiles{
			Ubuntu: UbuntuProfile{Version: "24.04"},
		},
		Datacenter:  "DC1",
		Datastore:   "DS1",
		NetworkName: "LAN",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}

func TestValidate_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		cfg  *VMConfig
	}{
		{"Missing VCenterHost", &VMConfig{VCenterUsername: "u", VCenterPassword: "p"}},
		{"Missing VCenterUsername", &VMConfig{VCenterHost: "h", VCenterPassword: "p"}},
		{"Missing VCenterPassword", &VMConfig{VCenterHost: "h", VCenterUsername: "u"}},
		{"Missing Name", &VMConfig{VCenterHost: "h", VCenterUsername: "u", VCenterPassword: "p"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

func TestValidate_MissingAuthInputs(t *testing.T) {
	cfg := &VMConfig{
		VCenterHost:     "vcenter",
		VCenterUsername: "admin",
		VCenterPassword: "secret",
		Name:            "vm1",
		Username:        "sysadmin",
		IPAddress:       "192.168.1.10",
		Netmask:         "255.255.255.0",
		Gateway:         "192.168.1.1",
		DNS:             []string{"8.8.8.8"},
		DiskSizeGB:      20,
		Profile:         "ubuntu",
		Profiles: VMProfiles{
			Ubuntu: UbuntuProfile{Version: "24.04"},
		},
		Datacenter:  "DC1",
		Datastore:   "DS1",
		NetworkName: "LAN",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when no SSH keys or password are provided")
	}
}

func TestValidate_NetworkErrors(t *testing.T) {
	cfg := &VMConfig{
		VCenterHost:     "vcenter",
		VCenterUsername: "admin",
		VCenterPassword: "secret",
		Name:            "vm1",
		Username:        "sysadmin",
		SSHPublicKeys:   []string{"ssh-ed25519 AAAA"},
		IPAddress:       "invalid",
		Netmask:         "255.255.255.0",
		Gateway:         "192.168.1.1",
		DNS:             []string{"8.8.8.8"},
		DiskSizeGB:      20,
		Profile:         "ubuntu",
		Profiles: VMProfiles{
			Ubuntu: UbuntuProfile{Version: "24.04"},
		},
		Datacenter:  "DC1",
		Datastore:   "DS1",
		NetworkName: "LAN",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid IP")
	}
}

func TestValidate_DiskTooSmall(t *testing.T) {
	cfg := &VMConfig{
		VCenterHost:     "vcenter",
		VCenterUsername: "admin",
		VCenterPassword: "secret",
		Name:            "vm1",
		Username:        "sysadmin",
		SSHPublicKeys:   []string{"ssh-ed25519 AAAA"},
		IPAddress:       "192.168.1.10",
		Netmask:         "255.255.255.0",
		Gateway:         "192.168.1.1",
		DNS:             []string{"8.8.8.8"},
		DiskSizeGB:      5,
		Profile:         "ubuntu",
		Profiles: VMProfiles{
			Ubuntu: UbuntuProfile{Version: "24.04"},
		},
		Datacenter:  "DC1",
		Datastore:   "DS1",
		NetworkName: "LAN",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for DiskSizeGB < 10")
	}
}

func TestValidate_TalosProfileRequiresVersion(t *testing.T) {
	cfg := minimalValidConfigForTests()
	cfg.Profile = "talos"
	cfg.Profiles.Ubuntu.Version = ""
	cfg.Profiles.Talos.Version = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when talos profile has no version")
	}
}

func TestValidate_TalosProfileWithVersion(t *testing.T) {
	cfg := minimalValidConfigForTests()
	cfg.Profile = "talos"
	cfg.Profiles.Ubuntu.Version = ""
	cfg.Profiles.Talos.Version = "v1.12.0"
	cfg.Profiles.Talos.SchematicID = "903b2da78f99adef03cbbd4df6714563823f63218508800751560d3bc3557e40"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error for valid talos profile: %v", err)
	}
}

func TestValidate_TalosProfileRequiresSchematicID(t *testing.T) {
	cfg := minimalValidConfigForTests()
	cfg.Profile = "talos"
	cfg.Profiles.Ubuntu.Version = ""
	cfg.Profiles.Talos.Version = "v1.12.0"
	cfg.Profiles.Talos.SchematicID = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when talos profile has no schematic id")
	}
}

func minimalValidConfigForTests() *VMConfig {
	return &VMConfig{
		VCenterHost:     "vcenter.example.com",
		VCenterUsername: "admin",
		VCenterPassword: "secret",
		Name:            "vm1",
		Username:        "sysadmin",
		SSHPublicKeys:   []string{"ssh-ed25519 AAAA"},
		IPAddress:       "192.168.1.10",
		Netmask:         "255.255.255.0",
		Gateway:         "192.168.1.1",
		DNS:             []string{"8.8.8.8"},
		DiskSizeGB:      20,
		Profile:         "ubuntu",
		Profiles: VMProfiles{
			Ubuntu: UbuntuProfile{Version: "24.04"},
		},
		Datacenter:  "DC1",
		Datastore:   "DS1",
		NetworkName: "LAN",
	}
}

func TestEffectiveVersionHelpers(t *testing.T) {
	cfg := &VMConfig{
		Profiles: VMProfiles{
			Ubuntu: UbuntuProfile{Version: "24.04"},
			Talos:  TalosProfile{Version: "v1.12.4"},
		},
	}

	if got := cfg.EffectiveUbuntuVersion(); got != "24.04" {
		t.Fatalf("EffectiveUbuntuVersion() = %q, want %q", got, "24.04")
	}
	if got := cfg.EffectiveTalosVersion(); got != "v1.12.4" {
		t.Fatalf("EffectiveTalosVersion() = %q, want %q", got, "v1.12.4")
	}
}
