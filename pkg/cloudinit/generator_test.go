package cloudinit

import (
	"strings"
	"testing"
	"text/template"
)

func TestNewGenerator(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	if gen.autoinstallTmpl == nil {
		t.Error("autoinstallTmpl is nil")
	}
	if gen.metaDataTmpl == nil {
		t.Error("metaDataTmpl is nil")
	}
	if gen.networkConfigTmpl == nil {
		t.Error("networkConfigTmpl is nil")
	}
}

func TestGenerateUserData(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	input := &UserDataInput{
		Hostname:       "test-vm",
		Username:       "ubuntu",
		PasswordHash:   "$6$rounds=5000$salt$hash",
		SSHPublicKeys:  []string{"ssh-ed25519 AAAA... test@example.com"},
		Locale:         "en_US.UTF-8",
		Timezone:       "UTC",
		KeyboardLayout: "us",
		SwapSize:       "4G",
		SwapSizeGB:     4,
		Packages:       []string{"open-vm-tools", "curl"},
		UserGroups:     "sudo,adm",
		UserShell:      "/bin/bash",
		IPAddress:      "192.168.1.10",
		CIDR:           24,
		Gateway:        "192.168.1.1",
		DNS:            []string{"8.8.8.8"},
	}

	userData, err := gen.GenerateUserData(input)
	if err != nil {
		t.Fatalf("GenerateUserData() failed: %v", err)
	}

	// Verify generated YAML contains expected values
	if !strings.Contains(userData, "hostname: test-vm") {
		t.Error("Generated user-data missing hostname")
	}
	if !strings.Contains(userData, "username: ubuntu") {
		t.Error("Generated user-data missing username")
	}
	if !strings.Contains(userData, "ssh-ed25519 AAAA...") {
		t.Error("Generated user-data missing SSH key")
	}
	if !strings.Contains(userData, "locale: en_US.UTF-8") {
		t.Error("Generated user-data missing locale")
	}
	if !strings.Contains(userData, "timezone: UTC") {
		t.Error("Generated user-data missing timezone")
	}
	if !strings.Contains(userData, "#cloud-config") {
		t.Error("Generated user-data missing #cloud-config header")
	}
}

func TestGenerateUserData_NoPasswordHash(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	input := &UserDataInput{
		Hostname:       "test-vm",
		Username:       "ubuntu",
		PasswordHash:   "",
		SSHPublicKeys:  []string{"ssh-ed25519 AAAA... test@example.com"},
		Locale:         "en_US.UTF-8",
		Timezone:       "UTC",
		KeyboardLayout: "us",
		SwapSize:       "2G",
		SwapSizeGB:     2,
		Packages:       []string{"open-vm-tools"},
		UserGroups:     "sudo",
		UserShell:      "/bin/bash",
		IPAddress:      "192.168.1.10",
		CIDR:           24,
		Gateway:        "192.168.1.1",
		DNS:            []string{"8.8.8.8", "8.8.4.4"},
	}

	userData, err := gen.GenerateUserData(input)
	if err != nil {
		t.Fatalf("GenerateUserData() failed: %v", err)
	}

	if !strings.Contains(userData, "password: \"!\"") {
		t.Error("Expected password to be disabled when PasswordHash is empty")
	}
	if !strings.Contains(userData, "nameserver 8.8.8.8") || !strings.Contains(userData, "nameserver 8.8.4.4") {
		t.Error("Expected resolv.conf early-commands to include all DNS servers")
	}
}

