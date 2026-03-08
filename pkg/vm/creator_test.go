package vm

import (
	"context"
	"testing"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/vmware/govmomi/vim25/types"
)

func TestNewCreator(t *testing.T) {
	ctx := context.Background()
	creator := NewCreator(ctx)

	if creator == nil {
		t.Fatal("NewCreator() returned nil")
	}
	if creator.ctx != ctx {
		t.Error("NewCreator() did not store context")
	}
}

func TestCreateSpec_fields(t *testing.T) {
	creator := NewCreator(context.Background())
	cfg := &Config{
		Name:      "web-01",
		CPUs:      4,
		MemoryMB:  8192,
		GuestOS:   "ubuntu64Guest",
		Datastore: "SSD-Storage-01",
	}

	spec := creator.CreateSpec(cfg)

	if spec.Name != "web-01" {
		t.Errorf("Name = %q, want web-01", spec.Name)
	}
	if spec.NumCPUs != 4 {
		t.Errorf("NumCPUs = %d, want 4", spec.NumCPUs)
	}
	if spec.MemoryMB != 8192 {
		t.Errorf("MemoryMB = %d, want 8192", spec.MemoryMB)
	}
	if spec.GuestId != "ubuntu64Guest" {
		t.Errorf("GuestId = %q, want ubuntu64Guest", spec.GuestId)
	}
	if spec.Files == nil {
		t.Fatal("Files is nil")
	}
	if spec.Files.VmPathName != "[SSD-Storage-01]" {
		t.Errorf("VmPathName = %q, want [SSD-Storage-01]", spec.Files.VmPathName)
	}
}

func TestCreateSpec_defaultGuestOS(t *testing.T) {
	creator := NewCreator(context.Background())
	cfg := &Config{
		Name:      "vm",
		GuestOS:   "", // empty → should use default
		Datastore: "ds",
	}

	spec := creator.CreateSpec(cfg)

	if spec.GuestId != configs.Defaults.VM.GuestOS {
		t.Errorf("GuestId = %q, want default %q", spec.GuestId, configs.Defaults.VM.GuestOS)
	}
}

func TestCreateSpec_biosFirewall_notSet(t *testing.T) {
	creator := NewCreator(context.Background())
	cfg := &Config{
		Name:      "vm",
		Firmware:  "bios",
		Datastore: "ds",
	}

	spec := creator.CreateSpec(cfg)

	// BIOS is default — govmomi expects empty string (not "bios") for BIOS firmware
	if spec.Firmware != "" {
		t.Errorf("BIOS firmware should leave spec.Firmware empty, got %q", spec.Firmware)
	}
}

func TestCreateSpec_efiFirmware(t *testing.T) {
	creator := NewCreator(context.Background())
	cfg := &Config{
		Name:      "vm",
		Firmware:  "efi",
		Datastore: "ds",
	}

	spec := creator.CreateSpec(cfg)

	want := string(types.GuestOsDescriptorFirmwareTypeEfi)
	if spec.Firmware != want {
		t.Errorf("EFI firmware spec.Firmware = %q, want %q", spec.Firmware, want)
	}
}

func TestCreateSpec_defaultFirmwareFromConfig(t *testing.T) {
	creator := NewCreator(context.Background())
	cfg := &Config{
		Name:      "vm",
		Firmware:  "", // empty → uses configs.Defaults.VM.Firmware
		Datastore: "ds",
	}

	spec := creator.CreateSpec(cfg)

	// Default is "bios" → spec.Firmware should be empty (bios = govmomi default)
	if configs.Defaults.VM.Firmware == "bios" && spec.Firmware != "" {
		t.Errorf("Default BIOS firmware should not set spec.Firmware, got %q", spec.Firmware)
	}
}
