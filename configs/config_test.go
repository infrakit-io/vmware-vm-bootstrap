package configs

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDefaultsLoaded(t *testing.T) {
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"VCenter.Port", Defaults.VCenter.Port, 443},
		{"VM.Firmware", Defaults.VM.Firmware, "bios"},
		{"VM.GuestOS", Defaults.VM.GuestOS, "ubuntu64Guest"},
		{"Network.Interface", Defaults.Network.Interface, "ens192"},
		{"Talos.DefaultVersion", Defaults.Talos.DefaultVersion, "v1.12.4"},
		{"Talos.DefaultCluster", Defaults.Talos.DefaultCluster, "dev"},
		{"Talos.DefaultTimeoutM", Defaults.Talos.DefaultTimeoutM, 45},
		{"CloudInit.Locale", Defaults.CloudInit.Locale, "en_US.UTF-8"},
		{"CloudInit.Timezone", Defaults.CloudInit.Timezone, "UTC"},
		{"ISO.NoCloudVolumeID", Defaults.ISO.NoCloudVolumeID, "CIDATA"},
		{"ISO.CacheDir", Defaults.ISO.CacheDir, "cache"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestTalosPlanNetworkDefaultsLoaded(t *testing.T) {
	n := Defaults.Talos.PlanNetwork
	if n.CIDR == "" || n.StartIP == "" || n.Gateway == "" || n.DNS == "" {
		t.Fatalf("talos.plan_network defaults must be fully populated, got %+v", n)
	}
}

func TestTimeoutDurationsPositive(t *testing.T) {
	d := Defaults.Timeouts

	durations := []struct {
		name string
		got  time.Duration
	}{
		{"Installation", d.Installation()},
		{"Polling", d.Polling()},
		{"ServiceStartup", d.ServiceStartup()},
		{"SSHConnect", d.SSHConnect()},
		{"SSHRetryDelay", d.SSHRetryDelay()},
		{"HardwareInit", d.HardwareInit()},
		{"Download", d.Download()},
		{"UploadProgress", d.UploadProgress()},
		{"ExtractProgress", d.ExtractProgress()},
	}

	for _, tt := range durations {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got <= 0 {
				t.Errorf("%s() = %v, want > 0", tt.name, tt.got)
			}
		})
	}
}

func TestTimeoutDurationsConversion(t *testing.T) {
	d := Defaults.Timeouts

	if d.Installation() != time.Duration(d.InstallationMinutes)*time.Minute {
		t.Error("Installation() conversion mismatch")
	}
	if d.Download() != time.Duration(d.DownloadMinutes)*time.Minute {
		t.Error("Download() conversion mismatch")
	}
	if d.Polling() != time.Duration(d.PollingSeconds)*time.Second {
		t.Error("Polling() conversion mismatch")
	}
	if d.ServiceStartup() != time.Duration(d.ServiceStartupSeconds)*time.Second {
		t.Error("ServiceStartup() conversion mismatch")
	}
}

func TestUbuntuReleasesLoaded(t *testing.T) {
	if len(UbuntuReleases.Releases) == 0 {
		t.Fatal("ubuntu-releases.yaml loaded no releases")
	}

	for _, version := range []string{"26.04", "24.04", "22.04"} {
		t.Run("Ubuntu "+version, func(t *testing.T) {
			r, ok := UbuntuReleases.Releases[version]
			if !ok {
				t.Fatalf("Ubuntu %s not found in releases", version)
			}
			if r.URL == "" {
				t.Errorf("Ubuntu %s has no download URL", version)
			}
		})
	}
}

// Every shipped release must carry a SHA256 checksum: DownloadUbuntu refuses
// a release without one, so an empty value here would make that version
// unusable rather than silently unverified.
func TestUbuntuReleasesAllHaveSHA256(t *testing.T) {
	sha256Hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for version, r := range UbuntuReleases.Releases {
		if !sha256Hex.MatchString(r.Checksum) {
			t.Errorf("Ubuntu %s checksum %q is not a lowercase SHA256 hex digest", version, r.Checksum)
		}
		if !strings.HasPrefix(r.URL, "https://") {
			t.Errorf("Ubuntu %s URL %q is not https", version, r.URL)
		}
	}
}

