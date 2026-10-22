package vm

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// NICChange describes the outcome of SetNICConnected.
type NICChange struct {
	Network string
	MAC     string
	Label   string
	Changed bool // false: already in the requested state, nothing was reconfigured
}

// nicNetworkName returns the portgroup name a NIC is attached to.
func nicNetworkName(ctx context.Context, vm *object.VirtualMachine, card *types.VirtualEthernetCard) (string, error) {
	switch b := card.Backing.(type) {
	case *types.VirtualEthernetCardNetworkBackingInfo:
		return b.DeviceName, nil
	case *types.VirtualEthernetCardDistributedVirtualPortBackingInfo:
		pg := object.NewDistributedVirtualPortgroup(vm.Client(), types.ManagedObjectReference{
			Type: "DistributedVirtualPortgroup", Value: b.Port.PortgroupKey,
		})
		return pg.ObjectName(ctx)
	}
	return "", nil
}

// SetNICConnected sets Connectable.Connected and StartConnected on the single
// NIC attached to the named portgroup. Exactly one NIC must match. Only that
// NIC is edited (ReconfigVM edit); nothing else is touched. dryRun reports
// the plan without reconfiguring.
func SetNICConnected(ctx context.Context, vm *object.VirtualMachine, network string, connected, dryRun bool) (*NICChange, error) {
	devices, err := vm.Device(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get VM devices: %w", err)
	}

	var matches []types.BaseVirtualDevice
	for _, d := range devices.SelectByType((*types.VirtualEthernetCard)(nil)) {
		card := d.(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
		name, err := nicNetworkName(ctx, vm, card)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve NIC network: %w", err)
		}
		if name == network {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no NIC on network %q", network)
	case 1:
	default:
		return nil, fmt.Errorf("%d NICs on network %q, expected exactly one", len(matches), network)
	}

	dev := matches[0]
	card := dev.(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	res := &NICChange{Network: network, MAC: card.MacAddress, Label: card.DeviceInfo.GetDescription().Label}

	// Connected is only meaningful while the VM is powered on; powered off,
	// StartConnected alone defines the state.
	powerState, err := vm.PowerState(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get power state: %w", err)
	}
	if c := card.Connectable; c != nil && c.StartConnected == connected &&
		(powerState != types.VirtualMachinePowerStatePoweredOn || c.Connected == connected) {
		return res, nil
	}
	res.Changed = true
	if dryRun {
		return res, nil
	}

	if card.Connectable == nil {
		card.Connectable = &types.VirtualDeviceConnectInfo{}
	}
	card.Connectable.Connected = connected
	card.Connectable.StartConnected = connected
	if err := vm.EditDevice(ctx, dev); err != nil {
		return nil, fmt.Errorf("failed to reconfigure NIC: %w", err)
	}
	return res, nil
}
