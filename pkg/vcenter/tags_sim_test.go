package vcenter

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vapi/rest"
	_ "github.com/vmware/govmomi/vapi/simulator"
	"github.com/vmware/govmomi/vapi/tags"
)

func simTagsManager(t *testing.T, c *Client) (*tags.Manager, func()) {
	t.Helper()
	rc := rest.NewClient(c.conn.Client)
	require.NoError(t, rc.Login(c.ctx, c.user))
	return tags.NewManager(rc), func() { _ = rc.Logout(c.ctx) }
}

func simVMs(t *testing.T, c *Client) []*object.VirtualMachine {
	t.Helper()
	dc, err := c.finder.DefaultDatacenter(c.ctx)
	require.NoError(t, err)
	c.finder.SetDatacenter(dc)
	vms, err := c.finder.VirtualMachineList(c.ctx, "*")
	require.NoError(t, err)
	require.NotEmpty(t, vms)
	return vms
}

func TestEnsureVMTags_CreatesAttachesAndIsIdempotent(t *testing.T) {
	client, ctx, cleanup := newSimClient(t)
	defer cleanup()

	vms := simVMs(t, client)
	vm := vms[0]
	refs := []TagRef{{Category: "Backup", Tag: "no-backup"}, {Category: "Backup", Tag: "other"}}

	_, err := client.EnsureVMTags(vm, refs)
	require.NoError(t, err)
	_, err = client.EnsureVMTags(vm, refs)
	require.NoError(t, err) // second run: no error, no duplicates

	m, done := simTagsManager(t, client)
	defer done()
	cats, err := m.GetCategories(ctx)
	require.NoError(t, err)
	n := 0
	for _, c := range cats {
		if c.Name == "Backup" {
			n++
			require.Equal(t, "MULTIPLE", c.Cardinality)
			require.Equal(t, []string{"VirtualMachine"}, c.AssociableTypes)
		}
	}
	require.Equal(t, 1, n, "category must be created once")

	attached, err := m.GetAttachedTags(ctx, vm)
	require.NoError(t, err)
	names := map[string]int{}
	for _, a := range attached {
		names[a.Name]++
	}
	require.Equal(t, map[string]int{"no-backup": 1, "other": 1}, names)
}

func TestEnsureVMTags_ReusesExistingCategoryAndTag(t *testing.T) {
	client, ctx, cleanup := newSimClient(t)
	defer cleanup()
	vms := simVMs(t, client)

	m, done := simTagsManager(t, client)
	defer done()
	catID, err := m.CreateCategory(ctx, &tags.Category{Name: "Backup", Cardinality: "MULTIPLE", AssociableTypes: []string{"VirtualMachine"}})
	require.NoError(t, err)
	tagID, err := m.CreateTag(ctx, &tags.Tag{Name: "no-backup", CategoryID: catID})
	require.NoError(t, err)

	_, err = client.EnsureVMTags(vms[0], []TagRef{{Category: "Backup", Tag: "no-backup"}})
	require.NoError(t, err)

	all, err := m.GetTags(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1, "pre-existing tag must be reused, not duplicated")
	attached, err := m.ListAttachedTags(ctx, vms[0])
	require.NoError(t, err)
	require.Equal(t, []string{tagID}, attached)
}

func TestEnsureVMTags_CategoryNotForVMs(t *testing.T) {
	client, ctx, cleanup := newSimClient(t)
	defer cleanup()
	vms := simVMs(t, client)

	m, done := simTagsManager(t, client)
	defer done()
	_, err := m.CreateCategory(ctx, &tags.Category{Name: "Hosts", Cardinality: "MULTIPLE", AssociableTypes: []string{"HostSystem"}})
	require.NoError(t, err)

	_, err = client.EnsureVMTags(vms[0], []TagRef{{Category: "Hosts", Tag: "x"}})
	require.ErrorContains(t, err, "cannot be associated with VirtualMachine")
}

func TestEnsureVMTags_EmptyIsNoop(t *testing.T) {
	w, err := (&Client{}).EnsureVMTags(nil, nil)
	require.NoError(t, err)
	require.Empty(t, w)
}

func TestEnsureVMTags_SingleCardinalityWarnsInsteadOfSecondTag(t *testing.T) {
	client, ctx, cleanup := newSimClient(t)
	defer cleanup()
	vms := simVMs(t, client)

	m, done := simTagsManager(t, client)
	defer done()
	catID, err := m.CreateCategory(ctx, &tags.Category{Name: "Tier", Cardinality: "SINGLE", AssociableTypes: []string{"VirtualMachine"}})
	require.NoError(t, err)
	goldID, err := m.CreateTag(ctx, &tags.Tag{Name: "gold", CategoryID: catID})
	require.NoError(t, err)
	require.NoError(t, m.AttachTag(ctx, goldID, vms[0]))

	warnings, err := client.EnsureVMTags(vms[0], []TagRef{{Category: "Tier", Tag: "silver"}})
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "SINGLE")

	attached, err := m.ListAttachedTags(ctx, vms[0])
	require.NoError(t, err)
	require.Equal(t, []string{goldID}, attached, "no second tag attached")
}