func TestTalosReleasesLoaded(t *testing.T) {
	if len(TalosReleases.Versions) == 0 {
		t.Fatal("talos-releases.yaml loaded no versions")
	}
	for i, v := range TalosReleases.Versions {
		if v == "" {
			t.Fatalf("TalosReleases.Versions[%d] is empty", i)
		}
		if v[0] != 'v' {
			t.Fatalf("Talos version must start with 'v': %q", v)
		}
	}
}

func TestTalosExtensionsLoaded(t *testing.T) {
	if TalosExtensions.FactoryURL == "" {
		t.Fatal("talos-extensions.yaml missing factory_url")
	}
	if len(TalosExtensions.RecommendedExtensions) == 0 {
		t.Fatal("talos-extensions.yaml loaded no recommended extensions")
	}
	if len(TalosExtensions.DefaultExtensions) == 0 {
		t.Fatal("talos-extensions.yaml loaded no default extensions")
	}
	if len(TalosExtensions.Extensions) == 0 {
		t.Fatal("talos-extensions.yaml loaded no extensions")
	}
}

func TestApplyBuiltinFallbacks_NilConfig(t *testing.T) {
	applyBuiltinFallbacks(nil)
}

func TestApplyBuiltinFallbacks_FillsMissingValues(t *testing.T) {
	cfg := &LibDefaults{}
	applyBuiltinFallbacks(cfg)

	if cfg.Talos.DefaultVersion == "" || cfg.Talos.DefaultCluster == "" || cfg.Talos.DefaultTimeoutM <= 0 {
		t.Fatalf("expected talos defaults, got %+v", cfg.Talos)
	}
	if cfg.Talos.PlanNetwork.CIDR == "" || cfg.Talos.PlanNetwork.StartIP == "" || cfg.Talos.PlanNetwork.Gateway == "" || cfg.Talos.PlanNetwork.DNS == "" {
		t.Fatalf("expected plan network defaults, got %+v", cfg.Talos.PlanNetwork)
	}
	if cfg.Talos.PlanNodeTypes.Controlplane.CPUs <= 0 || cfg.Talos.PlanNodeTypes.Worker.CPUs <= 0 {
		t.Fatalf("expected plan node type defaults, got %+v", cfg.Talos.PlanNodeTypes)
	}
	if cfg.Talos.PlanLayout.ControlplaneCount <= 0 || cfg.Talos.PlanLayout.WorkerCount < 0 {
		t.Fatalf("expected plan layout defaults, got %+v", cfg.Talos.PlanLayout)
	}
}

func TestApplyBuiltinFallbacks_PreservesExplicitValues(t *testing.T) {
	cfg := &LibDefaults{
		Talos: TalosDefaults{
			DefaultVersion:  "v9.9.9",
			DefaultCluster:  "prod",
			DefaultTimeoutM: 99,
			PlanNetwork: TalosPlanNetwork{
				CIDR:    "10.0.0.0/24",
				StartIP: "10.0.0.10",
				Gateway: "10.0.0.1",
				DNS:     "1.1.1.1",
			},
			PlanNodeTypes: TalosPlanNodeTypes{
				Controlplane: TalosPlanNodeTypeDefaults{CPUs: 2, MemoryGB: 4, DiskSizeG: 50},
				Worker:       TalosPlanNodeTypeDefaults{CPUs: 3, MemoryGB: 6, DiskSizeG: 70},
			},
			PlanLayout: TalosPlanLayout{
				ControlplaneCount: 5,
				WorkerCount:       8,
			},
		},
	}
	applyBuiltinFallbacks(cfg)

	if cfg.Talos.DefaultVersion != "v9.9.9" || cfg.Talos.DefaultCluster != "prod" || cfg.Talos.DefaultTimeoutM != 99 {
		t.Fatalf("fallbacks overwrote explicit talos defaults: %+v", cfg.Talos)
	}
	if cfg.Talos.PlanLayout.ControlplaneCount != 5 || cfg.Talos.PlanLayout.WorkerCount != 8 {
		t.Fatalf("fallbacks overwrote explicit plan layout: %+v", cfg.Talos.PlanLayout)
	}
	if cfg.Talos.PlanNetwork.DNS != "1.1.1.1" {
		t.Fatalf("fallbacks overwrote explicit DNS: %+v", cfg.Talos.PlanNetwork)
	}
}