func TestGenerateUserData_WithDataDisk(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	input := &UserDataInput{
		Hostname:          "test-vm",
		Username:          "ubuntu",
		PasswordHash:      "$6$rounds=5000$salt$hash",
		SSHPublicKeys:     []string{"ssh-ed25519 AAAA... test@example.com"},
		Locale:            "en_US.UTF-8",
		Timezone:          "UTC",
		KeyboardLayout:    "us",
		SwapSize:          "2G",
		SwapSizeGB:        2,
		Packages:          []string{"open-vm-tools"},
		UserGroups:        "sudo",
		UserShell:         "/bin/bash",
		DataDiskMountPath: "/data",
		IPAddress:         "192.168.1.10",
		CIDR:              24,
		Gateway:           "192.168.1.1",
		DNS:               []string{"8.8.8.8"},
	}

	userData, err := gen.GenerateUserData(input)
	if err != nil {
		t.Fatalf("GenerateUserData() failed: %v", err)
	}

	if !strings.Contains(userData, "path: /dev/sdb") {
		t.Error("Expected data disk section to be present")
	}
	if !strings.Contains(userData, "path: /data") {
		t.Error("Expected data disk mount path to be present")
	}
}

func TestGenerateUserData_AllowPasswordSSH(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	input := &UserDataInput{
		Hostname:         "test-vm",
		Username:         "ubuntu",
		PasswordHash:     "$6$rounds=5000$salt$hash",
		SSHPublicKeys:    []string{"ssh-ed25519 AAAA... test@example.com"},
		AllowPasswordSSH: true,
		Locale:           "en_US.UTF-8",
		Timezone:         "UTC",
		KeyboardLayout:   "us",
		SwapSize:         "2G",
		SwapSizeGB:       2,
		Packages:         []string{"open-vm-tools"},
		UserGroups:       "sudo",
		UserShell:        "/bin/bash",
		IPAddress:        "192.168.1.10",
		CIDR:             24,
		Gateway:          "192.168.1.1",
		DNS:              []string{"8.8.8.8"},
	}

	userData, err := gen.GenerateUserData(input)
	if err != nil {
		t.Fatalf("GenerateUserData() failed: %v", err)
	}

	if !strings.Contains(userData, "allow-pw: true") {
		t.Error("Expected allow-pw: true when AllowPasswordSSH is set")
	}
}

func TestGenerateUserData_EmptyDNSFails(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	input := &UserDataInput{
		Hostname:       "test-vm",
		Username:       "ubuntu",
		PasswordHash:   "$6$rounds=5000$salt$hash",
		SSHPublicKeys:  []string{"ssh-ed25519 AAAA... test@example.com"},
		Locale:         "en_US.UTF-8",
		Timezone:       "UTC",
		KeyboardLayout: "us",
		SwapSize:       "2G",
		SwapSizeGB:     2,
		Packages:       []string{"open-vm-tools"},
		UserGroups:     "sudo",
		UserShell:      "/bin/bash",
		IPAddress:      "192.168.1.10",
		CIDR:           24,
		Gateway:        "192.168.1.1",
		DNS:            []string{},
	}

	_, err = gen.GenerateUserData(input)
	if err == nil {
		t.Fatal("expected error when DNS is empty")
	}
}

func TestGenerateUserData_NoSwap(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	input := &UserDataInput{
		Hostname:       "test-vm",
		Username:       "ubuntu",
		PasswordHash:   "$6$rounds=5000$salt$hash",
		SSHPublicKeys:  []string{"ssh-ed25519 AAAA... test@example.com"},
		Locale:         "en_US.UTF-8",
		Timezone:       "UTC",
		KeyboardLayout: "us",
		SwapSize:       "0G",
		SwapSizeGB:     0,
		Packages:       []string{"open-vm-tools"},
		UserGroups:     "sudo",
		UserShell:      "/bin/bash",
		IPAddress:      "192.168.1.10",
		CIDR:           24,
		Gateway:        "192.168.1.1",
		DNS:            []string{"8.8.8.8"},
	}

	userData, err := gen.GenerateUserData(input)
	if err != nil {
		t.Fatalf("GenerateUserData() failed: %v", err)
	}
	if strings.Contains(userData, "swap-partition") {
		t.Error("Expected swap partition to be omitted when swap size is 0")
	}
	if strings.Contains(userData, "swap-format") || strings.Contains(userData, "swap-mount") {
		t.Error("Expected swap format/mount to be omitted when swap size is 0")
	}
}

