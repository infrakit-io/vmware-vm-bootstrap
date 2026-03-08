// Package mocks provides testify-based mock implementations for testing
// without a real vCenter connection.
package mocks

import (
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"

	vm "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm"
)

// CreatorInterface is a mock for vm.CreatorInterface.
type CreatorInterface struct {
	mock.Mock
}

func (m *CreatorInterface) CreateSpec(cfg *vm.Config) *types.VirtualMachineConfigSpec {
	args := m.Called(cfg)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*types.VirtualMachineConfigSpec)
}

func (m *CreatorInterface) Create(folder *object.Folder, resourcePool *object.ResourcePool, datastore *object.Datastore, spec *types.VirtualMachineConfigSpec) (*object.VirtualMachine, error) {
	args := m.Called(folder, resourcePool, datastore, spec)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*object.VirtualMachine), args.Error(1)
}

func (m *CreatorInterface) EnsureSCSIController(v *object.VirtualMachine) (int32, error) {
	args := m.Called(v)
	return args.Get(0).(int32), args.Error(1)
}

func (m *CreatorInterface) AddDisk(v *object.VirtualMachine, datastore *object.Datastore, sizeGB int64, scsiKey int32) error {
	args := m.Called(v, datastore, sizeGB, scsiKey)
	return args.Error(0)
}

func (m *CreatorInterface) AddNetworkAdapter(v *object.VirtualMachine, network object.NetworkReference) error {
	args := m.Called(v, network)
	return args.Error(0)
}

func (m *CreatorInterface) SetMACAddress(v *object.VirtualMachine, mac string) (string, error) {
	args := m.Called(v, mac)
	return args.String(0), args.Error(1)
}

func (m *CreatorInterface) GetMACAddress(v *object.VirtualMachine) (string, error) {
	args := m.Called(v)
	return args.String(0), args.Error(1)
}

func (m *CreatorInterface) PowerOn(v *object.VirtualMachine) error {
	args := m.Called(v)
	return args.Error(0)
}

func (m *CreatorInterface) PowerOff(v *object.VirtualMachine) error {
	args := m.Called(v)
	return args.Error(0)
}

func (m *CreatorInterface) Delete(v *object.VirtualMachine) error {
	args := m.Called(v)
	return args.Error(0)
}
