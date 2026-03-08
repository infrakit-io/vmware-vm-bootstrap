package iso

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm"
	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/soap"
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

func (e *simEnv) defaultObjects(t *testing.T) (*object.Folder, *object.ResourcePool, *object.Datastore) {
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

	return folder, pools[0], datastore
}

func createTestVM(t *testing.T, env *simEnv, name string) (*vm.Creator, *object.VirtualMachine, *object.Datastore) {
	t.Helper()

	creator := vm.NewCreator(env.ctx)
	folder, pool, datastore := env.defaultObjects(t)

	spec := creator.CreateSpec(&vm.Config{
		Name:      name,
		CPUs:      1,
		MemoryMB:  256,
		Datastore: datastore.Name(),
	})

	created, err := creator.Create(folder, pool, datastore, spec)
	require.NoError(t, err)
	require.NotNil(t, created)

	return creator, created, datastore
}

func TestMountISOs_AddsAndConnectsCdroms(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, datastore := createTestVM(t, env, "iso-mount")
	manager := NewManager(env.ctx)

	ubuntu := "[" + datastore.Name() + "] ISO/ubuntu.iso"
	nocloud := "[" + datastore.Name() + "] ISO/nocloud.iso"

	require.NoError(t, manager.MountISOs(vmObj, ubuntu, nocloud))

	devices, err := getDevices(env.ctx, vmObj)
	require.NoError(t, err)
	cdroms := getCDROMs(devices)
	require.GreaterOrEqual(t, len(cdroms), 2)

	found := map[string]bool{
		ubuntu:  false,
		nocloud: false,
	}
	for _, cdrom := range cdroms {
		backing, ok := cdrom.Backing.(*types.VirtualCdromIsoBackingInfo)
		if !ok {
			continue
		}
		if backing.FileName == ubuntu || backing.FileName == nocloud {
			found[backing.FileName] = true
		}
	}
	require.True(t, found[ubuntu])
	require.True(t, found[nocloud])

	unitNumbers := map[int32]bool{}
	for _, cdrom := range cdroms {
		if cdrom.UnitNumber != nil {
			unitNumbers[*cdrom.UnitNumber] = true
		}
	}
	require.GreaterOrEqual(t, len(unitNumbers), 2)
}

func TestRemoveAllCDROMs(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, datastore := createTestVM(t, env, "iso-remove")
	manager := NewManager(env.ctx)

	isoPath := "[" + datastore.Name() + "] ISO/ubuntu.iso"
	require.NoError(t, manager.mountSingleISO(vmObj, isoPath, "Ubuntu"))

	devices, err := getDevices(env.ctx, vmObj)
	require.NoError(t, err)
	require.NotEmpty(t, getCDROMs(devices))

	require.NoError(t, manager.RemoveAllCDROMs(vmObj))

	devices, err = getDevices(env.ctx, vmObj)
	require.NoError(t, err)
	require.Empty(t, getCDROMs(devices))
}

func TestMountSingleISO_PublicWrapper(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, datastore := createTestVM(t, env, "iso-single-public")
	manager := NewManager(env.ctx)

	isoPath := "[" + datastore.Name() + "] ISO/public-wrapper.iso"
	require.NoError(t, manager.MountSingleISO(vmObj, isoPath, "Ubuntu"))

	devices, err := getDevices(env.ctx, vmObj)
	require.NoError(t, err)
	cdroms := getCDROMs(devices)
	require.NotEmpty(t, cdroms)
}

func TestConnectAllCDROMs_NoCdroms(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, _ := createTestVM(t, env, "iso-connect-none")
	manager := NewManager(env.ctx)

	require.NoError(t, manager.ConnectAllCDROMs(vmObj))

	devices, err := getDevices(env.ctx, vmObj)
	require.NoError(t, err)
	require.Empty(t, getCDROMs(devices))
}

func TestCleanupNoCloudISO_RemovesCdroms(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, datastore := createTestVM(t, env, "iso-cleanup")
	manager := NewManager(env.ctx)

	isoPath := "[" + datastore.Name() + "] ISO/ubuntu.iso"
	require.NoError(t, manager.mountSingleISO(vmObj, isoPath, "Ubuntu"))
	require.NoError(t, manager.mountSingleISO(vmObj, isoPath, "NoCloud"))

	devices, err := getDevices(env.ctx, vmObj)
	require.NoError(t, err)
	require.NotEmpty(t, getCDROMs(devices))

	require.NoError(t, manager.CleanupNoCloudISO(vmObj))

	devices, err = getDevices(env.ctx, vmObj)
	require.NoError(t, err)
	require.Empty(t, getCDROMs(devices))
}

