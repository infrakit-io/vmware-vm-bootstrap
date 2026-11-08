# AlmaLinux Support

Profile `alma` installs AlmaLinux 9 unattended with an Anaconda **kickstart**.

- Supported: **9.8** (exact minor pinned in `configs/alma-releases.yaml`; a new minor means a new entry).
- BIOS firmware only (the boot-menu patch covers `isolinux`; UEFI is rejected by validation).
- Single NIC (`extra_nics` is rejected). Disk layout: `sda` = `/boot` + optional swap + `/`, `sdb` = data disk at `data_disk_mount_path`; ext4 (`configs/defaults.yaml: alma.filesystem`). `root_mount_options` / `data_disk_mount_options` become `part --fsoptions`.
- Login: `username: root` locks the root password and installs the SSH key for root; any other username creates that user (group `wheel`, NOPASSWD sudo) and locks root. A password (bcrypt hash) is set only if given (default: locked, SSH key only; under FIPS mode a bcrypt hash is rejected); `allow_password_ssh` toggles `PasswordAuthentication` (and `rootpw --allow-ssh` for root).
- Firmware is written into the VM spec explicitly as BIOS for this profile (rhel9_64Guest may default to EFI on vSphere 8, where the patched isolinux menu is never used).
- Disks are addressed as `sda` (OS) and `sdb` (data) in the kickstart. The kickstart reference only documents
  stable names as `/dev/disk/by-id` for `part --onpart`/`--ondisk` and `/dev/disk/by-path` globs for `clearpart`/`ignoredisk`
  (`kickstart-docs.txt`, "Special Notes for Referring to Disks"); no documented form covers `bootloader --boot-drive`
  too, by-id needs `disk.EnableUUID` (not set) and an exact by-path name embeds a vSphere-version dependent PCI address
  (the doc itself calls by-path "not 100% reliable"). Both disks sit on one pvscsi controller (units 0 and 1), probed in order.
- vSphere guest ID `rhel9_64Guest` (vSphere 7.0 U2+).

## How the kickstart is delivered

Two CD-ROMs: the AlmaLinux boot ISO (boot menu patched: plain "Install" is the default, short timeout)
and a small ISO labelled `OEMDRV` that holds `ks.cfg`. Anaconda auto-loads `/ks.cfg` from a volume
with that label (`docs/almalinux-official/anaconda-kickstart-genrules.sh.txt`,
`anaconda-fetch-kickstart-disk.txt`), so no kernel argument is needed and the pure-Go ISO writer
already used for the NoCloud seed is reused. The kickstart ends with `poweroff`; vmbootstrap waits for
power-off, detaches both CD-ROMs, deletes the kickstart ISO and powers the VM on (a `reboot` would
boot the installer again).

## ISO integrity

Same rules as Ubuntu: the SHA256 in `configs/alma-releases.yaml` is mandatory. It was taken from the
PGP-signed `CHECKSUM` (saved as `docs/almalinux-official/CHECKSUM-9.8.txt`), verified against the AlmaLinux 9 key,
fingerprint `BF18 AC28 7617 8908 D6E7 1267 D36C B86C B86B 3716`. The boot ISO only contains the
installer; packages come from the pinned BaseOS/AppStream trees (when a newer minor ships, the old
trees move to `vault.almalinux.org`).

Kickstart reference: `docs/almalinux-official/kickstart-docs.txt` (pykickstart). The RHEL 9 portal
(docs.redhat.com) answers 403 to automated fetches, so it could not be saved verbatim.
