A release now carries `install.sh` and `mash-local-nats-setup.sh`. `curl -fsSL https://github.com/bogworks/mash/releases/latest/download/install.sh | bash`
installs `mash` and `mashctl` from the same release into `~/.local/bin` (or the folder `MASH_INSTALL_DIR` names): it checks each
against the release's checksums, verifies the attestation too when `gh` is installed, asks nothing, needs no `sudo`, and
prints one `Installed ...` line for each with the path it used. Both scripts are listed in `SHA256SUMS` and attested like the binaries
([ADR 0150](docs/decisions/0150-releases-carry-install-and-local-nats-setup-scripts-as-attested-assets.md)).
