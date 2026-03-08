// Package vm provides virtual machine creation and configuration functionality.
package vm

import (
	"context"
	"fmt"
	"strings"

	"github.com/Bibi40k/vmware-vm-bootstrap/configs"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// Creator handles VM creation and hardware configuration.
type Creator struct {
	ctx context.Context
}

// NewCreator creates a new VM creator instance.
func NewCreator(ctx context.Context) *Creator {
	return &Creator{ctx: ctx}
}

// Config holds VM hardware configuration.
type Config struct {
	Name           string // VM name
	CPUs           int32  // Number of CPUs
	MemoryMB       int64  // Memory in MB
	GuestOS        string // Guest OS identifier (e.g., "ubuntu64Guest")
	Firmware       string // Firmware type: "bios" or "efi" (default: bios)
	DiskSizeGB     int64  // OS disk size in GB
	DataDiskSizeGB *int64 // Optional data disk size in GB
	NetworkName    string // Network name
	Datacenter     string // Datacenter name
	Folder         string // VM folder path
	ResourcePool   string // Resource pool path
	Datastore      string // Datastore name
}

// CreateSpec builds a VirtualMachineConfigSpec from the given configuration.
func (c *Creator) CreateSpec(cfg *Config) *types.VirtualMachineConfigSpec {
	// Default to BIOS firmware (from configs/defaults.yaml)
	firmware := cfg.Firmware
	if firmware == "" {
		firmware = configs.Defaults.VM.Firmware
	}

	// Default guest OS (from configs/defaults.yaml)
	guestOS := cfg.GuestOS
	if guestOS == "" {
		guestOS = configs.Defaults.VM.GuestOS
	}

	spec := &types.VirtualMachineConfigSpec{
		Name:     cfg.Name,
		NumCPUs:  cfg.CPUs,
		MemoryMB: cfg.MemoryMB,
		GuestId:  guestOS,
		Files: &types.VirtualMachineFileInfo{
			VmPathName: fmt.Sprintf("[%s]", cfg.Datastore),
		},
	}

	// Set firmware
	if firmware == "efi" {
		spec.Firmware = string(types.GuestOsDescriptorFirmwareTypeEfi)
	}

	return spec
}

// Create creates a VM in vCenter with the given specification.
func (c *Creator) Create(
	folder *object.Folder,
	resourcePool *object.ResourcePool,
	datastore *object.Datastore,
	spec *types.VirtualMachineConfigSpec,
) (*object.VirtualMachine, error) {
	task, err := folder.CreateVM(c.ctx, *spec, resourcePool, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create VM task: %w", err)
	}

	info, err := task.WaitForResult(c.ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("VM creation failed: %w", err)
	}

	vm := object.NewVirtualMachine(folder.Client(), info.Result.(types.ManagedObjectReference))
	return vm, nil
}

// EnsureSCSIController ensures a Paravirtual SCSI controller exists on the VM.
// Returns the controller key.
func (c *Creator) EnsureSCSIController(vm *object.VirtualMachine) (int32, error) {
	devices, err := vm.Device(c.ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get VM devices: %w", err)
	}

	// Check if SCSI controller already exists
	controllers := devices.SelectByType((*types.VirtualSCSIController)(nil))
	if len(controllers) > 0 {
		return controllers[0].GetVirtualDevice().Key, nil
	}

	// Create Paravirtual SCSI controller
	scsi := &types.ParaVirtualSCSIController{
		VirtualSCSIController: types.VirtualSCSIController{
			SharedBus: types.VirtualSCSISharingNoSharing,
			VirtualController: types.VirtualController{
				BusNumber: 0,
				VirtualDevice: types.VirtualDevice{
					Key: 1000,
				},
			},
		},
	}

	err = vm.AddDevice(c.ctx, scsi)
	if err != nil {
		return 0, fmt.Errorf("failed to add SCSI controller: %w", err)
	}

	return scsi.Key, nil
}

// AddDisk adds a virtual disk to the VM.
func (c *Creator) AddDisk(vm *object.VirtualMachine, datastore *object.Datastore, sizeGB int64, scsiKey int32) error {
	devices, err := vm.Device(c.ctx)
	if err != nil {
		return fmt.Errorf("failed to get VM devices: %w", err)
	}

	controller, err := devices.FindDiskController("scsi")
	if err != nil {
		return fmt.Errorf("failed to find SCSI controller: %w", err)
	}

	// Create disk WITHOUT hardcoded fileName - let vCenter auto-generate
	// This prevents collisions when creating multiple VMs
	disk := devices.CreateDisk(controller, datastore.Reference(), "")
	disk.CapacityInKB = sizeGB * 1024 * 1024

	// Configure backing as thin provisioned (matches Python implementation)
	if backing, ok := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo); ok {
		backing.ThinProvisioned = types.NewBool(true)
		dsRef := datastore.Reference()
		backing.Datastore = &dsRef
	}

	return vm.AddDevice(c.ctx, disk)
}