func TestGenerateMetaData_BadTemplate(t *testing.T) {
	gen := &Generator{
		metaDataTmpl: template.Must(template.New("meta").Parse("{{ index . 1 }}")),
	}

	_, err := gen.GenerateMetaData(&MetaDataInput{
		InstanceID: "i-123",
		Hostname:   "test-vm",
	})
	if err == nil {
		t.Fatal("expected error from bad meta-data template")
	}
}

func TestGenerateNetworkConfig_BadTemplate(t *testing.T) {
	gen := &Generator{
		networkConfigTmpl: template.Must(template.New("net").Parse("{{ index . 1 }}")),
	}

	_, err := gen.GenerateNetworkConfig(&NetworkConfigInput{
		InterfaceName: "ens192",
		IPAddress:     "192.168.1.10",
		CIDR:          24,
		Gateway:       "192.168.1.1",
		DNS:           []string{"8.8.8.8"},
	})
	if err == nil {
		t.Fatal("expected error from bad network-config template")
	}
}

func TestGenerateMetaData(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	input := &MetaDataInput{
		InstanceID: "i-12345678",
		Hostname:   "test-vm",
	}

	metaData, err := gen.GenerateMetaData(input)
	if err != nil {
		t.Fatalf("GenerateMetaData() failed: %v", err)
	}

	// Verify generated YAML contains expected values
	if !strings.Contains(metaData, "instance-id: i-12345678") {
		t.Error("Generated meta-data missing instance-id")
	}
	if !strings.Contains(metaData, "local-hostname: test-vm") {
		t.Error("Generated meta-data missing local-hostname")
	}
}

func TestGenerateNetworkConfig(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	input := &NetworkConfigInput{
		InterfaceName: "ens192",
		IPAddress:     "192.168.1.10",
		CIDR:          24,
		Gateway:       "192.168.1.1",
		DNS:           []string{"8.8.8.8", "8.8.4.4"},
	}

	networkConfig, err := gen.GenerateNetworkConfig(input)
	if err != nil {
		t.Fatalf("GenerateNetworkConfig() failed: %v", err)
	}

	// Verify generated YAML contains expected values
	if !strings.Contains(networkConfig, "ens192:") {
		t.Error("Generated network-config missing interface name")
	}
	if !strings.Contains(networkConfig, "192.168.1.10/24") {
		t.Error("Generated network-config missing IP address/CIDR")
	}
	if !strings.Contains(networkConfig, "gateway4: 192.168.1.1") {
		t.Error("Generated network-config missing gateway")
	}
	if !strings.Contains(networkConfig, "8.8.8.8") {
		t.Error("Generated network-config missing DNS server")
	}
	if !strings.Contains(networkConfig, "version: 2") {
		t.Error("Generated network-config missing version")
	}
}

func TestGenerateNetworkConfigDefaultInterface(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	input := &NetworkConfigInput{
		InterfaceName: "", // Empty - should default to ens192
		IPAddress:     "10.0.0.5",
		CIDR:          16,
		Gateway:       "10.0.0.1",
		DNS:           []string{"1.1.1.1"},
	}

	networkConfig, err := gen.GenerateNetworkConfig(input)
	if err != nil {
		t.Fatalf("GenerateNetworkConfig() failed: %v", err)
	}

	// Verify default interface name
	if !strings.Contains(networkConfig, "ens192:") {
		t.Error("Generated network-config should default to ens192")
	}
}

