package vm

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

func nicStates(t *testing.T, env *simEnv, vm *object.VirtualMachine) []types.VirtualDeviceConnectInfo {
	t.Helper()
	devices, err := vm.Device(env.ctx)
	require.NoError(t, err)
	var out []types.VirtualDeviceConnectInfo
	for _, d := range devices.SelectByType((*types.VirtualEthernetCard)(nil)) {
		out = append(out, *d.(types.BaseVirtualEthernetCard).GetVirtualEthernetCard().Connectable)
	}
	return out
}

func simNetworkName(t *testing.T, env *simEnv) string {
	t.Helper()
	_, _, _, network := env.defaultObjects(t)
	name, err := object.NewCommon(env.client.Client, network.Reference()).ObjectName(env.ctx)
	require.NoError(t, err)
	return name
}

func TestSetNICConnected_ConnectDisconnectIdempotent(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()
	creator, vm, _ := createTestVM(t, env, "vm-nic-connect")
	_, _, _, network := env.defaultObjects(t)
	name := simNetworkName(t, env)

	_, err := creator.AddExtraNetworkAdapter(vm, network, false, false)
	require.NoError(t, err)
	before := nicStates(t, env, vm)
	require.False(t, before[0].Connected)
	require.False(t, before[0].StartConnected)

	// dry-run changes nothing
	res, err := SetNICConnected(env.ctx, vm, name, true, true)
	require.NoError(t, err)
	require.True(t, res.Changed)
	require.False(t, nicStates(t, env, vm)[0].StartConnected)

	res, err = SetNICConnected(env.ctx, vm, name, true, false)
	require.NoError(t, err)
	require.True(t, res.Changed)
	st := nicStates(t, env, vm)[0]
	require.True(t, st.StartConnected, "VM is powered off: StartConnected is the effective state")
	require.False(t, st.AllowGuestControl, "other settings untouched")

	res, err = SetNICConnected(env.ctx, vm, name, true, false)
	require.NoError(t, err)
	require.False(t, res.Changed, "already connected is a no-op")

	res, err = SetNICConnected(env.ctx, vm, name, false, false)
	require.NoError(t, err)
	require.True(t, res.Changed)
	st = nicStates(t, env, vm)[0]
	require.False(t, st.StartConnected)

	res, err = SetNICConnected(env.ctx, vm, name, false, false)
	require.NoError(t, err)
	require.False(t, res.Changed)
}

func TestSetNICConnected_NotFoundAndAmbiguous(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()
	creator, vm, _ := createTestVM(t, env, "vm-nic-errors")
	_, _, _, network := env.defaultObjects(t)
	name := simNetworkName(t, env)

	_, err := creator.AddExtraNetworkAdapter(vm, network, false, false)
	require.NoError(t, err)
	_, err = SetNICConnected(env.ctx, vm, "no-such-portgroup", true, false)
	require.ErrorContains(t, err, "no NIC on network")

	_, err = creator.AddExtraNetworkAdapter(vm, network, false, false)
	require.NoError(t, err)
	_, err = SetNICConnected(env.ctx, vm, name, true, false)
	require.ErrorContains(t, err, "expected exactly one")
	for _, s := range nicStates(t, env, vm) {
		require.False(t, s.Connected, "nothing changed on error")
	}
}
