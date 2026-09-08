# install test fixture

This directory holds only plain text. CLAUDE.md forbids committing a binary
or an archive, so the release archives that `scripts/install.sh` and
`scripts/install.ps1` download are never checked in here.

`fake-shellforge` is a tiny POSIX sh script standing in for the real
`shellforge` binary. It answers `version` with a fixed string and `doctor`
with an exit code (0 by default, overridable through the
`FAKE_SHELLFORGE_DOCTOR_EXIT` environment variable), which is all the
installer tests need to check.

`scripts/tests/test_install_sh.py`, and the pwsh step appended to
`ci.yml`'s `test` job, both build the archives at run time: they pack this
script into `shellforge_v0.1.0-test_linux_amd64.tar.gz`,
`shellforge_v0.1.0-test_linux_arm64.tar.gz`, and
`shellforge_v0.1.0-test_windows_amd64.zip`, compute a real `SHA256SUMS`
next to them, and also write a `SHA256SUMS.wrong` copy with one digest
flipped for the mismatch case. Building the archives at test time means the
fixture cannot go stale against the real release layout, and it means the
checksum mismatch case exercises a digest that is genuinely wrong rather
than one that happens to be wrong.
