package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/object"

	isoiface "github.com/infrakit-io/vmware-vm-bootstrap/pkg/iso"
	isomocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/iso/mocks"
	vcface "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter"
	vcmocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vcenter/mocks"
	vmiface "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm"
	vmmocks "github.com/infrakit-io/vmware-vm-bootstrap/pkg/vm/mocks"
)

func TestBootstrap_ExtraNICsTagsHotAddDataDatastore(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)
	wireSuccessfulMocks(vc, creator, isoMgr)

	dataDS := &object.Datastore{Common: object.Common{InventoryPath: "/DC1/datastore/HDD01"}}
	extraNet := &object.Network{Common: object.Common{InventoryPath: "/DC1/network/BACKUP"}}
	vc.On("FindDatastore", "DC1", "HDD01").Return(dataDS, nil)
	vc.On("FindNetwork", "DC1", "BACKUP").Return(extraNet, nil)
	vc.On("EnsureVMTags", mock.Anything, []vcface.TagRef{{Category: "Backup", Tag: "no-backup"}}).Return([]string(nil), nil)

	var order []string
	creator.On("SetMACAddress", mock.Anything, "00:50:56:aa:bb:cc").
		Run(func(mock.Arguments) { order = append(order, "SetMAC") }).Return("00:50:56:aa:bb:cc", nil)
	creator.On("AddExtraNetworkAdapter", mock.Anything, extraNet, false, false).
		Run(func(mock.Arguments) { order = append(order, "ExtraNIC") }).Return("00:50:56:aa:bb:02", nil)
	creator.On("AddDisk", mock.Anything, dataDS, "HDD01", int64(100), int32(0)).Return(nil)
	creator.On("AddDisk", mock.Anything, mock.Anything, "", int64(20), int32(0)).Return(nil)

	cpuOn, memOff := true, false
	cfg := minimalConfig()
	dataSize := 100
	cfg.DataDiskSizeGB = &dataSize
	cfg.DataDiskMountPath = "/data"
	cfg.DataDiskDatastore = "HDD01"
	cfg.MACAddress = "00:50:56:aa:bb:cc"
	cfg.CPUHotAdd = &cpuOn
	cfg.MemoryHotAdd = &memOff
	cfg.ExtraNICs = []NIC{{Network: "BACKUP", IP: "10.0.0.5", Netmask: "255.255.255.0", Connected: false}}
	cfg.Tags = []Tag{{Category: "Backup", Tag: "no-backup"}}

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), cfg, slog.Default())
	require.NoError(t, err)

	assert.Equal(t, []string{"SetMAC", "ExtraNIC"}, order, "extra NICs must follow SetMACAddress")
	vc.AssertCalled(t, "EnsureVMTags", mock.Anything, []vcface.TagRef{{Category: "Backup", Tag: "no-backup"}})
	creator.AssertCalled(t, "AddDisk", mock.Anything, dataDS, "HDD01", int64(100), int32(0))
	creator.AssertCalled(t, "CreateSpec", mock.MatchedBy(func(c *vmiface.Config) bool {
		return c.CPUHotAdd == &cpuOn && c.MemoryHotAdd == &memOff
	}))
}

func TestBootstrap_DefaultsLeaveNewOptionsUntouched(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)
	wireSuccessfulMocks(vc, creator, isoMgr)

	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), minimalConfig(), slog.Default())
	require.NoError(t, err)

	creator.AssertNotCalled(t, "AddExtraNetworkAdapter", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	vc.AssertNotCalled(t, "EnsureVMTags", mock.Anything, mock.Anything)
	creator.AssertCalled(t, "CreateSpec", mock.MatchedBy(func(c *vmiface.Config) bool {
		return c.CPUHotAdd == nil && c.MemoryHotAdd == nil
	}))
}

