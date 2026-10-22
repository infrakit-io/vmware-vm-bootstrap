package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/types"
)

type simEnv struct {
	ctx    context.Context
	client *govmomi.Client
	finder *find.Finder
}

func newSimEnv(t *testing.T) (*simEnv, func()) {
	t.Helper()

	model := simulator.VPX()
	model.Datacenter = 1
	model.Cluster = 1
	model.Host = 1
	model.Pool = 1
	model.Machine = 0
	model.Portgroup = 1
	model.Datastore = 1

	require.NoError(t, model.Create())
	s := model.Service.NewServer()

	ctx := context.Background()
	u := s.URL
	u.User = simulator.DefaultLogin

	c, err := govmomi.NewClient(ctx, u, true)
	require.NoError(t, err)

	f := find.NewFinder(c.Client, true)

	cleanup := func() {
		s.Close()
		model.Remove()
	}

	return &simEnv{ctx: ctx, client: c, finder: f}, cleanup
}

func (e *simEnv) defaultObjects(t *testing.T) (*object.Folder, *object.ResourcePool, *object.Datastore, object.NetworkReference) {
	t.Helper()

	dc, err := e.finder.DefaultDatacenter(e.ctx)
	require.NoError(t, err)
	e.finder.SetDatacenter(dc)

	folder, err := e.finder.DefaultFolder(e.ctx)
	require.NoError(t, err)

	pools, err := e.finder.ResourcePoolList(e.ctx, "*")
	require.NoError(t, err)
	require.NotEmpty(t, pools)

	datastore, err := e.finder.DefaultDatastore(e.ctx)
	require.NoError(t, err)

	networks, err := e.finder.NetworkList(e.ctx, "*")
	require.NoError(t, err)
	require.NotEmpty(t, networks)

	return folder, pools[0], datastore, networks[0]
}

func createTestVM(t *testing.T, env *simEnv, name string) (*Creator, *object.VirtualMachine, *object.Datastore) {
	t.Helper()

	creator := NewCreator(env.ctx)
	folder, pool, datastore, _ := env.defaultObjects(t)

	spec := creator.CreateSpec(&Config{
		Name:      name,
		CPUs:      1,
		MemoryMB:  256,
		Datastore: datastore.Name(),
	})

	vm, err := creator.Create(folder, pool, datastore, spec)
	require.NoError(t, err)
	require.NotNil(t, vm)

	return creator, vm, datastore
}

func TestCreate_VM(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator := NewCreator(env.ctx)
	folder, pool, datastore, _ := env.defaultObjects(t)

	spec := creator.CreateSpec(&Config{
		Name:      "vm-create",
		CPUs:      1,
		MemoryMB:  256,
		Datastore: datastore.Name(),
	})

	vm, err := creator.Create(folder, pool, datastore, spec)
	require.NoError(t, err)
	require.NotNil(t, vm)
}

func TestEnsureSCSIController_AddDisk(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, datastore := createTestVM(t, env, "vm-disk")

	key, err := creator.EnsureSCSIController(vm)
	require.NoError(t, err)
	require.NotZero(t, key)

	require.NoError(t, creator.AddDisk(vm, datastore, datastore.Name(), 1, key))

	devices, err := vm.Device(env.ctx)
	require.NoError(t, err)
	disks := devices.SelectByType((*types.VirtualDisk)(nil))
	require.NotEmpty(t, disks)
}

func TestEnsureSCSIController_Existing(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, _ := createTestVM(t, env, "vm-scsi-existing")

	firstKey, err := creator.EnsureSCSIController(vm)
	require.NoError(t, err)
	require.NotZero(t, firstKey)

	secondKey, err := creator.EnsureSCSIController(vm)
	require.NoError(t, err)
	require.Equal(t, firstKey, secondKey)
}

type failingNetwork struct{}

func (f failingNetwork) Reference() types.ManagedObjectReference {
	return types.ManagedObjectReference{Type: "Network", Value: "net-1"}
}

func (f failingNetwork) GetInventoryPath() string {
	return "/DC0/network/fail"
}

func (f failingNetwork) EthernetCardBackingInfo(_ context.Context) (types.BaseVirtualDeviceBackingInfo, error) {
	return nil, errors.New("backing info failed")
}

func TestAddNetworkAdapter_Error(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, _ := createTestVM(t, env, "vm-net-error")

	err := creator.AddNetworkAdapter(vm, failingNetwork{})
	require.Error(t, err)
}

func TestAddNetworkAdapter(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, _ := createTestVM(t, env, "vm-net")
	_, _, _, network := env.defaultObjects(t)

	require.NoError(t, creator.AddNetworkAdapter(vm, network))

	devices, err := vm.Device(env.ctx)
	require.NoError(t, err)
	nics := devices.SelectByType((*types.VirtualEthernetCard)(nil))
	require.NotEmpty(t, nics)
}

