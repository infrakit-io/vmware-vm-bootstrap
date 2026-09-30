# Ubuntu Support

This project uses Ubuntu Server **autoinstall** (Subiquity + cloud-init).

Supported in this project:

- **26.04 LTS** (Resolute Raccoon, 26.04.1 ISO) — generated autoinstall validated against the
  Subiquity 26.04 autoinstall schema, cloud-init 26.1 schema, netplan and sudo-rs; the ISO boot
  layout (GRUB, El Torito images) matches 24.04. Not yet run end-to-end on vCenter.
- **24.04 LTS** (tested)
- **22.04 LTS** (tested)
- **20.04 LTS** (best-effort; not continuously tested)

Older releases (18.04 and earlier) use older installers and are **not supported** by this project.

## ISO integrity

Every release in `configs/ubuntu-releases.yaml` carries the SHA256 of its live-server ISO, taken
from the release's `SHA256SUMS` after verifying `SHA256SUMS.gpg` against the Ubuntu CD Image
Automatic Signing Key (2012), fingerprint `843938DF228D22F7B3742BC0D94AA3F0EFE21092`.

The checksum is mandatory:

- a release with an empty or malformed checksum is refused before anything is downloaded;
- a cached ISO that does not match is discarded and downloaded again;
- a downloaded ISO that does not match is deleted and the run fails.

There is no opt-out. To add a point release, verify the signed `SHA256SUMS` the same way and
change `url` and `checksum` together.

## Ubuntu 26.04 notes

- `sudo` is provided by `sudo-rs`; the `NOPASSWD` rule cloud-init writes to
  `/etc/sudoers.d/90-cloud-init-users` works unchanged.
- Subiquity 26.04 creates the `identity` user during installation, so on first boot cloud-init
  finds the user already present: SSH keys, the sudo rule and password locking are still applied,
  but the `user_groups` default is not (the user keeps Subiquity's default groups, which include
  `sudo`).
- NIC naming: systemd 257+ reads the PCI slot from `firmware_node/sun`. The default `ens192`
  name has not been re-verified on a 26.04 guest; if the installer's network never comes up,
  check the guest's interface name first.
