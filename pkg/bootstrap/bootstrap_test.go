package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/Bibi40k/vmware-vm-bootstrap/configs"
	"github.com/Bibi40k/vmware-vm-bootstrap/internal/utils"
	isomocks "github.com/Bibi40k/vmware-vm-bootstrap/pkg/iso/mocks"
	"github.com/Bibi40k/vmware-vm-bootstrap/pkg/profile"
	ubuntuprofile "github.com/Bibi40k/vmware-vm-bootstrap/pkg/profile/ubuntu"
	vcmocks "github.com/Bibi40k/vmware-vm-bootstrap/pkg/vcenter/mocks"
	vmmocks "github.com/Bibi40k/vmware-vm-bootstrap/pkg/vm/mocks"

	isoiface "github.com/Bibi40k/vmware-vm-bootstrap/pkg/iso"
	vcface "github.com/Bibi40k/vmware-vm-bootstrap/pkg/vcenter"
	vmiface "github.com/Bibi40k/vmware-vm-bootstrap/pkg/vm"
)

// minimalConfig returns a valid VMConfig for tests.
func minimalConfig() *VMConfig {
	return &VMConfig{
		VCenterHost:     "vcenter.test",
		VCenterUsername: "user",
		VCenterPassword: "pass",
		Name:            "test-vm",
		CPUs:            2,
		MemoryMB:        2048,
		DiskSizeGB:      20,
		NetworkName:     "LAN",
		IPAddress:       "192.168.1.10",
		Netmask:         "255.255.255.0",
		Gateway:         "192.168.1.1",
		DNS:             []string{"8.8.8.8"},
		Datacenter:      "DC1",
		Folder:          "Production",
		ResourcePool:    "pool",
		Datastore:       "SSD01",
		Profile:         "ubuntu",
		Profiles: VMProfiles{
			Ubuntu: UbuntuProfile{Version: "24.04"},
		},
		Username:      "sysadmin",
		SSHPublicKeys: []string{"ssh-ed25519 AAAA test"},
	}
}

// testBootstrapper builds a bootstrapper with injected mocks.
func testBootstrapper(
	vc *vcmocks.ClientInterface,
	creator *vmmocks.CreatorInterface,
	isoMgr *isomocks.ManagerInterface,
) *bootstrapper {
	return &bootstrapper{
		connectVCenter: func(ctx context.Context, cfg *VMConfig) (vcface.ClientInterface, error) {
			return vc, nil
		},
		newVMCreator: func(ctx context.Context) vmiface.CreatorInterface {
			return creator
		},
		newISOManager: func(ctx context.Context) isoiface.ManagerInterface {
			return isoMgr
		},
		resolveProfile: func(profileName string) (profile.Provisioner, error) {
			return ubuntuprofile.New(), nil
		},
		waitInstall: func(ctx context.Context, vmObj *object.VirtualMachine, cfg *VMConfig, logger *slog.Logger) error {
			return nil
		},
		checkSSH: func(ctx context.Context, ipAddr string) error {
			return nil
		},
	}
}

// fakeVM returns a VirtualMachine object suitable for mock returns.
func fakeVM() *object.VirtualMachine {
	return object.NewVirtualMachine(nil, types.ManagedObjectReference{
		Type:  "VirtualMachine",
		Value: "vm-42",
	})
}

// wireSuccessfulMocks configures all mocks for a complete successful bootstrap.
func wireSuccessfulMocks(vc *vcmocks.ClientInterface, creator *vmmocks.CreatorInterface, isoMgr *isomocks.ManagerInterface) {
	vm := fakeVM()
	spec := &types.VirtualMachineConfigSpec{}

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(spec)
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, spec).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("PowerOn", vm).Return(nil)
	creator.On("PowerOff", vm).Return(nil)
	creator.On("Delete", vm).Maybe().Return(nil) // only called in defer cleanup on failure

	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").Return("/tmp/nocloud.iso", nil)
	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("UploadAlways", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("MountISOs", vm, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", vm).Return(nil)
	isoMgr.On("RemoveAllCDROMs", vm).Return(nil)
	isoMgr.On("DeleteFromDatastore", "SSD01", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
}

// =============================================================================
// Tests: error paths
// =============================================================================

func TestBootstrap_VMAlreadyExists(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vc.On("FindVM", "DC1", "test-vm").Return(fakeVM(), nil)
	vc.On("Disconnect").Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	vc.AssertExpectations(t)
}

func TestBootstrap_VCenterConnectionFails(t *testing.T) {
	b := &bootstrapper{
		connectVCenter: func(ctx context.Context, cfg *VMConfig) (vcface.ClientInterface, error) {
			return nil, errors.New("connection refused")
		},
		newVMCreator:  func(ctx context.Context) vmiface.CreatorInterface { return nil },
		newISOManager: func(ctx context.Context) isoiface.ManagerInterface { return nil },
	}

	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "vCenter connection failed")
}

func TestBootstrap_FindFolderFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(nil, errors.New("folder not found"))
	vc.On("Disconnect").Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find folder")
}