func TestValidateYAML(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			"Valid YAML",
			"key: value\nlist:\n  - item1\n  - item2",
			false,
		},
		{
			"Invalid YAML - unclosed bracket",
			"key: [value",
			true,
		},
		{
			"Empty YAML",
			"",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gen.ValidateYAML(tt.yaml)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateYAML() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Benchmark tests
func BenchmarkGenerateUserData(b *testing.B) {
	gen, _ := NewGenerator()
	input := &UserDataInput{
		Hostname:      "benchmark-vm",
		Username:      "ubuntu",
		SSHPublicKeys: []string{"ssh-ed25519 AAAA... bench@example.com"},
		Locale:        "en_US.UTF-8",
		Timezone:      "UTC",
	}

	for i := 0; i < b.N; i++ {
		if _, err := gen.GenerateUserData(input); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerateNetworkConfig(b *testing.B) {
	gen, _ := NewGenerator()
	input := &NetworkConfigInput{
		IPAddress: "192.168.1.10",
		CIDR:      24,
		Gateway:   "192.168.1.1",
		DNS:       []string{"8.8.8.8"},
	}

	for i := 0; i < b.N; i++ {
		if _, err := gen.GenerateNetworkConfig(input); err != nil {
			b.Fatal(err)
		}
	}
}

func TestGenerateNetworkConfig_ExtraNICsNoGateway(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}
	out, err := gen.GenerateNetworkConfig(&NetworkConfigInput{
		InterfaceName: "ens192",
		IPAddress:     "192.168.1.10",
		CIDR:          24,
		Gateway:       "192.168.1.1",
		DNS:           []string{"8.8.8.8"},
		ExtraNICs: []ExtraNIC{
			{MACAddress: "00:50:56:aa:bb:01", IPAddress: "10.0.0.5", CIDR: 24},
			{MACAddress: "00:50:56:aa:bb:02", IPAddress: "10.0.1.5", CIDR: 16},
		},
	})
	if err != nil {
		t.Fatalf("GenerateNetworkConfig() failed: %v", err)
	}
	for _, want := range []string{"extra0:", "extra1:", `macaddress: "00:50:56:aa:bb:01"`, "10.0.0.5/24", "10.0.1.5/16", "optional: true"} {
		if !strings.Contains(out, want) {
			t.Errorf("network-config missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "gateway4"); n != 1 {
		t.Errorf("gateway4 must appear only for the primary NIC, got %d:\n%s", n, out)
	}
}

func TestGenerateNetworkConfig_NoExtraNICsUnchanged(t *testing.T) {
	gen, _ := NewGenerator()
	out, err := gen.GenerateNetworkConfig(&NetworkConfigInput{
		InterfaceName: "ens192", IPAddress: "192.168.1.10", CIDR: 24, Gateway: "192.168.1.1", DNS: []string{"8.8.8.8"},
	})
	if err != nil {
		t.Fatalf("GenerateNetworkConfig() failed: %v", err)
	}
	if strings.Contains(out, "extra0") || strings.Contains(out, "match:") || strings.Contains(out, "optional") {
		t.Errorf("unexpected extra NIC output:\n%s", out)
	}
}

func TestGenerateUserData_ExtraNICsNoGateway(t *testing.T) {
	gen, err := NewGenerator()
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}
	out, err := gen.GenerateUserData(&UserDataInput{
		Hostname: "vm", Username: "ubuntu", PasswordHash: "*",
		SSHPublicKeys: []string{"ssh-ed25519 AAAA test"}, Locale: "en_US.UTF-8", Timezone: "UTC",
		KeyboardLayout: "us", SwapSize: "2G", SwapSizeGB: 2, UserGroups: "sudo", UserShell: "/bin/bash",
		IPAddress: "192.168.1.10", CIDR: 24, Gateway: "192.168.1.1", DNS: []string{"8.8.8.8"},
		ExtraNICs: []ExtraNIC{{MACAddress: "00:50:56:aa:bb:01", IPAddress: "10.0.0.5", CIDR: 24}},
	})
	if err != nil {
		t.Fatalf("GenerateUserData() failed: %v", err)
	}
	for _, want := range []string{"extra0:", `macaddress: "00:50:56:aa:bb:01"`, "10.0.0.5/24", "optional: true"} {
		if !strings.Contains(out, want) {
			t.Errorf("user-data missing %q", want)
		}
	}
	if n := strings.Count(out, "optional: true"); n != 1 {
		t.Errorf("optional must be set only on the extra NIC, got %d", n)
	}
	if n := strings.Count(out, "via: "); n != 1 {
		t.Errorf("default route must exist only for the primary NIC, got %d", n)
	}
}