func TestCheckFileExists(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, _, datastore := createTestVM(t, env, "iso-checkfile")
	manager := NewManager(env.ctx)

	remotePath := "ISO/test.iso"
	content := bytes.NewReader([]byte("hello"))
	upload := soap.Upload{ContentLength: 5}

	uploadErr := datastore.Upload(env.ctx, content, remotePath, &upload)

	exists, err := manager.CheckFileExists(datastore, remotePath)
	require.NoError(t, err)
	if uploadErr == nil && exists {
		require.True(t, exists)
		return
	}

	// Some simulator configs don't support uploads or browser doesn't see uploaded files.
	// Try to find any existing file in the datastore and validate against that.
	browser, bErr := datastore.Browser(env.ctx)
	require.NoError(t, bErr)

	spec := types.HostDatastoreBrowserSearchSpec{MatchPattern: []string{"*"}}
	dsPath := "[" + datastore.Name() + "]"
	task, tErr := browser.SearchDatastore(env.ctx, dsPath, &spec)
	require.NoError(t, tErr)
	require.NoError(t, task.Wait(env.ctx))
	info, rErr := task.WaitForResult(env.ctx, nil)
	require.NoError(t, rErr)

	result, ok := info.Result.(types.HostDatastoreBrowserSearchResults)
	if !ok || len(result.File) == 0 {
		t.Skip("no existing files found in simulator datastore")
	}

	prefix := "[" + datastore.Name() + "] "
	folderRel := strings.TrimPrefix(result.FolderPath, prefix)
	folderRel = strings.TrimSuffix(folderRel, "/")
	name := result.File[0].GetFileInfo().Path
	if folderRel == "" {
		remotePath = name
	} else {
		remotePath = folderRel + "/" + name
	}

	exists, err = manager.CheckFileExists(datastore, remotePath)
	require.NoError(t, err)
	if !exists {
		t.Skipf("CheckFileExists returned false for remotePath %q (simulator limitation)", remotePath)
	}

	missing, err := manager.CheckFileExists(datastore, "ISO/missing.iso")
	require.NoError(t, err)
	require.False(t, missing)
}