func TestBootstrap_FindDatastoreFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(nil, errors.New("datastore not found"))
	vc.On("Disconnect").Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find datastore")
}

func TestBootstrap_FindNetworkFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return(nil, errors.New("network not found"))
	vc.On("Disconnect").Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find network")
}

func TestBootstrap_FindISODatastoreFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	cfg := minimalConfig()
	cfg.ISODatastore = "ISO-DS"

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "ISO-DS").Return(nil, errors.New("iso datastore not found"))
	vc.On("Disconnect").Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), cfg, slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find ISO datastore")
}

func TestBootstrap_VMCreationFails_cleansUp(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(&types.VirtualMachineConfigSpec{})
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("not enough resources"))

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "VM creation failed")
}

func TestBootstrap_EnsureSCSIControllerFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vm := fakeVM()
	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(&types.VirtualMachineConfigSpec{})
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), errors.New("scsi fail"))
	creator.On("Delete", vm).Return(nil)

	isoMgr.On("DeleteFromDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to add SCSI controller")
}

func TestBootstrap_UploadUbuntuFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vm := fakeVM()
	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(&types.VirtualMachineConfigSpec{})
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("Delete", vm).Return(nil)

	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").Return("/tmp/nocloud.iso", nil)
	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("upload failed"))
	isoMgr.On("DeleteFromDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to upload Ubuntu ISO")
}

func TestBootstrap_MountISOsFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vm := fakeVM()
	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(&types.VirtualMachineConfigSpec{})
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("Delete", vm).Return(nil)

	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").Return("/tmp/nocloud.iso", nil)
	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("UploadAlways", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("MountISOs", vm, mock.Anything, mock.Anything).Return(errors.New("mount failed"))
	isoMgr.On("DeleteFromDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to mount ISOs")
}

func TestBootstrap_PowerOnFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vm := fakeVM()
	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(&types.VirtualMachineConfigSpec{})
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("PowerOn", vm).Return(errors.New("power on failed"))
	creator.On("Delete", vm).Return(nil)

	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").Return("/tmp/nocloud.iso", nil)
	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("UploadAlways", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("MountISOs", vm, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("DeleteFromDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to power on VM")
}

func TestBootstrap_ISODownloadFails_cleansUpVM(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vm := fakeVM()
	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(&types.VirtualMachineConfigSpec{})
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("Delete", vm).Return(nil) // VM cleanup on failure

	isoMgr.On("DownloadUbuntu", "24.04").Return("", errors.New("network error"))
	isoMgr.On("DeleteFromDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to download Ubuntu ISO")
	creator.AssertCalled(t, "Delete", vm) // VM was cleaned up
}

func TestBootstrap_SSHVerificationFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	wireSuccessfulMocks(vc, creator, isoMgr)

	b := testBootstrapper(vc, creator, isoMgr)
	b.checkSSH = func(ctx context.Context, ipAddr string) error {
		return errors.New("SSH port not accessible")
	}

	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSH verification failed")
}

func TestBootstrap_InstallationFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	wireSuccessfulMocks(vc, creator, isoMgr)

	b := testBootstrapper(vc, creator, isoMgr)
	b.waitInstall = func(ctx context.Context, vmObj *object.VirtualMachine, cfg *VMConfig, logger *slog.Logger) error {
		return errors.New("installation timeout")
	}

	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "installation failed")
}

func TestBootstrap_InvalidNetmask(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vm := fakeVM()
	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(&types.VirtualMachineConfigSpec{})
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("Delete", vm).Return(nil)
	isoMgr.On("DeleteFromDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	cfg := minimalConfig()
	cfg.Netmask = "invalid"

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), cfg, slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid netmask")
}

