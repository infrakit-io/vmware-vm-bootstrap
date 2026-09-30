# Release Notes

## Unreleased

Highlights:
- TBD

Notes:
- TBD

## v0.3.4 (2026-09-30)

Highlights:
- Ubuntu 26.04 LTS (Resolute Raccoon) support: `"26.04"` → `ubuntu-26.04.1-live-server-amd64.iso`, labelled in the config wizard.
- ISO checksums are now mandatory and enforced. Every release in `configs/ubuntu-releases.yaml` carries the SHA256 from its GPG-verified `SHA256SUMS` (signing key fingerprint `843938DF228D22F7B3742BC0D94AA3F0EFE21092`). A release with an empty or malformed checksum is refused before download, a cached ISO that does not match is discarded and re-downloaded, and a downloaded ISO that does not match is deleted and the run fails.
- Ubuntu 24.04 moves to the 24.04.5 point release.

Notes:
- Breaking for anyone who relied on an empty `checksum` to skip verification: there is no opt-out; add the checksum instead.
- The generated autoinstall was validated against the Subiquity 26.04 autoinstall schema, cloud-init 26.1, netplan and sudo-rs on Ubuntu 26.04; the 26.04.1 ISO boot layout matches 24.04 (GRUB rewrite pinned by a fixture test). Not yet run end-to-end on vCenter — see `docs/UBUNTU_SUPPORT.md`.
- Also first release to include the changes merged since v0.3.3: VM hardware (CPU/memory/OS disk/data disk) applied after Talos OVA deployment, `CONFIG_REPO_ROOT` honoured by `checkRequiredFiles`, Go toolchain 1.26.5 and `golang.org/x/crypto` v0.52.0 (GO-2026-5018), wizard selector refactor and go-task migration. The Talos OVA tests were updated for the hardware reconfiguration step (CI had been red on master since that change).

## v0.2.3 (2026-03-01)

Highlights:
- VM config editor now offers renaming `configs/vm.*.sops.yaml` to match updated `vm.name`.

Notes:
- Prevents menu confusion where filename stayed on old VM name after edit.

## v0.2.2 (2026-03-01)

Highlights:
- Bumped `cli-wizard-core` dependency from `v0.1.1` to `v0.1.2`.

Notes:
- Pulls badge/docs patch release from shared wizard core.

## v0.2.1 (2026-03-01)

Highlights:
- Bumped `cli-wizard-core` dependency from `v0.1.0` to `v0.1.1`.

Notes:
- Includes latest shared wizard core docs/packaging release without behavioral breaking changes.

## v0.2.0 (2026-03-01)

Highlights:
- Generalized VM bootstrap around OS profiles (Ubuntu and Talos) with a reusable provisioning architecture.
- Added Talos-focused node lifecycle flows (`node-create`, `node-delete`, `node-recreate`) and Talos plan/config generation from interactive CLI.
- Unified enterprise wizard UX across flows: resume drafts, safer defaults, consistent selectors, and standardized prompts.

Notes:
- `make config` now includes Talos schematic and node generation entrypoints aligned with the same menu system.
- Talos release/extension catalogs and defaults are externalized in config files (`configs/talos-releases.yaml`, `configs/talos-extensions.yaml`).
- Added release checklist documentation in `docs/RELEASE_CHECKLIST.md` for standardized publication flow.

## v0.1.13 (2026-02-26)

Highlights:
- Installation progress now includes adaptive ETA based on local run history.

Notes:
- Stores per-profile install durations in `tmp/install-stats.json` and reports `Installation ETA`.
- Keeps `remaining_timeout` visible as the hard upper bound.

## v0.1.12 (2026-02-26)

Highlights:
- SSH host fingerprint is now computed using `ssh-ed25519` only for deterministic host-key pinning.
- Installation progress now prints immediately at Phase 1 start and then every 10 seconds.

Notes:
- Reduces false fingerprint drift between Stage 1 and downstream strict SSH verification.

## v0.1.11 (2026-02-26)

Highlights:
- Added periodic installation heartbeat during long autoinstall waits.

