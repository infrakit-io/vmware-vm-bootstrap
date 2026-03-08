package vm

import (
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// compile-time interface compliance check
var _ CreatorInterface = (*Creator)(nil)

// CreatorInterface abstracts VM creation and hardware operations.
// The real implementation uses govmomi; tests inject a mock.
type CreatorInterface interface {
	CreateSpec(cfg *Config) *types.VirtualMachineConfigSpec
	Create(folder *object.Folder, resourcePool *object.ResourcePool, datastore *object.Datastore, spec *types.VirtualMachineConfigSpec) (*object.VirtualMachine, error)
	EnsureSCSIController(vm *object.VirtualMachine) (int32, error)
	AddDisk(vm *object.VirtualMachine, datastore *object.Datastore, sizeGB int64, scsiKey int32) error
	AddNetworkAdapter(vm *object.VirtualMachine, network object.NetworkReference) error
	SetMACAddress(vm *object.VirtualMachine, mac string) (string, error) // Apply static MAC; returns normalized MAC
	GetMACAddress(vm *object.VirtualMachine) (string, error)            // Read assigned MAC from last NIC
	PowerOn(vm *object.VirtualMachine) error
	PowerOff(vm *object.VirtualMachine) error
	Delete(vm *object.VirtualMachine) error
}