func TestBootstrap_TagFailureIsNonFatal(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)
	wireSuccessfulMocks(vc, creator, isoMgr)
	vc.On("EnsureVMTags", mock.Anything, mock.Anything).Return([]string{"skipped"}, errors.New("rest down"))

	cfg := minimalConfig()
	cfg.Tags = []Tag{{Category: "Backup", Tag: "no-backup"}}
	b := testBootstrapper(vc, creator, isoMgr)
	result, err := b.run(context.Background(), cfg, slog.Default())

	require.NoError(t, err)
	require.NotNil(t, result)
	creator.AssertNotCalled(t, "Delete", mock.Anything)
}

func TestBootstrap_TagsAppliedOnlyAfterEverythingElse(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)
	wireSuccessfulMocks(vc, creator, isoMgr)
	isoMgr.ExpectedCalls = nil
	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").Return("/tmp/nocloud.iso", nil)
	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("network error"))
	vc.On("EnsureVMTags", mock.Anything, mock.Anything).Return([]string(nil), nil)

	cfg := minimalConfig()
	cfg.Tags = []Tag{{Category: "Backup", Tag: "no-backup"}}
	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), cfg, slog.Default())

	require.Error(t, err)
	vc.AssertNotCalled(t, "EnsureVMTags", mock.Anything, mock.Anything)
}

func TestBootstrap_ExtraNICNetworkNotFound(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)
	wireSuccessfulMocks(vc, creator, isoMgr)
	vc.On("FindNetwork", "DC1", "NOPE").Return(nil, errors.New("not found"))

	cfg := minimalConfig()
	cfg.ExtraNICs = []NIC{{Network: "NOPE", IP: "10.0.0.5", Netmask: "255.255.255.0"}}
	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), cfg, slog.Default())

	require.ErrorContains(t, err, "extra NIC network")
	creator.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestBootstrap_DataDiskDatastoreNotFound(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)
	wireSuccessfulMocks(vc, creator, isoMgr)
	vc.On("FindDatastore", "DC1", "NOPE").Return(nil, errors.New("not found"))

	cfg := minimalConfig()
	size := 50
	cfg.DataDiskSizeGB, cfg.DataDiskMountPath, cfg.DataDiskDatastore = &size, "/data", "NOPE"
	b := testBootstrapper(vc, creator, isoMgr)
	_, err := b.run(context.Background(), cfg, slog.Default())

	require.ErrorContains(t, err, "data disk datastore")
}

func TestValidate_NewOptions(t *testing.T) {
	mk := func(mut func(*VMConfig)) error {
		c := minimalConfig()
		mut(c)
		return c.Validate()
	}
	good := NIC{Network: "N", IP: "10.0.0.5", Netmask: "255.255.255.0"}

	assert.NoError(t, mk(func(c *VMConfig) { c.ExtraNICs = []NIC{good}; c.Tags = []Tag{{"Backup", "no-backup"}} }))
	assert.ErrorContains(t, mk(func(c *VMConfig) { n := good; n.Network = ""; c.ExtraNICs = []NIC{n} }), "Network is required")
	assert.ErrorContains(t, mk(func(c *VMConfig) { n := good; n.IP = "x"; c.ExtraNICs = []NIC{n} }), "ExtraNICs[0]")
	assert.ErrorContains(t, mk(func(c *VMConfig) { n := good; n.Netmask = "x"; c.ExtraNICs = []NIC{n} }), "netmask")
	assert.ErrorContains(t, mk(func(c *VMConfig) {
		c.Profile = "talos"
		c.Profiles.Talos = TalosProfile{Version: "v1.12.4", SchematicID: "abc"}
		c.ExtraNICs = []NIC{good}
	}), "only supported for the ubuntu profile")
	assert.ErrorContains(t, mk(func(c *VMConfig) { c.Tags = []Tag{{"", "x"}} }), "Tags[0]")
	assert.ErrorContains(t, mk(func(c *VMConfig) { c.DataDiskDatastore = "HDD" }), "DataDiskDatastore requires")

	talos := func(c *VMConfig) {
		c.Profile = "talos"
		c.Profiles.Talos = TalosProfile{Version: "v1.12.4", SchematicID: "abc"}
	}
	on := true
	assert.NoError(t, mk(talos))
	assert.ErrorContains(t, mk(func(c *VMConfig) { talos(c); c.Tags = []Tag{{"Backup", "no-backup"}} }), "not supported for the talos profile")
	assert.ErrorContains(t, mk(func(c *VMConfig) { talos(c); c.CPUHotAdd = &on }), "not supported for the talos profile")
	assert.ErrorContains(t, mk(func(c *VMConfig) { talos(c); c.MemoryHotAdd = &on }), "not supported for the talos profile")
	assert.ErrorContains(t, mk(func(c *VMConfig) { talos(c); c.DataDiskDatastore = "HDD" }), "not supported for the talos profile")
}