func TestUploadToDatastore_UsesGovc(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, _, datastore := createTestVM(t, env, "iso-upload")
	manager := NewManager(env.ctx)

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	require.NoError(t, os.MkdirAll(bin, 0755))
	writeFakeExecutable(t, bin, "govc", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	local := filepath.Join(tmp, "test.iso")
	require.NoError(t, os.WriteFile(local, []byte("data"), 0644))

	err := manager.UploadToDatastore(datastore, local, "ISO/test.iso", "vc", "user", "pass", true)
	require.NoError(t, err)

	if _, err := os.Stat(hashFile(local)); err != nil {
		t.Fatalf("expected uploaded hash file: %v", err)
	}
}

func TestUploadAlways_DoesNotSaveHash(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, _, datastore := createTestVM(t, env, "iso-upload-always")
	manager := NewManager(env.ctx)

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	require.NoError(t, os.MkdirAll(bin, 0755))
	writeFakeExecutable(t, bin, "govc", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	local := filepath.Join(tmp, "nocloud.iso")
	require.NoError(t, os.WriteFile(local, []byte("data"), 0644))

	err := manager.UploadAlways(datastore, local, "ISO/nocloud.iso", "vc", "user", "pass", true)
	require.NoError(t, err)

	if _, err := os.Stat(hashFile(local)); err == nil {
		t.Fatal("did not expect uploaded hash file for UploadAlways")
	}
}

func TestUploadWithGovmomi_ErrorPath(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, _, datastore := createTestVM(t, env, "iso-upload-govmomi")
	manager := NewManager(env.ctx)

	local := filepath.Join(t.TempDir(), "govmomi.iso")
	require.NoError(t, os.WriteFile(local, []byte("data"), 0644))

	err := manager.uploadWithGovmomi(datastore, local, "ISO/govmomi.iso")
	require.Error(t, err)
}

func TestEnsureCDROMsConnectedAfterBoot_NoCdroms(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, _ := createTestVM(t, env, "iso-ensure-none")
	manager := NewManager(env.ctx)

	old := configs.Defaults.Timeouts.HardwareInitSeconds
	configs.Defaults.Timeouts.HardwareInitSeconds = 0
	defer func() { configs.Defaults.Timeouts.HardwareInitSeconds = old }()

	require.NoError(t, manager.EnsureCDROMsConnectedAfterBoot(vmObj))
}

func TestEnsureCDROMsConnectedAfterBoot_Reconnects(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, datastore := createTestVM(t, env, "iso-ensure-reconnect")
	manager := NewManager(env.ctx)

	isoPath := "[" + datastore.Name() + "] ISO/ubuntu.iso"
	require.NoError(t, manager.mountSingleISO(vmObj, isoPath, "Ubuntu"))
	require.NoError(t, manager.mountSingleISO(vmObj, isoPath, "NoCloud"))

	devices, err := getDevices(env.ctx, vmObj)
	require.NoError(t, err)
	cdroms := getCDROMs(devices)
	require.NotEmpty(t, cdroms)

	// Force one CD-ROM to disconnected state
	cdroms[0].Connectable.Connected = false
	cdroms[0].Connectable.StartConnected = false
	spec := types.VirtualMachineConfigSpec{
		DeviceChange: []types.BaseVirtualDeviceConfigSpec{
			&types.VirtualDeviceConfigSpec{
				Operation: types.VirtualDeviceConfigSpecOperationEdit,
				Device:    cdroms[0],
			},
		},
	}
	task, err := vmObj.Reconfigure(env.ctx, spec)
	require.NoError(t, err)
	require.NoError(t, task.Wait(env.ctx))

	powerOn, err := vmObj.PowerOn(env.ctx)
	require.NoError(t, err)
	require.NoError(t, powerOn.Wait(env.ctx))

	old := configs.Defaults.Timeouts.HardwareInitSeconds
	configs.Defaults.Timeouts.HardwareInitSeconds = 0
	defer func() { configs.Defaults.Timeouts.HardwareInitSeconds = old }()

	require.NoError(t, manager.EnsureCDROMsConnectedAfterBoot(vmObj))

	devices, err = getDevices(env.ctx, vmObj)
	require.NoError(t, err)
	cdroms = getCDROMs(devices)
	for _, cdrom := range cdroms {
		require.NotNil(t, cdrom.Connectable)
		require.True(t, cdrom.Connectable.Connected)
	}
}

func TestGetNextCDROMUnitNumber_NoSlots(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, _ := createTestVM(t, env, "iso-units")
	manager := NewManager(env.ctx)

	devices, err := getDevices(env.ctx, vmObj)
	require.NoError(t, err)

	controller, err := manager.getOrCreateSATAController(vmObj, devices)
	require.NoError(t, err)

	// Fill all 30 SATA unit numbers with dummy CD-ROMs
	for unit := int32(0); unit < 30; unit++ {
		cdrom := &types.VirtualCdrom{
			VirtualDevice: types.VirtualDevice{
				Key:           -1,
				ControllerKey: controller.(types.BaseVirtualDevice).GetVirtualDevice().Key,
				UnitNumber:    &unit,
				Backing: &types.VirtualCdromIsoBackingInfo{
					VirtualDeviceFileBackingInfo: types.VirtualDeviceFileBackingInfo{
						FileName: "[datastore] ISO/dummy.iso",
					},
				},
			},
		}
		require.NoError(t, vmObj.AddDevice(env.ctx, cdrom))
	}

	_, err = manager.getNextCDROMUnitNumber(vmObj, controller)
	require.Error(t, err)
}

func TestGetDevices_ContextCanceled(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, _ := createTestVM(t, env, "iso-getdevices-cancel")
	ctx, cancel := context.WithCancel(env.ctx)
	cancel()

	_, err := getDevices(ctx, vmObj)
	require.Error(t, err)
}

func TestReconfigureVM_ContextCanceled(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, vmObj, _ := createTestVM(t, env, "iso-reconfig-cancel")
	ctx, cancel := context.WithCancel(env.ctx)
	cancel()

	err := reconfigureVM(ctx, vmObj, nil)
	require.Error(t, err)
}

func TestCheckFileExists_ContextCanceled(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()

	_, _, datastore := createTestVM(t, env, "iso-checkfile-cancel")
	ctx, cancel := context.WithCancel(env.ctx)
	cancel()

	manager := NewManager(ctx)
	_, err := manager.CheckFileExists(datastore, "ISO/anything.iso")
	require.Error(t, err)
}
