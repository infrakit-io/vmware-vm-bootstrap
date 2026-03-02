package bootstrap

import (
	"context"
	"crypto/tls"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/types"
)

type simBootstrapEnv struct {
	ctx    context.Context
	client *govmomi.Client
	finder *find.Finder
	url    *url.URL
	model  *simulator.Model
}

func newSimBootstrapEnv(t *testing.T) (*simBootstrapEnv, func()) {
	t.Helper()

	model := simulator.VPX()
	model.Datacenter = 1
	model.Cluster = 1
	model.Host = 1
	model.Pool = 1
	model.Machine = 1

	require.NoError(t, model.Create())
	model.Service.TLS = new(tls.Config)
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

	return &simBootstrapEnv{ctx: ctx, client: c, finder: f, url: u, model: model}, cleanup
}

func (e *simBootstrapEnv) findFirstVM(t *testing.T) *types.ManagedObjectReference {
	t.Helper()

	dc, err := e.finder.DefaultDatacenter(e.ctx)
	require.NoError(t, err)
	e.finder.SetDatacenter(dc)

	vms, err := e.finder.VirtualMachineList(e.ctx, "*")
	require.NoError(t, err)
	require.NotEmpty(t, vms)

	ref := vms[0].Reference()
	return &ref
}

func (e *simBootstrapEnv) newVM(t *testing.T) *VM {
	t.Helper()

	ref := e.findFirstVM(t)

	return &VM{
		Name:            "sim-vm",
		IPAddress:       "192.0.2.10",
		ManagedObject:   *ref,
		Hostname:        "",
		VCenterHost:     e.url.String(),
		VCenterPort:     0,
		VCenterUser:     simulator.DefaultLogin.Username(),
		VCenterPass:     func() string { p, _ := simulator.DefaultLogin.Password(); return p }(),
		VCenterInsecure: true,
	}
}

func TestVM_Verify(t *testing.T) {
	env, cleanup := newSimBootstrapEnv(t)
	defer cleanup()

	vm := env.newVM(t)
	old := sshVerifier
	sshVerifier = func(_ context.Context, _ string) error { return nil }
	t.Cleanup(func() { sshVerifier = old })

	simObj := env.model.Service.Context.Map.Get(vm.ManagedObject).(*simulator.VirtualMachine)
	simObj.Guest.ToolsRunningStatus = string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)

	require.NoError(t, vm.Verify(context.Background()))
}

func TestVM_PowerOnOffDelete(t *testing.T) {
	env, cleanup := newSimBootstrapEnv(t)
	defer cleanup()

	vm := env.newVM(t)

	require.NoError(t, vm.PowerOn(context.Background()))
	require.NoError(t, vm.PowerOff(context.Background()))
	require.NoError(t, vm.PowerOn(context.Background()))
	require.NoError(t, vm.Delete(context.Background()))
}

func TestVM_Verify_FailsWhenIPMissing(t *testing.T) {
	env, cleanup := newSimBootstrapEnv(t)
	defer cleanup()

	vm := env.newVM(t)
	vm.IPAddress = ""

	simObj := env.model.Service.Context.Map.Get(vm.ManagedObject).(*simulator.VirtualMachine)
	simObj.Guest.ToolsRunningStatus = string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)

	err := vm.Verify(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "IPAddress is required")
}

func TestVM_Verify_FailsWhenSSHFails(t *testing.T) {
	env, cleanup := newSimBootstrapEnv(t)
	defer cleanup()

	vm := env.newVM(t)
	simObj := env.model.Service.Context.Map.Get(vm.ManagedObject).(*simulator.VirtualMachine)
	simObj.Guest.ToolsRunningStatus = string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)

	old := sshVerifier
	sshVerifier = func(_ context.Context, _ string) error { return errors.New("ssh down") }
	t.Cleanup(func() { sshVerifier = old })

	err := vm.Verify(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "SSH verification failed")
}

func TestVM_MethodsFailWithInvalidManagedObject(t *testing.T) {
	env, cleanup := newSimBootstrapEnv(t)
	defer cleanup()

	vm := env.newVM(t)
	vm.ManagedObject = types.ManagedObjectReference{}

	err := vm.Verify(context.Background())
	require.Error(t, err)

	err = vm.PowerOff(context.Background())
	require.Error(t, err)

	err = vm.PowerOn(context.Background())
	require.Error(t, err)

	err = vm.Delete(context.Background())
	require.Error(t, err)
}