// =============================================================================
// Tests: happy path
// =============================================================================

func TestBootstrap_Success(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	wireSuccessfulMocks(vc, creator, isoMgr)

	b := testBootstrapper(vc, creator, isoMgr)
	result, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "test-vm", result.Name)
	assert.Equal(t, "192.168.1.10", result.IPAddress)
	assert.Equal(t, "test-vm", result.Hostname)
	assert.True(t, result.SSHReady)

	vc.AssertExpectations(t)
	creator.AssertExpectations(t)
	isoMgr.AssertExpectations(t)
}

func TestBootstrap_WithDataDisk(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	cfg := minimalConfig()
	dataDisk := 100
	cfg.DataDiskSizeGB = &dataDisk
	cfg.DataDiskMountPath = "/data"

	vm := fakeVM()
	spec := &types.VirtualMachineConfigSpec{}

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(spec)
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, spec).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)  // OS disk
	creator.On("AddDisk", vm, mock.Anything, int64(100), int32(0)).Return(nil) // data disk
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("PowerOn", vm).Return(nil)
	creator.On("PowerOff", vm).Return(nil)

	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").Return("/tmp/nocloud.iso", nil)
	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("UploadAlways", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("MountISOs", vm, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", vm).Return(nil)
	isoMgr.On("RemoveAllCDROMs", vm).Return(nil)
	isoMgr.On("DeleteFromDatastore", "SSD01", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	result, err := b.run(context.Background(), cfg, slog.Default())

	require.NoError(t, err)
	require.NotNil(t, result)
	creator.AssertNumberOfCalls(t, "AddDisk", 2) // OS + data disk
}

func TestBootstrap_InvalidConfig(t *testing.T) {
	cfg := &VMConfig{}
	_, err := Bootstrap(context.Background(), cfg)
	require.Error(t, err)
}

func TestBootstrap_PasswordHashPrecedence(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	cfg := minimalConfig()
	cfg.Password = "plaintext"
	cfg.PasswordHash = "HASHED_VALUE"

	vm := fakeVM()
	spec := &types.VirtualMachineConfigSpec{}

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(spec)
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, spec).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("PowerOn", vm).Return(nil)
	creator.On("PowerOff", vm).Return(nil)

	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)

	var capturedUserData string
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").
		Run(func(args mock.Arguments) {
			capturedUserData = args.String(0)
		}).Return("/tmp/nocloud.iso", nil)

	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("UploadAlways", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("MountISOs", vm, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", vm).Return(nil)
	isoMgr.On("RemoveAllCDROMs", vm).Return(nil)
	isoMgr.On("DeleteFromDatastore", "SSD01", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), cfg, slog.Default())

	require.NoError(t, err)
	require.Contains(t, capturedUserData, "HASHED_VALUE")
	require.NotContains(t, capturedUserData, "plaintext")
}

func TestVerifySSHAccess_FailsFast(t *testing.T) {
	oldRetries := configs.Defaults.Timeouts.SSHRetries
	oldConnect := configs.Defaults.Timeouts.SSHConnectSeconds
	oldDelay := configs.Defaults.Timeouts.SSHRetryDelaySeconds
	configs.Defaults.Timeouts.SSHRetries = 1
	configs.Defaults.Timeouts.SSHConnectSeconds = 1
	configs.Defaults.Timeouts.SSHRetryDelaySeconds = 0
	t.Cleanup(func() {
		configs.Defaults.Timeouts.SSHRetries = oldRetries
		configs.Defaults.Timeouts.SSHConnectSeconds = oldConnect
		configs.Defaults.Timeouts.SSHRetryDelaySeconds = oldDelay
	})

	err := verifySSHAccess(context.Background(), "203.0.113.1")
	require.Error(t, err)
}

func TestBootstrap_PasswordIsHashed(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	cfg := minimalConfig()
	cfg.Password = "plaintext"
	cfg.PasswordHash = ""

	vm := fakeVM()
	spec := &types.VirtualMachineConfigSpec{}

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(spec)
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, spec).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("PowerOn", vm).Return(nil)
	creator.On("PowerOff", vm).Return(nil)

	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)

	var capturedUserData string
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").
		Run(func(args mock.Arguments) {
			capturedUserData = args.String(0)
		}).Return("/tmp/nocloud.iso", nil)

	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("UploadAlways", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("MountISOs", vm, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", vm).Return(nil)
	isoMgr.On("RemoveAllCDROMs", vm).Return(nil)
	isoMgr.On("DeleteFromDatastore", "SSD01", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), cfg, slog.Default())

	require.NoError(t, err)
	require.NotContains(t, capturedUserData, "plaintext")
	require.Contains(t, capturedUserData, "$2")
}

