package vcenter

import (
	"github.com/vmware/govmomi/object"
)

// ClientInterface abstracts vCenter operations.
// The real implementation uses govmomi; tests inject a mock.
type ClientInterface interface {
	FindDatacenter(name string) (*object.Datacenter, error)
	FindDatastore(datacenter, name string) (*object.Datastore, error)
	FindNetwork(datacenter, name string) (object.NetworkReference, error)
	FindFolder(datacenter, path string) (*object.Folder, error)
	FindResourcePool(datacenter, path string) (*object.ResourcePool, error)
	FindVM(datacenter, name string) (*object.VirtualMachine, error)
	ListDatastores(datacenter string) ([]DatastoreInfo, error)
	ListNetworks(datacenter string) ([]NetworkInfo, error)
	ListFolders(datacenter string) ([]FolderInfo, error)
	ListResourcePools(datacenter string) ([]ResourcePoolInfo, error)
	ListVMGuestIPs(datacenter string) ([]VMGuestIPInfo, error)
	Disconnect() error
}

// compile-time interface compliance check
var _ ClientInterface = (*Client)(nil)
