package vm

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

func diskCount(t *testing.T, env *simEnv, vm *object.VirtualMachine) int {
	t.Helper()
	devices, err := vm.Device(env.ctx)
	require.NoError(t, err)
	return len(devices.SelectByType((*types.VirtualDisk)(nil)))
}

func runningVMWithBoot(t *testing.T, env *simEnv, name string) (*Creator, *object.VirtualMachine, *object.Datastore) {
	creator, vm, ds := createTestVM(t, env, name)
	key, err := creator.EnsureSCSIController(vm)
	require.NoError(t, err)
	require.NoError(t, creator.AddDisk(vm, ds, ds.Name(), 1, key))
	require.NoError(t, creator.PowerOn(vm))
	return creator, vm, ds
}

func TestEnsureExtraDisk_AddIdempotentDryRun(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()
	c, vm, ds := runningVMWithBoot(t, env, "vm-disk-add")

	res, err := EnsureExtraDisk(env.ctx, c, vm, ds, ds.Name(), 2, -1, 1, true)
	require.NoError(t, err)
	require.True(t, res.Changed)
	require.Equal(t, int32(0), res.Unit, "first free unit (simulator puts the boot disk on unit 1)")
	require.Equal(t, 1, diskCount(t, env, vm), "dry-run changes nothing")

	res, err = EnsureExtraDisk(env.ctx, c, vm, ds, ds.Name(), 2, -1, 1, false)
	require.NoError(t, err)
	require.True(t, res.Changed)
	require.Equal(t, 2, diskCount(t, env, vm))
	state, err := vm.PowerState(env.ctx)
	require.NoError(t, err)
	require.Equal(t, types.VirtualMachinePowerStatePoweredOn, state)

	res, err = EnsureExtraDisk(env.ctx, c, vm, ds, ds.Name(), 2, -1, 1, false)
	require.NoError(t, err)
	require.False(t, res.Changed, "second run is a no-op")
	require.Equal(t, 2, diskCount(t, env, vm))

	// boot disk (1 GB) is reserved: same size still adds a new disk
	res, err = EnsureExtraDisk(env.ctx, c, vm, ds, ds.Name(), 1, -1, 1, false)
	require.NoError(t, err)
	require.True(t, res.Changed)
	require.Equal(t, 3, diskCount(t, env, vm))
}

func TestEnsureExtraDisk_UnitConflictAndSameUnitIdempotent(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()
	c, vm, ds := runningVMWithBoot(t, env, "vm-disk-unit")

	_, err := EnsureExtraDisk(env.ctx, c, vm, ds, ds.Name(), 2, 1, 1, false)
	require.Error(t, err, "unit 1 holds the boot disk")
	require.Equal(t, 1, diskCount(t, env, vm))

	res, err := EnsureExtraDisk(env.ctx, c, vm, ds, ds.Name(), 2, 3, 1, false)
	require.NoError(t, err)
	require.True(t, res.Changed)
	require.Equal(t, int32(3), res.Unit)

	res, err = EnsureExtraDisk(env.ctx, c, vm, ds, ds.Name(), 2, 3, 1, false)
	require.NoError(t, err)
	require.False(t, res.Changed)

	_, err = EnsureExtraDisk(env.ctx, c, vm, ds, ds.Name(), 5, 3, 1, false)
	require.Error(t, err, "unit 3 taken by a different size")
	require.Equal(t, 2, diskCount(t, env, vm))
}

func TestEnsureExtraDisk_WrongDatastore(t *testing.T) {
	env, cleanup := newSimEnv(t)
	defer cleanup()
	_, _, _, _ = env.defaultObjects(t)
	_, err := env.finder.Datastore(env.ctx, "no-such-datastore")
	require.Error(t, err, "datastore lookup fails before any change (CLI path: vclient.FindDatastore)")
}
