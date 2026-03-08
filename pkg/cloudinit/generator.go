// Package cloudinit provides cloud-init configuration generation.
package cloudinit

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"gopkg.in/yaml.v3"
)

//go:embed templates/autoinstall-user-data.yaml.tmpl
var autoinstallTemplate string

//go:embed templates/meta-data.yaml.tmpl
var metaDataTemplate string

//go:embed templates/network-config.yaml.tmpl
var networkConfigTemplate string

// Generator generates cloud-init configuration files.
type Generator struct {
	autoinstallTmpl   *template.Template
	metaDataTmpl      *template.Template
	networkConfigTmpl *template.Template
}

// UserDataInput contains data for autoinstall user-data generation.
type UserDataInput struct {
	Hostname         string
	Username         string
	PasswordHash     string // Optional - bcrypt or SHA-512
	SSHPublicKeys    []string
	AllowPasswordSSH bool
	Locale           string   // e.g., "en_US.UTF-8"
	Timezone         string   // e.g., "UTC"
	KeyboardLayout   string   // e.g., "us"
	SwapSize         string   // e.g., "2G", "4G" - used in explicit storage config
	SwapSizeGB       int      // 0 disables swap partition
	Packages         []string // e.g., ["open-vm-tools", "curl"]
	UserGroups       string   // e.g., "sudo,adm,dialout"
	UserShell        string   // e.g., "/bin/bash"
	InterfaceName    string   // Guest NIC name (e.g., "ens192")
	// Data disk mount point (empty = no data disk, uses layout:lvm)
	DataDiskMountPath string // e.g., "/data"
	// Network (required for package installation during autoinstall)
	IPAddress string
	CIDR      int
	Gateway   string
	DNS       []string
}

// MetaDataInput contains data for meta-data generation.
type MetaDataInput struct {
	InstanceID string
	Hostname   string
}

// NetworkConfigInput contains data for network-config generation.
type NetworkConfigInput struct {
	InterfaceName string   // e.g., "ens192" (default for VMware)
	IPAddress     string   // e.g., "192.168.1.10"
	CIDR          int      // e.g., 24
	Gateway       string   // e.g., "192.168.1.1"
	DNS           []string // e.g., ["8.8.8.8", "8.8.4.4"]
}

// NewGenerator creates a new cloud-init generator with embedded templates.
func NewGenerator() (*Generator, error) {
	// Parse autoinstall template
	autoinstallTmpl, err := template.New("autoinstall").Parse(autoinstallTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse autoinstall template: %w", err)
	}

	// Parse meta-data template
	metaDataTmpl, err := template.New("meta-data").Parse(metaDataTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse meta-data template: %w", err)
	}

	// Parse network-config template
	networkConfigTmpl, err := template.New("network-config").Parse(networkConfigTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse network-config template: %w", err)
	}

	return &Generator{
		autoinstallTmpl:   autoinstallTmpl,
		metaDataTmpl:      metaDataTmpl,
		networkConfigTmpl: networkConfigTmpl,
	}, nil
}

// GenerateUserData generates autoinstall user-data YAML.
func (g *Generator) GenerateUserData(input *UserDataInput) (string, error) {
	// Set default interface name if not provided
	if input.InterfaceName == "" {
		input.InterfaceName = configs.Defaults.Network.Interface
	}

	var buf bytes.Buffer
	if err := g.autoinstallTmpl.Execute(&buf, input); err != nil {
		return "", fmt.Errorf("failed to execute autoinstall template: %w", err)
	}

	content := buf.String()

	// Validate YAML syntax
	if err := g.ValidateYAML(content); err != nil {
		return "", fmt.Errorf("generated user-data is invalid YAML: %w", err)
	}

	return content, nil
}

// GenerateMetaData generates cloud-init meta-data.
func (g *Generator) GenerateMetaData(input *MetaDataInput) (string, error) {
	var buf bytes.Buffer
	if err := g.metaDataTmpl.Execute(&buf, input); err != nil {
		return "", fmt.Errorf("failed to execute meta-data template: %w", err)
	}

	content := buf.String()

	// Validate YAML syntax
	if err := g.ValidateYAML(content); err != nil {
		return "", fmt.Errorf("generated meta-data is invalid YAML: %w", err)
	}

	return content, nil
}

// GenerateNetworkConfig generates network-config YAML (Netplan v2).
func (g *Generator) GenerateNetworkConfig(input *NetworkConfigInput) (string, error) {
	// Set default interface name if not provided (from configs/defaults.yaml)
	if input.InterfaceName == "" {
		input.InterfaceName = configs.Defaults.Network.Interface
	}

	var buf bytes.Buffer
	if err := g.networkConfigTmpl.Execute(&buf, input); err != nil {
		return "", fmt.Errorf("failed to execute network-config template: %w", err)
	}

	content := buf.String()

	// Validate YAML syntax
	if err := g.ValidateYAML(content); err != nil {
		return "", fmt.Errorf("generated network-config is invalid YAML: %w", err)
	}

	return content, nil
}

// ValidateYAML validates YAML syntax.
func (g *Generator) ValidateYAML(content string) error {
	var data interface{}
	if err := yaml.Unmarshal([]byte(content), &data); err != nil {
		return fmt.Errorf("YAML validation failed: %w", err)
	}
	return nil
}
