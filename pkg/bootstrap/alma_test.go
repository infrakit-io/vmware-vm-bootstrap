package bootstrap

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"

	isomocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/iso/mocks"
	"github.com/infrakit-io/vmware-vm-bootstrap/pkg/profile"
	almaprofile "github.com/infrakit-io/vmware-vm-bootstrap/pkg/profile/alma"
	vcmocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter/mocks"
	vmiface "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm"
	vmmocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm/mocks"
)

func almaConfig() *VMConfig {
	cfg := minimalConfig()
	cfg.Profile = "alma"
	cfg.Profiles = VMProfiles{Alma: AlmaProfile{Version: "9.8"}}
	cfg.Username = "root"
	cfg.MACAddress = "00:50:56:aa:bb:cc"
	size := 100
	cfg.DataDiskSizeGB = &size
	cfg.DataDiskMountPath = "/home"
	return cfg
}

func TestValidate_Alma(t *testing.T) {
	require.NoError(t, almaConfig().Validate())

	cfg := almaConfig()
	cfg.Profiles.Alma.Version = ""
	assert.ErrorContains(t, cfg.Validate(), "Profiles.Alma.Version is required")

	cfg = almaConfig()
	cfg.Profiles.Alma.Version = "9.99"
	assert.ErrorContains(t, cfg.Validate(), "unsupported AlmaLinux version")

	cfg = almaConfig()
	cfg.Firmware = "efi"
	assert.ErrorContains(t, cfg.Validate(), "only Firmware")

	cfg = almaConfig()
	cfg.Username = ""
	assert.ErrorContains(t, cfg.Validate(), "username is required")

	cfg = almaConfig()
	cfg.SSHPublicKeys = nil
	assert.Error(t, cfg.Validate())

	cfg = almaConfig()
	cfg.ExtraNICs = []NIC{{Network: "n", IP: "10.0.0.5", Netmask: "255.255.255.0"}}
	assert.Error(t, cfg.Validate())

	cfg = almaConfig()
	cfg.Profile = "centos"
	assert.ErrorContains(t, cfg.Validate(), "supported: ubuntu, alma, talos")

	cfg = almaConfig()
	assert.Equal(t, "alma", cfg.EffectiveProfile())
	assert.Equal(t, "9.8", cfg.EffectiveOSVersion())
}

func TestResolveProfile_Alma(t *testing.T) {
	p, err := defaultBootstrapper().resolveProfile("alma")
	require.NoError(t, err)
	assert.Equal(t, "alma", p.Name())
}

func TestBootstrap_AlmaFlow(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)
	vm := fakeVM()
	spec := &types.VirtualMachineConfigSpec{}

	vc.On("FindVM", "DC1", "test-vm").Return(nil, nil)
	vc.On("FindFolder", "DC1", "Production").Return(&object.Folder{}, nil)
	vc.On("FindResourcePool", "DC1", "pool").Return(&object.ResourcePool{}, nil)
	vc.On("FindDatastore", "DC1", "SSD01").Return(&object.Datastore{}, nil)
	vc.On("FindNetwork", "DC1", "LAN").Return((*object.Network)(nil), nil)
	vc.On("Disconnect").Return(nil)

	var gotCfg *vmiface.Config
	creator.On("CreateSpec", mock.Anything).Run(func(a mock.Arguments) { gotCfg = a.Get(0).(*vmiface.Config) }).Return(spec)
	creator.On("Create", mock.Anything, mock.Anything, mock.Anything, spec).Return(vm, nil)
	creator.On("EnsureSCSIController", vm).Return(int32(0), nil)
	creator.On("AddDisk", vm, mock.Anything, mock.Anything, mock.Anything, int32(0)).Return(nil)
	creator.On("AddNetworkAdapter", vm, mock.Anything).Return(nil)
	creator.On("SetMACAddress", vm, "00:50:56:aa:bb:cc").Return("00:50:56:aa:bb:cc", nil)
	creator.On("PowerOn", vm).Return(nil)
	creator.On("PowerOff", vm).Return(nil)

	var ks string
	isoMgr.On("DownloadAlma", "9.8").Return("/tmp/alma.iso", nil)
	isoMgr.On("ModifyAlmaISO", "/tmp/alma.iso").Return("/tmp/alma-unattended.iso", false, nil)
	isoMgr.On("CreateKickstartISO", mock.Anything, "test-vm").Run(func(a mock.Arguments) { ks = a.String(0) }).Return("/tmp/oemdrv.iso", nil)
	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("UploadAlways", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("MountISOs", vm, mock.Anything, mock.Anything).Return(nil)
	isoMgr.On("EnsureCDROMsConnectedAfterBoot", vm).Return(nil)
	isoMgr.On("RemoveAllCDROMs", vm).Return(nil)
	isoMgr.On("DeleteFromDatastore", "SSD01", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	var order []string
	b := testBootstrapper(vc, creator, isoMgr)
	b.resolveProfile = func(string) (profile.Provisioner, error) { return almaprofile.New(), nil }
	b.waitPowerOff = func(context.Context, *object.VirtualMachine, *slog.Logger) error {
		order = append(order, "powered-off")
		return nil
	}
	b.waitInstall = func(context.Context, *object.VirtualMachine, *VMConfig, *slog.Logger) error {
		order = append(order, "first-boot")
		return nil
	}
	b.checkSSH = func(context.Context, string) error { order = append(order, "ssh"); return nil }

	result, err := b.run(context.Background(), almaConfig(), slog.Default())
	require.NoError(t, err)
	assert.True(t, result.SSHReady)
	assert.Equal(t, []string{"powered-off", "first-boot", "ssh"}, order)
	require.NotNil(t, gotCfg)
	assert.Equal(t, "rhel9_64Guest", gotCfg.GuestOS, "EL9 vSphere guest ID")
	assert.True(t, gotCfg.ExplicitFirmware, "alma spec must carry Firmware=bios explicitly")
	assert.Contains(t, ks, "--device=00:50:56:aa:bb:cc")
	assert.Contains(t, ks, "part /home ")
	isoMgr.AssertNotCalled(t, "DownloadUbuntu", mock.Anything)
}

func TestBootstrap_AlmaInstallNeverPowersOff(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)
	wireSuccessfulMocks(vc, creator, isoMgr)
	creator.On("SetMACAddress", mock.Anything, mock.Anything).Return("00:50:56:aa:bb:cc", nil)
	isoMgr.On("DownloadAlma", "9.8").Return("/tmp/alma.iso", nil)
	isoMgr.On("ModifyAlmaISO", "/tmp/alma.iso").Return("/tmp/alma-unattended.iso", false, nil)
	isoMgr.On("CreateKickstartISO", mock.Anything, "test-vm").Return("/tmp/oemdrv.iso", nil)

	b := testBootstrapper(vc, creator, isoMgr)
	b.resolveProfile = func(string) (profile.Provisioner, error) { return almaprofile.New(), nil }
	b.waitPowerOff = func(context.Context, *object.VirtualMachine, *slog.Logger) error {
		return assert.AnError
	}
	cfg := almaConfig()
	cfg.DataDiskSizeGB, cfg.DataDiskMountPath = nil, ""
	_, err := b.run(context.Background(), cfg, slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "installation failed")
}
