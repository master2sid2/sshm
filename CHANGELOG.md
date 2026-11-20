# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/) (while the major version is 0, minor
releases may include behaviour changes — see each release's *Changed* and
*Upgrade notes*).

Longer, narrative release notes live in [docs/releases/](docs/releases/).

## [Unreleased]

## [0.2.0] - 2026-11-20

### Added

- **Host key verification** against `~/.ssh/known_hosts` (shared with OpenSSH): a new host asks for confirmation of its fingerprint and is remembered; a changed or revoked key is refused with a full warning. Only the key types on record are negotiated, so a server with several keys is not mistaken for a changed one.
- **`~/.ssh/config` import** (`sshm import-ssh`, offered on the first run): hosts from `~/.ssh/config` and its includes, resolved like `ssh` does, one folder per source file; duplicates are skipped; no passwords involved.
- **`~/.ssh/config` export** (`sshm export-ssh`): writes `~/.ssh/config.d/sshm.conf` without any password or passphrase, and offers to add `Include config.d/sshm.conf` to `~/.ssh/config` (with a backup).
- **Session unlock cache**: the master password is asked once per login session on Linux (kernel session keyring); `sshm lock` forgets it.
- **`sshm passwd`** changes the master password in place.
- **Search across subfolders** (`/`) by name, host, user or folder path; locked folders are searched once unlocked.
- **Sorting** (`s` cycles name / host / updated, `S` reverses).
- **`ctrl+r`** reveals the selected host's password or key passphrase.
- Missing key passphrases and passwords are asked when connecting; keyboard-interactive authentication (with OTP questions) for password hosts.
- Keepalive every 30 s; a dead connection ends the session.
- `make` targets, version baked in with `-ldflags`, GitHub CI, GoReleaser releases for Linux and macOS.

### Changed

- **The first run asks for the master password twice**, refuses an empty one, and writes the vault right away.
- Changing or removing a folder password requires the current one; renaming or moving a locked folder asks for it unless unlocked in this session.
- After a session you return to the same folder, cursor and search; folders unlocked earlier stay unlocked.
- Header status messages disappear after a few seconds; the version stays visible.
- Module path is `github.com/master2sid2/sshm`, so `go install` works.
- `ctrl+c` no longer quits sshm while connecting: it cancels the attempt.

### Fixed

- **Folder passwords could be bypassed**: `p` removed any folder's password without asking for it, and cancelling a delete prompt let the next unlock of *another* folder open the delete confirmation for the first one.
- **Deleting a folder could leave subfolders and their hosts behind**, invisible but still in the vault, when a subfolder was stored before its parent (after a move).
- **The vault could end up readable by other users**: it was written through a fixed `vault.enc.tmp` with `os.WriteFile`, which keeps the mode of a leftover file. Writes now use a random temporary file with mode `0600`, synced before the rename.
- A damaged vault (bad nonce length) made sshm panic.
- A failed save left the interface showing changes that were not on disk.
- An unreachable host blocked for about two minutes; connect and handshake now time out after 15 s.
- IPv6 hosts and tunnel addresses did not work.
- A tunnel that could not start (port in use) failed silently.
- A remote shell exiting with a non-zero status was reported as "Connection failed".
- The first key pressed after leaving an SSH session was lost: it went to the closed session.
- `ctrl+c` at the master password prompt was ignored; at other prompts it could leave the terminal without echo.
- Modals were off-center when the footer was wider than the terminal; long folder names broke modal borders; the footer wrapped on narrow terminals.

### Security

- **sshm used to accept any host key**, so on a hostile network a man-in-the-middle could collect the password of every password host. Host keys are now verified (see *Added*).
- Updated `golang.org/x/crypto` (0.24 → 0.59) and the other `golang.org/x` modules, `bubbletea` (1.3.10) and `bubbles` (1.0.0).

### Upgrade notes

- Vaults from 0.1.x open unchanged; the format is the same.
- Hosts imported with a key path but no passphrase ask for the passphrase when connecting.
- **The first connection to each host asks you to confirm its fingerprint** unless you already connected to it with `ssh` (the same `known_hosts` is used).

## [0.1.2]

### Fixed

- `~` is expanded in key paths; the default key is suggested for key auth.
- Navigation state survives SSH sessions; the list keeps its cursor after a refresh.
- Arrow keys in the host form.

## [0.1.1]

### Fixed

- Filter: substring matching instead of fuzzy matching; `esc` clears an applied filter.

## [0.1.0]

First release: encrypted vault, nested folders with optional passwords, move,
host form with tunnels, pure-Go SSH client with PTY and resize.

[Unreleased]: https://github.com/master2sid2/sshm/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/master2sid2/sshm/releases/tag/v0.2.0
