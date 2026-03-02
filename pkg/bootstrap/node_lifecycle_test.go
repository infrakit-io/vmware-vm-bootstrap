package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/simulator"
)

func simNodeConfig(t *testing.T, vmName string) *VMConfig {
	t.Helper()
	pass, _ := simulator.DefaultLogin.Password()
	return &VMConfig{
		VCenterHost:     "",
		VCenterUsername: simulator.DefaultLogin.Username(),
		VCenterPassword: pass,
		Name:            vmName,
		Datacenter:      "DC0",
		VCenterInsecure: true,
	}
}

func firstSimVMName(t *testing.T, env *simBootstrapEnv) string {
	t.Helper()
	dc, err := env.finder.DefaultDatacenter(env.ctx)
	require.NoError(t, err)
	env.finder.SetDatacenter(dc)
	vms, err := env.finder.VirtualMachineList(env.ctx, "*")
	require.NoError(t, err)
	require.NotEmpty(t, vms)
	name, err := vms[0].ObjectName(env.ctx)
	require.NoError(t, err)
	return name
}

func TestNodeExists(t *testing.T) {
	env, cleanup := newSimBootstrapEnv(t)
	defer cleanup()

	vmName := firstSimVMName(t, env)
	cfg := simNodeConfig(t, vmName)
	cfg.VCenterHost = env.url.String()

	ok, err := NodeExists(env.ctx, cfg)
	require.NoError(t, err)
	require.True(t, ok)

	cfg.Name = "does-not-exist"
	ok, err = NodeExists(env.ctx, cfg)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestDeleteNode_IdempotentAndExisting(t *testing.T) {
	env, cleanup := newSimBootstrapEnv(t)
	defer cleanup()

	vmName := firstSimVMName(t, env)
	cfg := simNodeConfig(t, vmName)
	cfg.VCenterHost = env.url.String()

	require.NoError(t, DeleteNode(env.ctx, cfg))
	exists, err := NodeExists(env.ctx, cfg)
	require.NoError(t, err)
	require.False(t, exists)

	require.NoError(t, DeleteNode(env.ctx, cfg))
}

func TestRecreateNode_ReturnsDeleteErrorWhenVCenterUnavailable(t *testing.T) {
	cfg := simNodeConfig(t, "node-1")
	cfg.VCenterHost = "https://127.0.0.1:1/sdk"

	_, err := RecreateNode(context.Background(), cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "vCenter connection failed")
}

func TestRecreateNodeWithLogger_ReturnsDeleteErrorWhenVCenterUnavailable(t *testing.T) {
	cfg := simNodeConfig(t, "node-1")
	cfg.VCenterHost = "https://127.0.0.1:1/sdk"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := RecreateNodeWithLogger(context.Background(), cfg, logger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "vCenter connection failed")
}

func TestRecreateNodeWithLogger_DeleteThenCreatePath(t *testing.T) {
	env, cleanup := newSimBootstrapEnv(t)
	defer cleanup()

	cfg := simNodeConfig(t, "does-not-exist")
	cfg.VCenterHost = env.url.String()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := RecreateNodeWithLogger(context.Background(), cfg, logger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid config")
}

func TestCreateNodeAndCreateNodeWithLogger_ValidateConfig(t *testing.T) {
	_, err := CreateNode(context.Background(), &VMConfig{})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "invalid config") || strings.Contains(err.Error(), "is required"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err = CreateNodeWithLogger(context.Background(), &VMConfig{}, logger)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "invalid config") || strings.Contains(err.Error(), "is required"))
}