func TestBootstrap_FailureKeptVMErrorReportsPowerOffResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offErr error
		want   bool
	}{{"power-off ok", nil, true}, {"power-off fails", errors.New("task failed"), false}} {
		t.Run(tc.name, func(t *testing.T) {
			vc := new(vcmocks.ClientInterface)
			creator := new(vmmocks.CreatorInterface)
			isoMgr := new(isomocks.ManagerInterface)
			wireSuccessfulMocks(vc, creator, isoMgr)
			creator.ExpectedCalls = filterCalls(creator.ExpectedCalls, "PowerOff")
			creator.On("PowerOff", mock.Anything).Return(tc.offErr)
			isoMgr.ExpectedCalls = nil
			isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
			isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)
			isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").Return("/tmp/nocloud.iso", nil)
			isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("network error"))

			b := testBootstrapper(vc, creator, isoMgr)
			_, err := b.run(context.Background(), minimalConfig(), slog.Default())

			var kept *KeptVMError
			require.ErrorAs(t, err, &kept)
			assert.Equal(t, "test-vm", kept.Name)
			assert.Equal(t, tc.want, kept.PoweredOff)
			assert.Contains(t, err.Error(), "failed to upload Ubuntu ISO", "original message preserved")
			creator.AssertNotCalled(t, "Delete", mock.Anything)
		})
	}
}

func TestBootstrap_FailurePowerOffUsesFreshContext(t *testing.T) {
	vc := new(vcmocks.ClientInterface)
	creator := new(vmmocks.CreatorInterface)
	isoMgr := new(isomocks.ManagerInterface)
	wireSuccessfulMocks(vc, creator, isoMgr)
	isoMgr.ExpectedCalls = nil
	isoMgr.On("DownloadUbuntu", "24.04").Return("/tmp/ubuntu.iso", nil)
	isoMgr.On("ModifyUbuntuISO", "/tmp/ubuntu.iso").Return("/tmp/ubuntu-autoinstall.iso", false, nil)
	isoMgr.On("CreateNoCloudISO", mock.Anything, mock.Anything, mock.Anything, "test-vm").Return("/tmp/nocloud.iso", nil)
	isoMgr.On("UploadToDatastore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("network error"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var offCtxErr error
	b := testBootstrapper(vc, creator, isoMgr)
	b.newVMCreator = func(c context.Context) vmiface.CreatorInterface {
		offCtxErr = c.Err() // the creator built in the defer must not inherit a cancelled ctx
		return creator
	}
	b.newISOManager = func(c context.Context) isoiface.ManagerInterface {
		cancel() // simulate the caller's context being cancelled during the failure
		return isoMgr
	}
	_, err := b.run(ctx, minimalConfig(), slog.Default())
	require.Error(t, err)
	assert.NoError(t, offCtxErr)
}

func filterCalls(calls []*mock.Call, drop string) []*mock.Call {
	out := calls[:0]
	for _, c := range calls {
		if c.Method != drop {
			out = append(out, c)
		}
	}
	return out
}
