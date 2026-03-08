package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/infrakit-io/vmware-vm-bootstrap/configs"
)

func newSimVM(t *testing.T) (*simulator.Model, *govmomi.Client, *find.Finder, func()) {
	t.Helper()

	model := simulator.VPX()
	model.Datacenter = 1
	model.Cluster = 1
	model.Host = 1
	model.Pool = 1
	model.Machine = 1
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

	return model, c, f, cleanup
}

func overrideTimeouts(t *testing.T, fn func()) {
	t.Helper()
	old := configs.Defaults.Timeouts
	configs.Defaults.Timeouts.InstallationMinutes = 1
	configs.Defaults.Timeouts.PollingSeconds = 1
	configs.Defaults.Timeouts.HostnameChecks = 1
	configs.Defaults.Timeouts.ServiceStartupSeconds = 0
	fn()
	configs.Defaults.Timeouts = old
}

func TestWaitForInstallation_RebootPath(t *testing.T) {
	model, _, finder, cleanup := newSimVM(t)
	defer cleanup()

	ctx := context.Background()
	dc, err := finder.DefaultDatacenter(ctx)
	require.NoError(t, err)
	finder.SetDatacenter(dc)

	vms, err := finder.VirtualMachineList(ctx, "*")
	require.NoError(t, err)
	require.NotEmpty(t, vms)
	vmObj := vms[0]

	ref := vmObj.Reference()
	obj := model.Service.Context.Map.Get(ref)
	simVM, ok := obj.(*simulator.VirtualMachine)
	require.True(t, ok)
	simVM.Guest = &types.GuestInfo{}

	cfg := minimalConfig()

	go func() {
		time.Sleep(500 * time.Millisecond)
		simVM.Guest.ToolsRunningStatus = "guestToolsRunning"
		simVM.Guest.HostName = "temp"

		time.Sleep(1200 * time.Millisecond)
		simVM.Guest.ToolsRunningStatus = "guestToolsNotRunning"

		time.Sleep(1200 * time.Millisecond)
		simVM.Guest.ToolsRunningStatus = "guestToolsRunning"
		simVM.Guest.HostName = cfg.Name
	}()

	overrideTimeouts(t, func() {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		err := waitForInstallation(ctx, vmObj, cfg, logger)
		require.NoError(t, err)
	})
}

func TestWaitForInstallation_NoRebootPath(t *testing.T) {
	model, _, finder, cleanup := newSimVM(t)
	defer cleanup()

	ctx := context.Background()
	dc, err := finder.DefaultDatacenter(ctx)
	require.NoError(t, err)
	finder.SetDatacenter(dc)

	vms, err := finder.VirtualMachineList(ctx, "*")
	require.NoError(t, err)
	require.NotEmpty(t, vms)
	vmObj := vms[0]

	ref := vmObj.Reference()
	obj := model.Service.Context.Map.Get(ref)
	simVM, ok := obj.(*simulator.VirtualMachine)
	require.True(t, ok)
	simVM.Guest = &types.GuestInfo{
		ToolsRunningStatus: "guestToolsRunning",
	}

	cfg := minimalConfig()
	simVM.Guest.HostName = cfg.Name

	overrideTimeouts(t, func() {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		err := waitForInstallation(ctx, vmObj, cfg, logger)
		require.NoError(t, err)
	})
}
