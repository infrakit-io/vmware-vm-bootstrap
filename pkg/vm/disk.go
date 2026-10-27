package vm

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

const scsiReservedUnit = 7 // reserved for the SCSI controller itself

// DiskChange describes the outcome of EnsureExtraDisk.
type DiskChange struct {
	ControllerKey int32
	Unit          int32
	Datastore     string
	SizeGB        int64
	Changed       bool // false: an equivalent disk already exists
}

// diskDatastore extracts the datastore name from a "[ds] path" backing file name.
func diskDatastore(fileName string) string {
	if !strings.HasPrefix(fileName, "[") {
		return ""
	}
	if i := strings.Index(fileName, "]"); i > 0 {
		return fileName[1:i]
	}
	return ""
}

// EnsureExtraDisk adds a thin disk of sizeGB on dsName to the VM's SCSI
// controller. unit < 0 means the first free unit. The first `reserved` disks
// (boot, data) are ignored when deciding whether the disk is already present:
// an extra disk of the same size on dsName (or at the requested unit) means
// "already present". A requested unit holding a disk of another size/datastore
// is refused. dryRun reports the plan without reconfiguring. The VM power state
// is not touched.
func EnsureExtraDisk(ctx context.Context, c *Creator, vm *object.VirtualMachine, ds *object.Datastore, dsName string, sizeGB int64, unit int32, reserved int, dryRun bool) (*DiskChange, error) {
	devices, err := vm.Device(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get VM devices: %w", err)
	}
	controller, err := devices.FindDiskController("scsi")
	if err != nil {
		return nil, fmt.Errorf("failed to find SCSI controller: %w", err)
	}
	ctrlKey := controller.GetVirtualController().Key

	used := map[int32]bool{scsiReservedUnit: true}
	var disks []*types.VirtualDisk
	for _, d := range devices.SelectByType((*types.VirtualDisk)(nil)) {
		disk := d.(*types.VirtualDisk)
		if disk.ControllerKey != ctrlKey || disk.UnitNumber == nil {
			continue
		}
		disks = append(disks, disk)
		used[*disk.UnitNumber] = true
	}
	// Order by device key (creation order) so "first N" is boot then data.
	for i := range disks {
		for j := i + 1; j < len(disks); j++ {
			if disks[j].Key < disks[i].Key {
				disks[i], disks[j] = disks[j], disks[i]
			}
		}
	}

	res := &DiskChange{ControllerKey: ctrlKey, Datastore: dsName, SizeGB: sizeGB}
	wantKB := sizeGB * 1024 * 1024
	for idx, disk := range disks {
		u := *disk.UnitNumber
		if unit >= 0 && u == unit {
			if idx >= reserved && disk.CapacityInKB == wantKB && datastoreOf(disk) == dsName {
				res.Unit = u
				return res, nil
			}
			return nil, fmt.Errorf("SCSI unit %d is already taken by another disk (%d GB on %q)", u, disk.CapacityInKB/1024/1024, datastoreOf(disk))
		}
		if unit < 0 && idx >= reserved && disk.CapacityInKB == wantKB && datastoreOf(disk) == dsName {
			res.Unit = u
			return res, nil
		}
	}

	if unit < 0 {
		for u := int32(0); ; u++ {
			if !used[u] {
				unit = u
				break
			}
		}
	} else if used[unit] {
		return nil, fmt.Errorf("SCSI unit %d is reserved or taken", unit)
	}
	res.Unit = unit
	res.Changed = true
	if dryRun {
		return res, nil
	}
	if err := c.AddDiskAtUnit(vm, ds, dsName, sizeGB, unit); err != nil {
		return nil, fmt.Errorf("failed to add disk: %w", err)
	}
	return res, nil
}

func datastoreOf(disk *types.VirtualDisk) string {
	if b, ok := disk.Backing.(types.BaseVirtualDeviceFileBackingInfo); ok {
		return diskDatastore(b.GetVirtualDeviceFileBackingInfo().FileName)
	}
	return ""
}
