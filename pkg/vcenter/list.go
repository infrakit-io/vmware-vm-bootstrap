package vcenter

import (
	"net"
	"strings"

	"github.com/vmware/govmomi/vim25/mo"
)

// DatastoreInfo holds information about a vCenter datastore.
type DatastoreInfo struct {
	Name        string
	CapacityGB  float64
	FreeSpaceGB float64
	Accessible  bool
	Type        string // "SSD" or "HDD" (inferred from name)
}

// NetworkInfo holds information about a vCenter network/port group.
type NetworkInfo struct {
	Name string
}

// ListDatastores returns all datastores in a datacenter.
func (c *Client) ListDatastores(datacenter string) ([]DatastoreInfo, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}
	c.finder.SetDatacenter(dc)

	dsList, err := c.finder.DatastoreList(c.ctx, "*")
	if err != nil {
		return nil, err
	}

	var result []DatastoreInfo
	for _, ds := range dsList {
		var moDS mo.Datastore
		if err := ds.Properties(c.ctx, ds.Reference(), []string{"summary"}, &moDS); err != nil {
			continue
		}
		s := moDS.Summary
		result = append(result, DatastoreInfo{
			Name:        s.Name,
			CapacityGB:  float64(s.Capacity) / (1024 * 1024 * 1024),
			FreeSpaceGB: float64(s.FreeSpace) / (1024 * 1024 * 1024),
			Accessible:  s.Accessible,
			Type:        inferStorageType(s.Name),
		})
	}
	return result, nil
}

// ListNetworks returns all networks/port groups in a datacenter.
func (c *Client) ListNetworks(datacenter string) ([]NetworkInfo, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}
	c.finder.SetDatacenter(dc)

	nets, err := c.finder.NetworkList(c.ctx, "*")
	if err != nil {
		return nil, err
	}

	var result []NetworkInfo
	for _, n := range nets {
		result = append(result, NetworkInfo{Name: n.GetInventoryPath()})
	}
	return result, nil
}

// FolderInfo holds information about a vCenter VM folder.
type FolderInfo struct {
	Name string // relative path under the datacenter's vm root (e.g., "Production/WebServers")
}

// ListFolders returns all VM folders in a datacenter (excludes the datacenter root folder itself).
func (c *Client) ListFolders(datacenter string) ([]FolderInfo, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}
	c.finder.SetDatacenter(dc)

	folders, err := c.finder.FolderList(c.ctx, "*")
	if err != nil {
		return nil, err
	}

	// VM folders live under /<datacenter>/vm/...
	// The root "vm" folder itself is not user-selectable.
	vmRoot := "/" + datacenter + "/vm"
	var result []FolderInfo
	for _, f := range folders {
		path := f.InventoryPath
		if !strings.HasPrefix(path, vmRoot+"/") {
			continue
		}
		result = append(result, FolderInfo{
			Name: strings.TrimPrefix(path, vmRoot+"/"),
		})
	}
	return result, nil
}

// ResourcePoolInfo holds information about a vCenter resource pool.
type ResourcePoolInfo struct {
	Name string // relative path under the datacenter's host tree (e.g., "Cluster/Resources/MyPool")
}

// ListResourcePools returns all resource pools in a datacenter.
func (c *Client) ListResourcePools(datacenter string) ([]ResourcePoolInfo, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}
	c.finder.SetDatacenter(dc)

	pools, err := c.finder.ResourcePoolList(c.ctx, "*")
	if err != nil {
		return nil, err
	}

	// Resource pools live under /<datacenter>/host/...
	// Strip the datacenter/host prefix so names are short and readable.
	hostRoot := "/" + datacenter + "/host/"
	var result []ResourcePoolInfo
	for _, p := range pools {
		name := strings.TrimPrefix(p.InventoryPath, hostRoot)
		result = append(result, ResourcePoolInfo{Name: name})
	}
	return result, nil
}

// VMGuestIPInfo holds best-effort guest IP information reported by vCenter tools.
// IP can be empty/stale when VMware Tools data is unavailable.
type VMGuestIPInfo struct {
	Name string
	IP   string
}

// ListVMGuestIPs returns VM name -> reported guest IPv4 address for a datacenter.
// This is best-effort information sourced from guest tools.
func (c *Client) ListVMGuestIPs(datacenter string) ([]VMGuestIPInfo, error) {
	dc, err := c.FindDatacenter(datacenter)
	if err != nil {
		return nil, err
	}
	c.finder.SetDatacenter(dc)

	vms, err := c.finder.VirtualMachineList(c.ctx, "*")
	if err != nil {
		return nil, err
	}

	out := make([]VMGuestIPInfo, 0, len(vms))
	for _, vm := range vms {
		var moVM mo.VirtualMachine
		if err := vm.Properties(c.ctx, vm.Reference(), []string{"name", "guest.ipAddress"}, &moVM); err != nil {
			continue
		}
		if moVM.Guest == nil {
			continue
		}
		ip := strings.TrimSpace(moVM.Guest.IpAddress)
		if ip == "" {
			continue
		}
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.To4() == nil {
			continue
		}
		out = append(out, VMGuestIPInfo{
			Name: moVM.Name,
			IP:   parsed.String(),
		})
	}
	return out, nil
}

// inferStorageType infers SSD vs HDD from the datastore name (matches Python heuristic).
func inferStorageType(name string) string {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "ssd") || strings.Contains(lower, "nvme") {
		return "SSD"
	}
	return "HDD"
}
