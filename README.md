# sshm

[![CI](https://github.com/master2sid2/sshm/actions/workflows/ci.yml/badge.svg)](https://github.com/master2sid2/sshm/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/master2sid2/sshm)](https://github.com/master2sid2/sshm/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/master2sid2/sshm)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**An encrypted SSH profile manager with a terminal UI.**

`sshm` keeps your SSH hosts — address, user, password or key, port forwards —
in one encrypted vault, organised in folders (optionally password-protected).
Pick a host and press `enter`: the connection is made by a pure-Go SSH client,
so no `ssh` process is spawned and no credential shows up in `ps` or audit
logs. When the session ends you are back in the list, exactly where you were.

![sshm: first run importing ~/.ssh/config, connecting to a new host, back in the list, search](docs/demo/sshm.gif)

*Recorded with the made-up hosts of [docs/demo/ssh/](docs/demo/ssh/) and a throwaway SSH server.*

## Features

- **Encrypted vault** — AES-256-GCM, key derived with Argon2id; written atomically with mode `0600`.
- **Folders** — nested to any depth; a folder can have its own password.
- **Search across folders** — `/` finds hosts by name, host, user or folder path in the current folder and everything below it.
- **Auth types** — ssh-agent, key file (passphrase stored or asked), password (also keyboard-interactive, with OTP prompts).
- **Port forwarding** — local (`-L`) and remote (`-R`) tunnels per host, started with the session.
- **`~/.ssh/config` import and export** — bring your existing hosts in (the first run offers it), or write the vault's hosts out for plain `ssh`. Passwords never leave the vault.
- **Master password once per login session** — the derived key is cached in the Linux kernel keyring; `sshm lock` forgets it.
- **Pure Go** — no `ssh` binary needed; PTY, resize, keepalive and tunnels handled in-process.

## Install

```bash
go install github.com/master2sid2/sshm@latest
```

Or download a binary for Linux or macOS (amd64, arm64) from the
[releases](https://github.com/master2sid2/sshm/releases) page and check it
against `SHA256SUMS`. Or build from source (Go 1.26.1+):

```bash
git clone https://github.com/master2sid2/sshm.git
cd sshm
make build      # ./sshm, with the version baked in
make install    # copies it to ~/.local/bin
sshm --version
```

`make dist` builds the release binaries into `dist/`.

## Quick start

```bash
sshm
```

1. The first run creates the vault at `~/.config/sshm/vault.enc` and asks for a **master password** twice.
2. If `~/.ssh/config` has hosts, sshm offers to import them (no passwords; key paths are kept).
3. Press `g` to create a folder, `enter` to open it, `n` to add a host, `ctrl+s` to save.
4. `enter` on a host connects. Leave the remote shell and you are back in the list.

Press `?` at any time for the key reference.

## Keys

### Main list

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate |
| `enter` | Open folder / connect to host |
| `esc` | Clear the search, or go up one folder |
| `/` | Search here and in subfolders (`enter` connects, `esc` clears) |
| `s` / `S` | Cycle sort (name, host, updated) / reverse it |
| `ctrl+r` | Reveal / hide the selected host's password or passphrase |
| `n` | New host in the current folder |
| `g` | New folder in the current folder |
| `e` | Edit host / rename folder |
| `m` | Move host or folder anywhere in the tree |
| `d` | Delete host or folder (with its contents) |
| `p` | Set, change or remove a folder password |
| `?` | Help |
| `q` | Quit |

### Host form

| Key | Action |
|-----|--------|
| `tab` / `↑` / `↓` | Next / previous field |
| `←` / `→` | Change the auth type |
| `enter` (tunnel list) | Edit the tunnel, or add one on `[ + Add tunnel ]` |
| `d` (tunnel list) | Remove the tunnel |
| `ctrl+s` | Save |
| `esc` | Cancel |

In the tunnel editor, `←` / `→` switch between `local` and `remote`, and
`ctrl+s` confirms.

## Folders and folder passwords

Press `p` on a folder to protect it with a password (separate from the master
password, stored as a bcrypt hash inside the vault):

- Opening, renaming or moving it asks for the password once per run; it then shows 🔓.
- **Deleting it or changing its password always asks again**, even when it is unlocked.
- Search skips locked folders until they are unlocked; `export-ssh` leaves them out unless `-locked`.
- Leave both fields blank in the password form to remove the password.

A folder password hides a folder in the interface; it is not a second layer
of encryption. Everything is protected by the master password.

## Connecting

- **Host keys are verified against `~/.ssh/known_hosts`**, the file `ssh` uses (plus `/etc/ssh/ssh_known_hosts`). A new host shows its fingerprint and needs a typed `yes`; the key is then saved (hashed if your file uses hashed entries). A host whose key has **changed** is refused with a full warning — remove the old key with `ssh-keygen -R <host>` if the change is expected.
- Connect and handshake time out after 15 seconds; `ctrl+c` cancels a connection attempt and returns to the list.
- An encrypted key without a stored passphrase, or a password host without a password, asks on the terminal.
- Tunnels are bound before the shell starts; a port already in use is reported, and the session runs without that tunnel.
- A keepalive every 30 s ends a session whose network died instead of freezing it.

## `~/.ssh/config`

```bash
sshm import-ssh [-n] [-config path]     # -n: show what would be imported
sshm export-ssh [-y] [-locked] [-o path|-]
```

**Import** reads `~/.ssh/config` and every file it includes, and resolves each
host the way `ssh` does (first value wins, `Host *` defaults, patterns,
`%h`-style tokens). Each source file becomes a root folder (`ssh config`,
`config.d/work.conf` → `work`). Hosts already in the vault (same host, user
and port) are skipped, so importing again only adds what is new.
`IdentityFile` becomes key auth, `LocalForward` / `RemoteForward` become
tunnels. Options sshm cannot honour, such as `ProxyJump`, are reported.

**Export** writes `~/.ssh/config.d/sshm.conf`, a file sshm owns and overwrites
each time. Key hosts get `IdentityFile`; for password hosts `ssh` asks for the
password itself — **passwords and passphrases are never exported**. If
`~/.ssh/config` does not read the file yet, sshm offers to add
`Include config.d/sshm.conf` at its top (the previous file is kept as
`config.bak`); nothing else in your config is changed. Hosts your own config
already defines are left out.

## Other commands

| Command | |
|---|---|
| `sshm passwd` | Change the master password (always asks for the current one) |
| `sshm lock` | Forget the unlock cached for this login session |
| `sshm --version` | Print the version |

All of them accept `-vault <path>` to use another vault file.

## Vault

`~/.config/sshm/vault.enc`:

```
[magic "SSHM" 4B][version 4B][salt-len 4B][salt 32B][nonce-len 4B][nonce 12B][ciphertext + GCM tag]
```

Key derivation: Argon2id, 64 MiB, 2 passes, 4 threads. Writes go through a
randomly named temporary file with mode `0600`, synced before the rename.
See [SECURITY.md](SECURITY.md) for what is and is not protected.

## Development

```bash
make test    # go test ./...
make vet
make lint    # golangci-lint
make demo    # re-record docs/demo/sshm.gif (Docker)
```

Release notes are in [CHANGELOG.md](CHANGELOG.md) and [docs/releases/](docs/releases/).

## License

[MIT](LICENSE)