Notes:
- `waitForInstallation` now logs elapsed/remaining time and current phase to avoid "stuck" perception.

## v0.1.10 (2026-02-26)

Highlights:
- Destructive confirmations now default to No (`[y/N]`) and are highlighted in red.

Notes:
- Applied to overwrite/delete/cleanup prompts in `config`, `create`, and `smoke` flows.

## v0.1.9 (2026-02-26)

Highlights:
- Fixes VM selector lint regression introduced in v0.1.8.

Notes:
- No functional changes beyond lint cleanup.

## v0.1.8 (2026-02-26)

Highlights:
- VM bootstrap menu now uses the same interactive selector style as config manager.

Notes:
- Fixes selector UI alignment issues in some terminals.

## v0.1.7 (2026-02-26)

Highlights:
- Autoinstall no-swap now works when `swap_size_gb=0` (swap partition omitted).
- VM selection supports a clean Exit option in the bootstrap menu.

Notes:
- Ctrl+C in the VM selector exits without triggering a bootstrap prompt.

## v0.1.6 (2026-02-26)

Highlights:
- Bootstrap result terminology (no legacy naming) across CLI/docs/config.
- Default bootstrap result output is enabled and configurable via `output.*`.

Notes:
- New flag `--bootstrap-result` replaces the previous output flag name.
- Default output path: `tmp/bootstrap-result.{vm}.yaml` (can be disabled).

## v0.1.5 (2026-02-26)

Highlights:
- Bootstrap result export includes SSH host fingerprint for downstream automation.
- Optional CLI flag writes a normalized bootstrap contract.

Notes:
- Bootstrap result enables strict host key verification in downstream tools (no prompt required).

## v0.1.4 (2026-02-25)

Highlights:
- Configurable guest NIC name (no longer hard-coded `ens192`).
- Smoke test improvements (reuse/recreate, SSH key handling, SSH port support, better feedback).
- ISO autoinstall cache invalidation via metadata.
- Added smoke test doc and automated release notes flow.

Notes:
- Ubuntu 20.04 autoinstall now patches ISOLINUX `append` lines.
- Release notes generated from `docs/RELEASES.md` via `scripts/release-notes.sh`.
- `--debug` writes to `tmp/vmbootstrap-debug.log`.

## v0.1.3 (2026-02-25)

Highlights:
- Configurable guest NIC name (no longer hard-coded `ens192`).
- Smoke test improvements (reuse/recreate, SSH key handling, SSH port support, better feedback).
- ISO autoinstall cache invalidation via metadata.
- Added smoke test doc and automated release notes flow.

Notes:
- Ubuntu 20.04 autoinstall now patches ISOLINUX `append` lines.
- Release notes generated from `docs/RELEASES.md` via `scripts/release-notes.sh`.
- `--debug` writes to `tmp/vmbootstrap-debug.log`.

## v0.1.1 (2026-02-24)

Highlights:
- First public alpha release.
- CI pipeline with tests, linting, and vuln checks.
- Coverage badge generation.
- Expanded simulator-based test coverage.
- VM post-creation operations: verify, power on/off, delete.
- Release workflow now attaches prebuilt binaries.
- Example configs for vCenter/VM.

Notes:
- NoCloud ISO creation uses a pure Go ISO9660 writer.
- `make test-cover` excludes `cmd/` packages to avoid toolchain issues.
- `vcenter.NewClient` accepts full `https://.../sdk` URLs (https only).
- `VM.Verify` requires VMware Tools running + SSH access.

## v0.1.0 (2026-02-24)

Highlights:
- First public alpha release.
- CI pipeline with tests, linting, and vuln checks.
- Coverage badge generation.
- Expanded simulator-based test coverage.
- VM post-creation operations: verify, power on/off, delete.
- Release workflow now attaches prebuilt binaries.
- Example configs for vCenter/VM.

Notes:
- NoCloud ISO creation uses a pure Go ISO9660 writer.
- `make test-cover` excludes `cmd/` packages to avoid toolchain issues.
- `vcenter.NewClient` accepts full `https://.../sdk` URLs (https only).
- `VM.Verify` requires VMware Tools running + SSH access.
