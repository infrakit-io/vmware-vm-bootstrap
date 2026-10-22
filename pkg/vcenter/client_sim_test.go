package vcenter

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
)

func newSimClient(t *testing.T) (*Client, context.Context, func()) {
	model := simulator.VPX()
	model.Datacenter = 1
	model.Cluster = 1
	model.Host = 1
	model.Pool = 1
	model.Machine = 1

	require.NoError(t, model.Create())
	model.Service.TLS = new(tls.Config)
	model.Service.RegisterEndpoints = true // vapi/simulator (tagging REST)
	s := model.Service.NewServer()

	ctx := context.Background()
	u := s.URL
	u.User = simulator.DefaultLogin

	client, err := NewClient(ctx, &Config{
		Host:     u.String(),
		Username: simulator.DefaultLogin.Username(),
		Password: func() string { p, _ := simulator.DefaultLogin.Password(); return p }(),
		Insecure: true,
	})
	require.NoError(t, err)

	cleanup := func() {
		_ = client.Disconnect()
		s.Close()
		model.Remove()
	}

	return client, ctx, cleanup
}

func getDatacenterName(t *testing.T, client *Client) string {
	t.Helper()

	dcs, err := client.finder.DatacenterList(client.ctx, "*")
	require.NoError(t, err)
	require.NotEmpty(t, dcs)
	return dcs[0].Name()
}

func TestNewClient_WithURLScheme(t *testing.T) {
	client, _, cleanup := newSimClient(t)
	defer cleanup()

	require.NotNil(t, client)
}

func TestNewClient_HTTPHostFails(t *testing.T) {
	_, err := NewClient(context.Background(), &Config{
		Host:     "http://example.com/sdk",
		Username: "user",
		Password: "pass",
		Insecure: true,
	})
	require.Error(t, err)
}

func TestNewClient_InvalidURL(t *testing.T) {
	_, err := NewClient(context.Background(), &Config{
		Host:     "http://bad::url",
		Username: "user",
		Password: "pass",
		Insecure: true,
	})
	require.Error(t, err)
}

func TestClient_DisconnectNil(t *testing.T) {
	c := &Client{}
	require.NoError(t, c.Disconnect())
}

func TestClient_ListAndFind(t *testing.T) {
	client, ctx, cleanup := newSimClient(t)
	defer cleanup()

	dcName := getDatacenterName(t, client)

	// Ensure a VM folder exists for list/find tests.
	if vmFolder, err := client.finder.DefaultFolder(ctx); err == nil {
		_, _ = vmFolder.CreateFolder(ctx, "TestFolder")
	}

	datastores, err := client.ListDatastores(dcName)
	require.NoError(t, err)
	require.NotEmpty(t, datastores)
	_, err = client.FindDatastore(dcName, datastores[0].Name)
	require.NoError(t, err)

	networks, err := client.ListNetworks(dcName)
	require.NoError(t, err)
	require.NotEmpty(t, networks)
	_, err = client.FindNetwork(dcName, networks[0].Name)
	require.NoError(t, err)

	_, err = client.ListFolders(dcName)
	require.NoError(t, err)
	_, err = client.FindFolder(dcName, "/"+dcName+"/vm")
	require.NoError(t, err)

	pools, err := client.ListResourcePools(dcName)
	require.NoError(t, err)
	require.NotEmpty(t, pools)
	_, err = client.FindResourcePool(dcName, pools[0].Name)
	require.NoError(t, err)

	// VM lookup
	vms, err := client.finder.VirtualMachineList(ctx, "*")
	require.NoError(t, err)
	require.NotEmpty(t, vms)
	_, err = client.FindVM(dcName, vms[0].Name())
	require.NoError(t, err)

	missing, err := client.FindVM(dcName, "does-not-exist")
	require.NoError(t, err)
	require.Nil(t, missing)

	// Finder should be set and usable.
	require.IsType(t, &find.Finder{}, client.finder)

	require.NotNil(t, client.Client())
	require.NotNil(t, client.SOAPClient())

	// Coverage for storage type inference.
	require.Equal(t, "SSD", inferStorageType("fast-ssd-datastore"))
	require.Equal(t, "HDD", inferStorageType("slow-hdd"))
}

func TestClient_FindErrors(t *testing.T) {
	client, _, cleanup := newSimClient(t)
	defer cleanup()

	dcName := getDatacenterName(t, client)

	_, err := client.FindDatacenter("does-not-exist")
	require.Error(t, err)

	_, err = client.FindDatastore(dcName, "missing-datastore")
	require.Error(t, err)

	_, err = client.FindNetwork(dcName, "missing-network")
	require.Error(t, err)

	_, err = client.FindFolder(dcName, "missing-folder")
	require.Error(t, err)

	_, err = client.FindResourcePool(dcName, "missing-pool")
	require.Error(t, err)
}