func TestBootstrap_NoPasswordKeyOnly(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	cfg := minimalConfig()
	cfg.Password = ""
	cfg.PasswordHash = ""

	vm := fakeVM()
	spec := &types.VirtualMachineConfigSpec{}

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(spec)
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, spec).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("PowerOn", vm).Return(nil)
	creator.On("PowerOff", vm).Return(nil)

	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)

	var capturedUserData string
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").
		Run(func(args mock.Arguments) {
			capturedUserData = args.String(0)
		}).Return("/tmp/nocloud.iso", nil)

	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("UploadAlways", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("MountISOs", vm, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", vm).Return(nil)
	isoMgr.On("RemoveAllCDROMs", vm).Return(nil)
	isoMgr.On("DeleteFromDatastore", "SSD01", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), cfg, slog.Default())

	require.NoError(t, err)
	require.Contains(t, capturedUserData, "password: \"*\"")
}

func TestBootstrap_UploadNoCloudFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	vm := fakeVM()
	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	creator.On("CreateSpec", mock.Anything).Return(&types.VirtualMachineConfigSpec{})
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, int64(20), int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("GetMACAddress", vm).Return("00:50:56:00:00:01", nil)
	creator.On("Delete", vm).Return(nil)

	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").Return("/tmp/nocloud.iso", nil)
	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("UploadAlways", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("upload nocloud failed"))
	isoMgr.On("DeleteFromDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to upload NoCloud ISO")
}

func TestBootstrap_CDROMPostBootCheckFails(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)

	wireSuccessfulMocks(vc, creator, isoMgr)
	for _, call := range isoMgr.ExpectedCalls {
		if call.Method == "EnsureCDROMsConnectedAfterBoot" {
			call.ReturnArguments = mock.Arguments{errors.New("cdrom check failed")}
		}
	}

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())

	require.NoError(t, err)
}

func TestBootstrap_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	b := &bootstrapper{
		connectVCenter: func(ctx context.Context, cfg *VMConfig) (vcface.ClientInterface, error) {
			return nil, ctx.Err()
		},
		newVMCreator:  func(ctx context.Context) vmiface.CreatorInterface { return nil },
		newISOManager: func(ctx context.Context) isoiface.ManagerInterface { return nil },
	}

	_, err := b.run(ctx, minimalConfig(), slog.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "vCenter connection failed")
}

// =============================================================================
// Tests: pure Go helper functions
// =============================================================================

func TestIsPortOpen_closedPort(t *testing.T) {
	open := utils.IsPortOpen("127.0.0.1", 19999, 100*time.Millisecond)
	assert.False(t, open)
}

func TestIsPortOpen_openPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() {
		_ = ln.Close()
	}()

	addr := ln.Addr().(*net.TCPAddr)
	open := utils.IsPortOpen("127.0.0.1", addr.Port, time.Second)
	assert.True(t, open)
}

func TestIsPortOpen_invalidHost(t *testing.T) {
	open := utils.IsPortOpen("invalid-host-xyz", 80, 100*time.Millisecond)
	assert.False(t, open)
}

func TestVerifySSHAccess_portNotOpen(t *testing.T) {
	// Skip if SSH daemon is actually running on localhost (common in dev environments)
	if utils.IsPortOpen("127.0.0.1", 22, 200*time.Millisecond) {
		t.Skip("SSH daemon running on localhost - port 22 is open, skipping negative test")
	}
	err := verifySSHAccess(context.Background(), "127.0.0.1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSH port 22 not accessible")
}

func TestVerifySSHAccess_portOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() {
		_ = ln.Close()
	}()

	addr := ln.Addr().(*net.TCPAddr)
	open := utils.IsPortOpen("127.0.0.1", addr.Port, time.Second)
	assert.True(t, open)
}
