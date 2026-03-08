// Package mocks provides testify-based mock implementations for testing
// without a real vCenter connection.
package mocks

import (
	"github.com/Bibi40k/vmware-vm-bootstrap/pkg/vcenter"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/object"
)

// ClientInterface is a mock for vcenter.ClientInterface.
type ClientInterface struct {
	mock.Mock
}

func (m *ClientInterface) FindDatacenter(name string) (*object.Datacenter, error) {
	args := m.Called(name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*object.Datacenter), args.Error(1)
}

func (m *ClientInterface) FindDatastore(datacenter, name string) (*object.Datastore, error) {
	args := m.Called(datacenter, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*object.Datastore), args.Error(1)
}

func (m *ClientInterface) FindNetwork(datacenter, name string) (object.NetworkReference, error) {
	args := m.Called(datacenter, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(object.NetworkReference), args.Error(1)
}

func (m *ClientInterface) FindFolder(datacenter, path string) (*object.Folder, error) {
	args := m.Called(datacenter, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*object.Folder), args.Error(1)
}

func (m *ClientInterface) FindResourcePool(datacenter, path string) (*object.ResourcePool, error) {
	args := m.Called(datacenter, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*object.ResourcePool), args.Error(1)
}

func (m *ClientInterface) FindVM(datacenter, name string) (*object.VirtualMachine, error) {
	args := m.Called(datacenter, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*object.VirtualMachine), args.Error(1)
}

func (m *ClientInterface) ListDatastores(datacenter string) ([]vcenter.DatastoreInfo, error) {
	args := m.Called(datacenter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]vcenter.DatastoreInfo), args.Error(1)
}

func (m *ClientInterface) ListNetworks(datacenter string) ([]vcenter.NetworkInfo, error) {
	args := m.Called(datacenter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]vcenter.NetworkInfo), args.Error(1)
}

func (m *ClientInterface) ListFolders(datacenter string) ([]vcenter.FolderInfo, error) {
	args := m.Called(datacenter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]vcenter.FolderInfo), args.Error(1)
}

func (m *ClientInterface) ListResourcePools(datacenter string) ([]vcenter.ResourcePoolInfo, error) {
	args := m.Called(datacenter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]vcenter.ResourcePoolInfo), args.Error(1)
}

func (m *ClientInterface) ListVMGuestIPs(datacenter string) ([]vcenter.VMGuestIPInfo, error) {
	args := m.Called(datacenter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]vcenter.VMGuestIPInfo), args.Error(1)
}

func (m *ClientInterface) Disconnect() error {
	args := m.Called()
	return args.Error(0)
}
