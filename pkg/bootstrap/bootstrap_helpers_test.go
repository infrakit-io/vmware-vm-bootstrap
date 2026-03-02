package bootstrap

import (
	"context"
	"testing"
)

func TestDefaultBootstrapperResolveProfile(t *testing.T) {
	b := defaultBootstrapper()

	p, err := b.resolveProfile("")
	if err != nil || p.Name() != "ubuntu" {
		t.Fatalf("expected default profile ubuntu, got profile=%v err=%v", p, err)
	}

	p, err = b.resolveProfile("talos")
	if err != nil || p.Name() != "talos" {
		t.Fatalf("expected talos profile, got profile=%v err=%v", p, err)
	}

	if _, err := b.resolveProfile("unknown"); err == nil {
		t.Fatal("expected unsupported profile error")
	}
}

func TestCurrentInstallPhase(t *testing.T) {
	if got := currentInstallPhase(false, false); got != "phase1-wait-tools-start" {
		t.Fatalf("unexpected phase: %s", got)
	}
	if got := currentInstallPhase(true, true); got != "phase3-wait-stable-hostname" {
		t.Fatalf("unexpected phase: %s", got)
	}
	if got := currentInstallPhase(false, true); got != "phase2-wait-reboot" {
		t.Fatalf("unexpected phase: %s", got)
	}
}

func TestDefaultBootstrapperFactories(t *testing.T) {
	b := defaultBootstrapper()

	if b.newVMCreator(context.Background()) == nil {
		t.Fatal("expected vm creator factory to return non-nil")
	}
	if b.newISOManager(context.Background()) == nil {
		t.Fatal("expected iso manager factory to return non-nil")
	}

	_, err := b.connectVCenter(context.Background(), &VMConfig{
		VCenterHost:     "https://127.0.0.1:1/sdk",
		VCenterUsername: "user",
		VCenterPassword: "pass",
		VCenterInsecure: true,
	})
	if err == nil {
		t.Fatal("expected connectVCenter to fail for unreachable endpoint")
	}
}