func TestPowerOnPowerOffDelete(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, _ := createTestVM(t, env, "vm-power")

	require.NoError(t, creator.PowerOn(vm))
	state, err := vm.PowerState(env.ctx)
	require.NoError(t, err)
	require.Equal(t, types.VirtualMachinePowerStatePoweredOn, state)

	require.NoError(t, creator.PowerOff(vm))
	state, err = vm.PowerState(env.ctx)
	require.NoError(t, err)
	require.Equal(t, types.VirtualMachinePowerStatePoweredOff, state)

	require.NoError(t, creator.Delete(vm))
}

func TestAddExtraNetworkAdapter_AfterMACStartConnected(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, _ := createTestVM(t, env, "vm-extra-nic")
	_, _, _, network := env.defaultObjects(t)

	require.NoError(t, creator.AddNetworkAdapter(vm, network))
	_, err := creator.SetMACAddress(vm, "00:50:56:aa:bb:cc")
	require.NoError(t, err)
	mac1, err := creator.AddExtraNetworkAdapter(vm, network, false, false)
	require.NoError(t, err)
	mac2, err := creator.AddExtraNetworkAdapter(vm, network, true, true)
	require.NoError(t, err)
	require.NotEmpty(t, mac1)
	require.NotEmpty(t, mac2)
	require.NotEqual(t, mac1, mac2, "each call returns the MAC of the NIC it just added")

	devices, err := vm.Device(env.ctx)
	require.NoError(t, err)
	nics := devices.SelectByType((*types.VirtualEthernetCard)(nil))
	require.Len(t, nics, 3)
	primary := nics[0].(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	require.Equal(t, "00:50:56:aa:bb:cc", primary.MacAddress, "primary keeps the static MAC")
	require.True(t, primary.Connectable.StartConnected, "primary behaviour unchanged")
	extra1 := nics[1].(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	extra2 := nics[2].(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	require.False(t, extra1.Connectable.StartConnected)
	require.True(t, extra2.Connectable.StartConnected)
	require.False(t, extra1.Connectable.AllowGuestControl, "default: guest cannot connect a disconnected NIC")
	require.True(t, extra2.Connectable.AllowGuestControl, "explicit opt-in")
	require.Equal(t, mac1, strings.ToLower(extra1.MacAddress))
	require.Equal(t, mac2, strings.ToLower(extra2.MacAddress))
}

func TestAddExtraNetworkAdapter_Error(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, _ := createTestVM(t, env, "vm-extra-nic-err")
	_, err := creator.AddExtraNetworkAdapter(vm, failingNetwork{}, true, false)
	require.Error(t, err)
}

func TestAddDisk_BackingFileNameIsDatastoreOnlyPath(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, datastore := createTestVM(t, env, "vm-disk-ds")
	key, err := creator.EnsureSCSIController(vm)
	require.NoError(t, err)
	require.NoError(t, creator.AddDisk(vm, datastore, datastore.Name(), 1, key))

	devices, err := vm.Device(env.ctx)
	require.NoError(t, err)
	disks := devices.SelectByType((*types.VirtualDisk)(nil))
	require.Len(t, disks, 1)
	backing := disks[0].(*types.VirtualDisk).Backing.(*types.VirtualDiskFlatVer2BackingInfo)
	require.True(t, strings.HasPrefix(backing.FileName, "["+datastore.Name()+"]"), "FileName = %q", backing.FileName)
	require.True(t, *backing.ThinProvisioned)
}

func TestAddDisk_EmptyDatastoreNameKeepsOriginalBacking(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, datastore := createTestVM(t, env, "vm-disk-os")
	key, err := creator.EnsureSCSIController(vm)
	require.NoError(t, err)
	require.NoError(t, creator.AddDisk(vm, datastore, "", 1, key))

	devices, err := vm.Device(env.ctx)
	require.NoError(t, err)
	disks := devices.SelectByType((*types.VirtualDisk)(nil))
	require.Len(t, disks, 1)
	backing := disks[0].(*types.VirtualDisk).Backing.(*types.VirtualDiskFlatVer2BackingInfo)
	require.True(t, *backing.ThinProvisioned)
	require.NotNil(t, backing.Datastore)
}

func TestPowerOff_AlreadyPoweredOffIsSuccess(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	creator, vm, _ := createTestVM(t, env, "vm-already-off")
	state, err := vm.PowerState(env.ctx)
	require.NoError(t, err)
	require.Equal(t, types.VirtualMachinePowerStatePoweredOff, state)

	require.NoError(t, creator.PowerOff(vm)) // must not fail with InvalidPowerState
	require.NoError(t, creator.PowerOff(vm)) // idempotent
}
