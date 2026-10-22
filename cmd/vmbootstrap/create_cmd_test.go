package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/simulator"
	"gopkg.in/yaml.v3"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/bootstrap"
)

const createVMYAML = `
vm:
  name: %NAME%
  cpus: 2
  memory_mb: 2048
  disk_size_gb: 20
  data_disk_size_gb: 100
  data_disk_mount_path: /data
  data_disk_datastore: HDD01
  username: ops
  ssh_key: "ssh-ed25519 AAAA test"
  ip_address: 10.0.0.10
  netmask: 255.255.255.0
  gateway: 10.0.0.1
  dns: 10.0.0.2
  datastore: SSD01
  network_name: PRIMARY
  folder: GDC
  resource_pool: pool
  profiles:
    ubuntu:
      version: "24.04"
  extra_nics:
    - {network: BACKUP, ip: 10.0.1.5, netmask: 255.255.255.0, connected: false}
  tags:
    - {category: Backup, tag: no-backup}
`

func simCreateEnv(t *testing.T) (*vcenterFileConfig, string, func()) {
	t.Helper()
	model := simulator.VPX()
	require.NoError(t, model.Create())
	model.Service.TLS = new(tls.Config)
	s := model.Service.NewServer()
	u, err := url.Parse(s.URL.String())
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	var vc vcenterFileConfig
	vc.VCenter.Host = u.Hostname()
	vc.VCenter.Port = port
	vc.VCenter.Username = simulator.DefaultLogin.Username()
	vc.VCenter.Password, _ = simulator.DefaultLogin.Password()
	vc.VCenter.Datacenter = "DC0"
	vc.VCenter.Insecure = true
	return &vc, "DC0_H0_VM0", func() { s.Close(); model.Remove() }
}

func vmFileFor(t *testing.T, name string) *vmFileConfig {
	t.Helper()
	var f vmFileConfig
	y := bytes.ReplaceAll([]byte(createVMYAML), []byte("%NAME%"), []byte(name))
	require.NoError(t, yaml.Unmarshal(y, &f))
	return &f
}

func testDeps(out *bytes.Buffer, runCalls, confirmCalls *int) createDeps {
	d := defaultCreateDeps(out)
	d.confirm = func(string) bool { *confirmCalls++; return true }
	d.run = func(ctx context.Context, cfg *bootstrap.VMConfig) (*bootstrap.VM, error) {
		*runCalls++
		return &bootstrap.VM{Name: cfg.Name, IPAddress: cfg.IPAddress}, nil
	}
	return d
}

func TestCreateCmd_FlagsParse(t *testing.T) {
	require.NoError(t, createCmd.Flags().Parse([]string{"--config", "vm.x.sops.yaml", "--yes", "--dry-run"}))
	cfg, _ := createCmd.Flags().GetString("config")
	yes, _ := createCmd.Flags().GetBool("yes")
	dry, _ := createCmd.Flags().GetBool("dry-run")
	require.Equal(t, "vm.x.sops.yaml", cfg)
	require.True(t, yes)
	require.True(t, dry)
	require.NotNil(t, rootCmd.PersistentFlags().Lookup("vcenter-config"), "--vcenter-config is shared with nic/run")
	found, _, err := rootCmd.Find([]string{"create"})
	require.NoError(t, err)
	require.Equal(t, createCmd, found)
}

func TestCreateCmd_ExistingVMErrorsAndChangesNothing(t *testing.T) {
	vc, existing, cleanup := simCreateEnv(t)
	defer cleanup()
	var out bytes.Buffer
	runs, confirms := 0, 0
	err := runCreate(vc, vmFileFor(t, existing), createOptions{Yes: true}, testDeps(&out, &runs, &confirms))
	require.ErrorContains(t, err, "already exists")
	require.Zero(t, runs)
	require.Zero(t, confirms)
	require.NotContains(t, out.String(), "Delete")
}

func TestCreateCmd_DryRunPrintsPlanWithoutWrites(t *testing.T) {
	vc, _, cleanup := simCreateEnv(t)
	defer cleanup()
	var out bytes.Buffer
	runs, confirms := 0, 0
	require.NoError(t, runCreate(vc, vmFileFor(t, "bareos-01"), createOptions{DryRun: true}, testDeps(&out, &runs, &confirms)))
	require.Zero(t, runs)
	require.Zero(t, confirms)
	for _, want := range []string{"bareos-01", "24.04", "GDC", "HDD01", "BACKUP", "DISCONNECTED", "Backup/no-backup", "power off and keep"} {
		require.Contains(t, out.String(), want)
	}
	// still absent in vCenter
	exists, err := vmExists(&bootstrap.VMConfig{
		VCenterHost: vc.VCenter.Host, VCenterPort: vc.VCenter.Port, VCenterUsername: vc.VCenter.Username,
		VCenterPassword: vc.VCenter.Password, VCenterInsecure: true, Datacenter: "DC0", Name: "bareos-01",
	})
	require.NoError(t, err)
	require.False(t, exists)
}

func TestCreateCmd_YesSkipsPromptAndPromptDeclineCancels(t *testing.T) {
	vc, _, cleanup := simCreateEnv(t)
	defer cleanup()
	var out bytes.Buffer
	runs, confirms := 0, 0
	require.NoError(t, runCreate(vc, vmFileFor(t, "bareos-01"), createOptions{Yes: true}, testDeps(&out, &runs, &confirms)))
	require.Equal(t, 1, runs)
	require.Zero(t, confirms, "--yes never prompts")

	runs, confirms = 0, 0
	d := testDeps(&out, &runs, &confirms)
	d.confirm = func(string) bool { confirms++; return false }
	require.NoError(t, runCreate(vc, vmFileFor(t, "bareos-01"), createOptions{}, d))
	require.Equal(t, 1, confirms, "asks exactly once")
	require.Zero(t, runs)
}

func TestCreateCmd_FailurePrintsKeptVMOutcome(t *testing.T) {
	vc, _, cleanup := simCreateEnv(t)
	defer cleanup()
	var out bytes.Buffer
	runs, confirms := 0, 0
	d := testDeps(&out, &runs, &confirms)
	d.run = func(ctx context.Context, cfg *bootstrap.VMConfig) (*bootstrap.VM, error) {
		return nil, &bootstrap.KeptVMError{Name: cfg.Name, PoweredOff: true, Err: errors.New("boom")}
	}
	err := runCreate(vc, vmFileFor(t, "bareos-01"), createOptions{Yes: true}, d)
	require.ErrorContains(t, err, "bootstrap failed")
	require.Contains(t, out.String(), "VM kept (powered off): bareos-01")
}