// AddNetworkAdapter adds a network adapter to the VM.
func (c *Creator) AddNetworkAdapter(vm *object.VirtualMachine, network object.NetworkReference) error {
	backing, err := network.EthernetCardBackingInfo(c.ctx)
	if err != nil {
		return fmt.Errorf("failed to get network backing info: %w", err)
	}

	devices, err := vm.Device(c.ctx)
	if err != nil {
		return fmt.Errorf("failed to get VM devices: %w", err)
	}

	netdev, err := devices.CreateEthernetCard("vmxnet3", backing)
	if err != nil {
		return fmt.Errorf("failed to create network adapter: %w", err)
	}

	return vm.AddDevice(c.ctx, netdev)
}

// SetMACAddress applies a static MAC address to the last NIC on the VM.
// Returns the normalized (lowercase) MAC that was set.
func (c *Creator) SetMACAddress(vm *object.VirtualMachine, mac string) (string, error) {
	devices, err := vm.Device(c.ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get VM devices: %w", err)
	}
	nics := devices.SelectByType((*types.VirtualEthernetCard)(nil))
	if len(nics) == 0 {
		return "", fmt.Errorf("no network adapter found")
	}
	nic := nics[len(nics)-1].(types.BaseVirtualEthernetCard)
	card := nic.GetVirtualEthernetCard()
	card.AddressType = "manual"
	card.MacAddress = strings.ToLower(mac)

	spec := types.VirtualMachineConfigSpec{
		DeviceChange: []types.BaseVirtualDeviceConfigSpec{
			&types.VirtualDeviceConfigSpec{
				Operation: types.VirtualDeviceConfigSpecOperationEdit,
				Device:    nics[len(nics)-1],
			},
		},
	}
	task, err := vm.Reconfigure(c.ctx, spec)
	if err != nil {
		return "", fmt.Errorf("failed to reconfigure VM: %w", err)
	}
	if err := task.Wait(c.ctx); err != nil {
		return "", fmt.Errorf("failed to apply MAC address: %w", err)
	}
	return strings.ToLower(mac), nil
}

// GetMACAddress reads the MAC address from the last NIC on the VM.
func (c *Creator) GetMACAddress(vm *object.VirtualMachine) (string, error) {
	devices, err := vm.Device(c.ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get VM devices: %w", err)
	}
	nics := devices.SelectByType((*types.VirtualEthernetCard)(nil))
	if len(nics) == 0 {
		return "", fmt.Errorf("no network adapter found")
	}
	card := nics[len(nics)-1].(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	return strings.ToLower(card.MacAddress), nil
}

// PowerOn powers on the VM.
func (c *Creator) PowerOn(vm *object.VirtualMachine) error {
	task, err := vm.PowerOn(c.ctx)
	if err != nil {
		return fmt.Errorf("failed to power on VM: %w", err)
	}

	return task.Wait(c.ctx)
}

// PowerOff powers off the VM gracefully.
func (c *Creator) PowerOff(vm *object.VirtualMachine) error {
	task, err := vm.PowerOff(c.ctx)
	if err != nil {
		return fmt.Errorf("failed to power off VM: %w", err)
	}

	return task.Wait(c.ctx)
}

// Delete removes the VM from vCenter.
func (c *Creator) Delete(vm *object.VirtualMachine) error {
	// Power off if running
	powerState, err := vm.PowerState(c.ctx)
	if err != nil {
		return fmt.Errorf("failed to get power state: %w", err)
	}

	if powerState == types.VirtualMachinePowerStatePoweredOn {
		if err := c.PowerOff(vm); err != nil {
			return fmt.Errorf("failed to power off before delete: %w", err)
		}
	}

	task, err := vm.Destroy(c.ctx)
	if err != nil {
		return fmt.Errorf("failed to delete VM: %w", err)
	}

	return task.Wait(c.ctx)
}
