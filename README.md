# vmware-vm-bootstrap

![CI](https://github.com/infrakit-io/vmware-vm-bootstrap/actions/workflows/ci.yml/badge.svg)
![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/infrakit-io/vmware-vm-bootstrap/master/docs/coverage/coverage.json)
[![Go Report Card](https://goreportcard.com/badge/github.com/infrakit-io/vmware-vm-bootstrap)](https://goreportcard.com/report/github.com/infrakit-io/vmware-vm-bootstrap)
![Go Version](https://img.shields.io/github/go-mod/go-version/infrakit-io/vmware-vm-bootstrap)
![Release](https://img.shields.io/github/v/release/infrakit-io/vmware-vm-bootstrap)

**Go library for automated VM lifecycle in VMware vSphere (profile-driven: Ubuntu/Talos).**

## Features

- Core library in Go (ISO tooling still uses system binaries)
- VMware vSphere 7.0+ support
- OS profile model: Ubuntu and Talos
- Node lifecycle operations: create/delete/recreate/update
- Cloud-init configuration (network, users, SSH keys)
- Password hashing (bcrypt; SHA-512 planned)
- Context-aware operations (timeout/cancel support)
- Comprehensive error handling

## Installation

```bash
go get github.com/infrakit-io/vmware-vm-bootstrap
```

## Quick Start (Library)

```go
package main

import (
    "context"
    "log"
    "github.com/infrakit-io/vmware-vm-bootstrap/pkg/bootstrap"
)

func main() {
    dataDiskSize := 500 // optional data disk (GB)
    swapSize := 4       // optional swap (GB)

    cfg := &bootstrap.VMConfig{
        // Required vCenter
        VCenterHost:     "vcenter.example.com",
        VCenterUsername: "administrator@vsphere.local",
        VCenterPassword: "secret",

        // Optional vCenter (defaults from configs/defaults.yaml)
        VCenterPort:     443,
        VCenterInsecure: false,

        // Required VM specs
        Name:            "web-server-01",
        CPUs:            4,
        MemoryMB:        8192,
        DiskSizeGB:      40,

        // Optional data disk (set both or leave both empty)
        DataDiskSizeGB:    &dataDiskSize,
        DataDiskMountPath: "/data",

        // Required network
        NetworkName:     "LAN_Management",
        NetworkInterface: "ens192",
        IPAddress:       "192.168.1.10",
        Netmask:         "255.255.255.0",
        Gateway:         "192.168.1.1",
        DNS:             []string{"8.8.8.8"},

        // Required placement
        Datacenter:      "DC1",
        Folder:          "Production",
        Datastore:       "SSD-Storage-01",

        // Optional placement
        ResourcePool:    "WebTier",
        ISODatastore:    "ISO-Storage-01",

        // Required OS/user
        Profile: "ubuntu",
        Profiles: bootstrap.VMProfiles{
            Ubuntu: bootstrap.UbuntuProfile{Version: "24.04"},
        },
        Username:      "sysadmin",
        SSHPublicKeys: []string{"ssh-ed25519 AAAA..."},

        // Optional auth (use keys OR password OR password hash)
        Password:        "",
        PasswordHash:    "",
        AllowPasswordSSH: false,

        // Optional OS tweaks (defaults in configs/defaults.yaml)
        Timezone:        "UTC",
        Locale:          "en_US.UTF-8",
        SwapSizeGB:      &swapSize,
        Firmware:        "bios",
    }

    vm, err := bootstrap.Bootstrap(context.Background(), cfg)
    if err != nil {
        log.Fatalf("Bootstrap failed: %v", err)
    }

    log.Printf("VM %s ready at %s", vm.Name, vm.IPAddress)
}
```

Post-creation operations:

```go
if err := vm.Verify(context.Background()); err != nil {
    log.Fatalf("Verify failed: %v", err)
}
// vm.PowerOff(...), vm.PowerOn(...), vm.Delete(...)
```

Note: `Verify` requires VMware Tools running and SSH access. `VCenterHost` accepts a hostname or a full `https://.../sdk` URL (https only).

Security note:

- SSH password authentication is **disabled by default**. To allow it, set `AllowPasswordSSH=true` and provide `Password` or `PasswordHash`.

## Bootstrap Result (for downstream automation)

If you want to feed the bootstrap output into another tool (e.g., `github.com/infrakit-io/talos-docker-bootstrap`), you can save a normalized result file:

```bash
vmbootstrap run --bootstrap-result tmp/bootstrap-result.yaml
```

By default, the CLI writes a bootstrap result to `tmp/bootstrap-result.{vm}.yaml` (see `configs/defaults.yaml`).
You can disable it by setting `output.enable=false`.

The result includes the VM IP, SSH user/key path, port, and the SSH host fingerprint. This enables strict host key verification in downstream automation without manual prompts.
- Prefer SSH keys over passwords. Passwords exist in plaintext at runtime (even if stored encrypted at rest).

Auth options (choose one):

- SSH keys:
```go
SSHPublicKeys: []string{"ssh-ed25519 AAAA..."},
```

- Password (plaintext; will be bcrypt-hashed):
```go
Password: "strong-password",
AllowPasswordSSH: true,
```

- Pre-hashed bcrypt password:
```go
PasswordHash: "$2a$10$...",
AllowPasswordSSH: true,
```

Note: Using `Password` means the plaintext password exists in memory (and possibly in code/config). For better security, prefer `PasswordHash`, and load secrets from a secure source (SOPS via the CLI, a secrets manager, or environment variables) before building the config.

Disable optional fields:

```go
DataDiskSizeGB: nil,
DataDiskMountPath: "",
SwapSizeGB: nil,
```

Default values (from `configs/defaults.yaml`):

- vCenter port: `443`
- Firmware: `bios`
- Network interface: `ens192`
- Locale: `en_US.UTF-8`
- Timezone: `UTC`
- Swap size: `2` GB
- Packages: `open-vm-tools`
- User groups: `sudo,adm,dialout,cdrom,audio,video,plugdev,users`
- User shell: `/bin/bash`
- Timeouts: see `configs/defaults.yaml`
- ISO defaults: see `configs/defaults.yaml`

## CLI Tool

```bash
# Install CLI
go install github.com/infrakit-io/vmware-vm-bootstrap/cmd/vmbootstrap@latest

# Bootstrap from a selected config
vmbootstrap run

# Node lifecycle
vmbootstrap node create --config configs/vm.node01.sops.yaml
vmbootstrap node delete --config configs/vm.node01.sops.yaml
vmbootstrap node recreate --config configs/vm.node01.sops.yaml
vmbootstrap node update --config configs/vm.node01.sops.yaml --to-version v1.12.0
```

Note: The library API consumes an in-memory `bootstrap.VMConfig` and has no SOPS dependency. SOPS is used only by the CLI for encrypted config files.

## Config Files

- `configs/vcenter.sops.yaml`: vCenter connection + default placement settings + Talos content library (`content_library`/`content_library_id`).
- `configs/vm.*.sops.yaml`: per-VM runtime config (profile, compute, network, auth).
- `configs/vm.example.yaml`: template for new VM config files.
- `configs/talos.schematics.sops.yaml`: Talos Image Factory schematic catalog used by Talos profile flow.
- `configs/defaults.yaml`: repo defaults for wizard prompts and runtime behavior.
- `.sops.yaml` and `.sopsrc`: local SOPS/AGE setup for encryption.

## Requirements

Library:
- Go 1.26+
- VMware vCenter 7.0+
- Ubuntu 22.04 or 24.04 Server ISO

CLI (in addition to library requirements):
- `govc` (vSphere CLI)
- `genisoimage`
- `xorriso`
- `sops` (only for encrypted config files)

## Development

Common targets:

```bash
# Install external tools (golangci-lint, govulncheck, govc, sops, genisoimage, xorriso)
make install-requirements

# Build & verify
make build
make build-cli
make test
make lint
make vulncheck
make test-cover

# Maintenance
make fmt
make vet
make deps
make verify
make clean
```

VM management (CLI):

```bash
make config   # interactive config wizard
make vm-deploy  # bootstrap a VM
make smoke VM=configs/vm.myvm.sops.yaml  # bootstrap + smoke test (+ cleanup)
```

## Releases

Release automation (safe mode):

- Pushing a tag `v*` creates a **Draft Release** with binaries and checksums.
- Manually running the "Release" workflow **publishes** a release for an existing tag.

Example:

```bash
git tag v0.1.0
git push origin v0.1.0
```

## Documentation

See [pkg.go.dev](https://pkg.go.dev/github.com/infrakit-io/vmware-vm-bootstrap) for full API documentation.

Ubuntu support matrix: see [docs/UBUNTU_SUPPORT.md](docs/UBUNTU_SUPPORT.md).

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## Versioning

Semantic Versioning is documented in [docs/VERSIONING.md](docs/VERSIONING.md).

## License

MIT - see [LICENSE](LICENSE) file.

## Status

**v0.1.0 Alpha** - Core library functional (VM creation, cloud-init, ISO, SSH verification). API may change before v1.0.0.
