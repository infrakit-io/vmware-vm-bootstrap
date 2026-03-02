package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withFakeGovc(t *testing.T, script string) {
	t.Helper()
	tmp := t.TempDir()
	govcPath := filepath.Join(tmp, "govc")
	if err := os.WriteFile(govcPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake govc: %v", err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+oldPath)
}

func validTalosOVAConfig() *VMConfig {
	return &VMConfig{
		VCenterHost:      "vc.example.local",
		VCenterUsername:  "user",
		VCenterPassword:  "pass",
		VCenterInsecure:  true,
		Name:             "talos-vm-01",
		CPUs:             2,
		MemoryMB:         4096,
		DiskSizeGB:       20,
		NetworkName:      "LAN",
		IPAddress:        "192.168.1.10",
		Netmask:          "255.255.255.0",
		Gateway:          "192.168.1.1",
		DNS:              []string{"1.1.1.1"},
		Datacenter:       "DC1",
		Datastore:        "DS1",
		Profile:          "talos",
		ContentLibraryID: "lib-1",
		Profiles: VMProfiles{
			Talos: TalosProfile{
				Version:     "v1.12.4",
				SchematicID: "903b2da78f99adef03cbbd4df6714563823f63218508800751560d3bc3557e40",
			},
		},
	}
}

func TestCreateTalosNodeFromOVA_RequiresTalosProfile(t *testing.T) {
	cfg := validTalosOVAConfig()
	cfg.Profile = "ubuntu"

	_, err := CreateTalosNodeFromOVA(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "requires Profile=talos") {
		t.Fatalf("expected profile error, got: %v", err)
	}
}

func TestCreateTalosNodeFromOVA_ValidateConfigError(t *testing.T) {
	cfg := validTalosOVAConfig()
	cfg.NetworkName = ""

	_, err := CreateTalosNodeFromOVA(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid config") {
		t.Fatalf("expected validation error, got: %v", err)
	}
}

func TestCreateTalosNodeFromOVA_ImportSpecFailure(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "library.info" ]]; then
  echo "null"
  exit 0
fi
if [[ "${1:-}" == "library.import" ]]; then
  exit 0
fi
if [[ "${1:-}" == "import.spec" ]]; then
  echo "import.spec failed from fake govc" >&2
  exit 1
fi
echo "unexpected govc call: $*" >&2
exit 1
`
	withFakeGovc(t, script)

	cfg := validTalosOVAConfig()
	_, err := CreateTalosNodeFromOVA(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "govc import.spec failed") {
		t.Fatalf("expected import.spec error, got: %v", err)
	}
}

func TestCreateTalosNodeFromOVA_ImportSpecParseError(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "library.info" ]]; then
  echo '{"name":"already-there"}'
  exit 0
fi
if [[ "${1:-}" == "import.spec" ]]; then
  echo 'not-json'
  exit 0
fi
echo "unexpected govc call: $*" >&2
exit 1
`
	withFakeGovc(t, script)

	cfg := validTalosOVAConfig()
	_, err := CreateTalosNodeFromOVA(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "parse import spec") {
		t.Fatalf("expected import spec parse error, got: %v", err)
	}
}

func TestCreateTalosNodeFromOVA_EnsureItemError(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "library.info" ]]; then
  echo "govc: matches 0 items" >&2
  exit 1
fi
echo "unexpected govc call: $*" >&2
exit 1
`
	withFakeGovc(t, script)

	cfg := validTalosOVAConfig()
	_, err := CreateTalosNodeFromOVA(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "ensure library item") {
		t.Fatalf("expected ensure item error, got: %v", err)
	}
}

func TestCreateTalosNodeFromOVA_DeployThenPowerOnFail(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "library.info" ]]; then
  echo '{"name":"already-there"}'
  exit 0
fi
if [[ "${1:-}" == "import.spec" ]]; then
  echo '{}'
  exit 0
fi
if [[ "${1:-}" == "library.deploy" ]]; then
  exit 0
fi
if [[ "${1:-}" == "vm.power" ]]; then
  echo "power on failed in fake govc" >&2
  exit 1
fi
echo "unexpected govc call: $*" >&2
exit 1
`
	withFakeGovc(t, script)

	cfg := validTalosOVAConfig()
	_, err := CreateTalosNodeFromOVA(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to power on Talos VM") {
		t.Fatalf("expected power-on failure, got: %v", err)
	}
}

func TestCreateTalosNodeFromOVA_RecoverInvalidLibraryItem(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "govc-deploy-count")
	t.Setenv("GOVC_FAKE_STATE", stateFile)
	script := `#!/usr/bin/env bash
set -euo pipefail
state="${GOVC_FAKE_STATE}"
if [[ "${1:-}" == "library.info" ]]; then
  echo '{"name":"already-there"}'
  exit 0
fi
if [[ "${1:-}" == "import.spec" ]]; then
  echo '{}'
  exit 0
fi
if [[ "${1:-}" == "library.deploy" ]]; then
  n=0
  if [[ -f "$state" ]]; then n=$(cat "$state"); fi
  n=$((n+1))
  echo "$n" > "$state"
  if [[ "$n" -eq 1 ]]; then
    echo "not an OVF" >&2
    exit 1
  fi
  exit 0
fi
if [[ "${1:-}" == "library.rm" ]]; then
  exit 0
fi
if [[ "${1:-}" == "library.import" ]]; then
  exit 0
fi
if [[ "${1:-}" == "vm.power" ]]; then
  echo "power on failed in fake govc" >&2
  exit 1
fi
echo "unexpected govc call: $*" >&2
exit 1
`
	withFakeGovc(t, script)

	cfg := validTalosOVAConfig()
	_, err := CreateTalosNodeFromOVA(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to power on Talos VM") {
		t.Fatalf("expected power-on failure after recovery path, got: %v", err)
	}
}

func TestCreateTalosNodeFromOVA_PostDeployVCenterConnectFails(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "library.info" ]]; then
  echo '{"name":"already-there"}'
  exit 0
fi
if [[ "${1:-}" == "import.spec" ]]; then
  echo '{}'
  exit 0
fi
if [[ "${1:-}" == "library.deploy" ]]; then
  exit 0
fi
if [[ "${1:-}" == "vm.power" ]]; then
  exit 0
fi
echo "unexpected govc call: $*" >&2
exit 1
`
	withFakeGovc(t, script)

	cfg := validTalosOVAConfig()
	cfg.VCenterHost = "https://127.0.0.1:1/sdk"

	_, err := CreateTalosNodeFromOVA(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "vCenter connection failed after OVA deploy") {
		t.Fatalf("expected vCenter connection error, got: %v", err)
	}
}
