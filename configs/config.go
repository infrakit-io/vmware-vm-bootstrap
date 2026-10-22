// Package configs provides library defaults loaded from embedded YAML files.
// All hardcoded values live in defaults.yaml and releases YAML files.
package configs

import (
	_ "embed"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed defaults.yaml
var defaultsYAML []byte

//go:embed ubuntu-releases.yaml
var releasesYAML []byte

//go:embed talos-releases.yaml
var talosReleasesYAML []byte

//go:embed talos-extensions.yaml
var talosExtensionsYAML []byte

// Defaults holds all library default values (loaded from defaults.yaml at startup).
var Defaults LibDefaults

// UbuntuReleases holds Ubuntu release download info (loaded from ubuntu-releases.yaml).
var UbuntuReleases ReleasesConfig

// TalosReleases holds Talos versions for wizard selection (loaded from talos-releases.yaml).
var TalosReleases TalosReleasesConfig

// TalosExtensions holds extension catalog + defaults for schematic wizard.
var TalosExtensions TalosExtensionsConfig

func init() {
	if err := yaml.Unmarshal(defaultsYAML, &Defaults); err != nil {
		panic("vmware-vm-bootstrap: invalid defaults.yaml: " + err.Error())
	}
	applyBuiltinFallbacks(&Defaults)
	if err := yaml.Unmarshal(releasesYAML, &UbuntuReleases); err != nil {
		panic("vmware-vm-bootstrap: invalid ubuntu-releases.yaml: " + err.Error())
	}
	if err := yaml.Unmarshal(talosReleasesYAML, &TalosReleases); err != nil {
		panic("vmware-vm-bootstrap: invalid talos-releases.yaml: " + err.Error())
	}
	if err := yaml.Unmarshal(talosExtensionsYAML, &TalosExtensions); err != nil {
		panic("vmware-vm-bootstrap: invalid talos-extensions.yaml: " + err.Error())
	}
}

// applyBuiltinFallbacks keeps one central safety net if defaults.yaml misses fields.
func applyBuiltinFallbacks(d *LibDefaults) {
	if d == nil {
		return
	}
	if d.Timeouts.DownloadMinutes <= 0 {
		d.Timeouts.DownloadMinutes = 120
	}
	if d.Talos.DefaultVersion == "" {
		d.Talos.DefaultVersion = "v1.12.4"
	}
	if d.Talos.DefaultCluster == "" {
		d.Talos.DefaultCluster = "dev"
	}
	if d.Talos.DefaultTimeoutM <= 0 {
		d.Talos.DefaultTimeoutM = 45
	}
	if d.Talos.PlanNetwork.CIDR == "" {
		d.Talos.PlanNetwork.CIDR = "192.168.110.0/24"
	}
	if d.Talos.PlanNetwork.StartIP == "" {
		d.Talos.PlanNetwork.StartIP = "192.168.110.20"
	}
	if d.Talos.PlanNetwork.Gateway == "" {
		d.Talos.PlanNetwork.Gateway = "192.168.110.1"
	}
	if d.Talos.PlanNetwork.DNS == "" {
		d.Talos.PlanNetwork.DNS = d.Talos.PlanNetwork.Gateway
	}
	if d.Talos.PlanNodeTypes.Controlplane.CPUs <= 0 {
		d.Talos.PlanNodeTypes.Controlplane.CPUs = 4
	}
	if d.Talos.PlanNodeTypes.Controlplane.MemoryGB <= 0 {
		d.Talos.PlanNodeTypes.Controlplane.MemoryGB = 8
	}
	if d.Talos.PlanNodeTypes.Controlplane.DiskSizeG <= 0 {
		d.Talos.PlanNodeTypes.Controlplane.DiskSizeG = 60
	}
	if d.Talos.PlanNodeTypes.Worker.CPUs <= 0 {
		d.Talos.PlanNodeTypes.Worker.CPUs = 8
	}
	if d.Talos.PlanNodeTypes.Worker.MemoryGB <= 0 {
		d.Talos.PlanNodeTypes.Worker.MemoryGB = 16
	}
	if d.Talos.PlanNodeTypes.Worker.DiskSizeG <= 0 {
		d.Talos.PlanNodeTypes.Worker.DiskSizeG = 100
	}
	if d.Talos.PlanLayout.ControlplaneCount <= 0 {
		d.Talos.PlanLayout.ControlplaneCount = 3
	}
	if d.Talos.PlanLayout.WorkerCount < 0 {
		d.Talos.PlanLayout.WorkerCount = 2
	}
}

// LibDefaults holds all configurable library defaults.
type LibDefaults struct {
	VCenter   VCenterDefaults   `yaml:"vcenter"`
	VM        VMDefaults        `yaml:"vm"`
	Network   NetworkDefaults   `yaml:"network"`
	Talos     TalosDefaults     `yaml:"talos"`
	CloudInit CloudInitDefaults `yaml:"cloudinit"`
	Timeouts  TimeoutDefaults   `yaml:"timeouts"`
	ISO       ISODefaults       `yaml:"iso"`
	Output    OutputDefaults    `yaml:"output"`
}

// VCenterDefaults holds vCenter connection defaults.
type VCenterDefaults struct {
	Port int `yaml:"port"`
}

// VMDefaults holds VM hardware defaults.
type VMDefaults struct {
	Firmware string `yaml:"firmware"`
	GuestOS  string `yaml:"guest_os"`
}

// NetworkDefaults holds network configuration defaults.
type NetworkDefaults struct {
	Interface string `yaml:"interface"`
}

// TalosDefaults holds Talos-specific defaults used by wizards.
type TalosDefaults struct {
	DefaultVersion  string             `yaml:"default_version"`
	DefaultCluster  string             `yaml:"default_cluster_name"`
	DefaultTimeoutM int                `yaml:"default_timeout_minutes"`
	PlanNetwork     TalosPlanNetwork   `yaml:"plan_network"`
	PlanNodeTypes   TalosPlanNodeTypes `yaml:"plan_node_types"`
	PlanLayout      TalosPlanLayout    `yaml:"plan_layout"`
}

type TalosPlanNetwork struct {
	CIDR    string `yaml:"cidr"`
	StartIP string `yaml:"start_ip"`
	Gateway string `yaml:"gateway"`
	DNS     string `yaml:"dns"`
}

type TalosPlanNodeTypeDefaults struct {
	CPUs      int `yaml:"cpus"`
	MemoryGB  int `yaml:"memory_gb"`
	DiskSizeG int `yaml:"disk_size_gb"`
}

type TalosPlanNodeTypes struct {
	Controlplane TalosPlanNodeTypeDefaults `yaml:"controlplane"`
	Worker       TalosPlanNodeTypeDefaults `yaml:"worker"`
}

type TalosPlanLayout struct {
	ControlplaneCount int `yaml:"controlplane_count"`
	WorkerCount       int `yaml:"worker_count"`
}

// CloudInitDefaults holds cloud-init configuration defaults.
type CloudInitDefaults struct {
	Locale         string   `yaml:"locale"`
	Timezone       string   `yaml:"timezone"`
	KeyboardLayout string   `yaml:"keyboard_layout"`
	SwapSizeGB     int      `yaml:"swap_size_gb"`
	Packages       []string `yaml:"packages"`
	UserGroups     string   `yaml:"user_groups"`
	UserShell      string   `yaml:"user_shell"`
}

// TimeoutDefaults holds all timeout and retry values.
type TimeoutDefaults struct {
	InstallationMinutes    int `yaml:"installation_minutes"`
	PollingSeconds         int `yaml:"polling_seconds"`
	HostnameChecks         int `yaml:"hostname_checks"`
	ServiceStartupSeconds  int `yaml:"service_startup_seconds"`
	SSHRetries             int `yaml:"ssh_retries"`
	SSHConnectSeconds      int `yaml:"ssh_connect_seconds"`
	SSHRetryDelaySeconds   int `yaml:"ssh_retry_delay_seconds"`
	HardwareInitSeconds    int `yaml:"hardware_init_seconds"`
	DownloadMinutes        int `yaml:"download_minutes"`
	UploadProgressSeconds  int `yaml:"upload_progress_seconds"`
	ExtractProgressSeconds int `yaml:"extract_progress_seconds"`
}

// As time.Duration convenience methods.

func (t TimeoutDefaults) Installation() time.Duration {
	return time.Duration(t.InstallationMinutes) * time.Minute
}
func (t TimeoutDefaults) Polling() time.Duration {
	return time.Duration(t.PollingSeconds) * time.Second
}
func (t TimeoutDefaults) ServiceStartup() time.Duration {
	return time.Duration(t.ServiceStartupSeconds) * time.Second
}
func (t TimeoutDefaults) SSHConnect() time.Duration {
	return time.Duration(t.SSHConnectSeconds) * time.Second
}
func (t TimeoutDefaults) SSHRetryDelay() time.Duration {
	return time.Duration(t.SSHRetryDelaySeconds) * time.Second
}
func (t TimeoutDefaults) HardwareInit() time.Duration {
	return time.Duration(t.HardwareInitSeconds) * time.Second
}
func (t TimeoutDefaults) Download() time.Duration {
	return time.Duration(t.DownloadMinutes) * time.Minute
}
func (t TimeoutDefaults) UploadProgress() time.Duration {
	return time.Duration(t.UploadProgressSeconds) * time.Second
}
func (t TimeoutDefaults) ExtractProgress() time.Duration {
	return time.Duration(t.ExtractProgressSeconds) * time.Second
}

// ISODefaults holds ISO-related defaults.
type ISODefaults struct {
	NoCloudVolumeID      string `yaml:"nocloud_volume_id"`
	GRUBTimeoutSeconds   int    `yaml:"grub_timeout_seconds"`
	CacheDir             string `yaml:"cache_dir"`
	UbuntuModifiedSuffix string `yaml:"ubuntu_modified_suffix"`
	ExtractDirName       string `yaml:"extract_dir_name"`
	UbuntuVolumeID       string `yaml:"ubuntu_volume_id"`
}

// OutputDefaults holds CLI output defaults.
type OutputDefaults struct {
	Enable              bool   `yaml:"enable"`
	BootstrapResultPath string `yaml:"bootstrap_result_path"`
}

// UbuntuRelease holds download info for a single Ubuntu release.
type UbuntuRelease struct {
	URL      string `yaml:"url"`
	Checksum string `yaml:"checksum"`
}

// ReleasesConfig holds all known Ubuntu releases.
type ReleasesConfig struct {
	Releases map[string]UbuntuRelease `yaml:"releases"`
}

// TalosReleasesConfig holds Talos versions used by config wizard.
type TalosReleasesConfig struct {
	Versions []string `yaml:"versions"`
}

// TalosExtensionsConfig holds extension catalog + defaults for schematic wizard.
type TalosExtensionsConfig struct {
	FactoryURL            string   `yaml:"factory_url"`
	RecommendedExtensions []string `yaml:"recommended_extensions"`
	DefaultExtensions     []string `yaml:"default_extensions"`
	Extensions            []string `yaml:"extensions"`
}
