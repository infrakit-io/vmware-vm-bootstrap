package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/bootstrap"
)

const extrasYAML = `
vm:
  name: bareos-01
  data_disk_size_gb: 500
  data_disk_datastore: HDD01
  cpu_hot_add: true
  memory_hot_add: false
  extra_nics:
    - {network: BACKUP, ip: 10.0.0.5, netmask: 255.255.255.0, connected: false}
    - {network: MGMT2, ip: 10.0.1.5, netmask: 255.255.255.0, allow_guest_control: true}
  tags:
    - {category: Backup, tag: no-backup}
`

func TestVMFile_ExtrasParseAndApply(t *testing.T) {
	var f vmFileConfig
	require.NoError(t, yaml.Unmarshal([]byte(extrasYAML), &f))
	v := f.VM

	cfg := &bootstrap.VMConfig{}
	applyVMExtras(cfg, v.DataDiskDatastore, v.CPUHotAdd, v.MemoryHotAdd, v.ExtraNICs, v.Tags)

	require.Equal(t, "HDD01", cfg.DataDiskDatastore)
	require.NotNil(t, cfg.CPUHotAdd)
	require.True(t, *cfg.CPUHotAdd)
	require.NotNil(t, cfg.MemoryHotAdd)
	require.False(t, *cfg.MemoryHotAdd)
	require.Equal(t, []bootstrap.NIC{
		{Network: "BACKUP", IP: "10.0.0.5", Netmask: "255.255.255.0", Connected: false, AllowGuestControl: false}, // default false
		{Network: "MGMT2", IP: "10.0.1.5", Netmask: "255.255.255.0", Connected: true, AllowGuestControl: true},    // omitted connected => true
	}, cfg.ExtraNICs)
	require.Equal(t, []bootstrap.Tag{{Category: "Backup", Tag: "no-backup"}}, cfg.Tags)
}

func TestVMFile_ExtrasAbsentLeavesDefaults(t *testing.T) {
	var f vmFileConfig
	require.NoError(t, yaml.Unmarshal([]byte("vm:\n  name: x\n"), &f))
	v := f.VM
	cfg := &bootstrap.VMConfig{}
	applyVMExtras(cfg, v.DataDiskDatastore, v.CPUHotAdd, v.MemoryHotAdd, v.ExtraNICs, v.Tags)
	require.Nil(t, cfg.CPUHotAdd)
	require.Nil(t, cfg.MemoryHotAdd)
	require.Empty(t, cfg.ExtraNICs)
	require.Empty(t, cfg.Tags)
	require.Empty(t, cfg.DataDiskDatastore)
}

func TestVMWizardOutput_PreservesExtrasOnRoundTrip(t *testing.T) {
	var w VMWizardOutput
	require.NoError(t, yaml.Unmarshal([]byte(extrasYAML), &w))
	out, err := yaml.Marshal(w)
	require.NoError(t, err)
	var back vmFileConfig
	require.NoError(t, yaml.Unmarshal(out, &back))
	require.Equal(t, "HDD01", back.VM.DataDiskDatastore)
	require.Len(t, back.VM.ExtraNICs, 2)
	require.Nil(t, back.VM.ExtraNICs[1].Connected, "omitted stays omitted")
	require.Len(t, back.VM.Tags, 1)
	require.NotNil(t, back.VM.CPUHotAdd)
}

func TestVMFile_CleanupOnErrorOptIn(t *testing.T) {
	var f vmFileConfig
	require.NoError(t, yaml.Unmarshal([]byte("vm:\n  name: x\n"), &f))
	require.False(t, f.VM.CleanupOnError, "default must not destroy the VM")
	require.NoError(t, yaml.Unmarshal([]byte("vm:\n  cleanup_on_error: true\n"), &f))
	require.True(t, f.VM.CleanupOnError)
}

func TestKeptVMMessage(t *testing.T) {
	require.Contains(t, keptVMMessage(&bootstrap.KeptVMError{Name: "vm1", PoweredOff: true}), "VM kept (powered off): vm1")
	require.Contains(t, keptVMMessage(&bootstrap.KeptVMError{Name: "vm1"}), "VM kept (power-off FAILED, still running): vm1")
}
