# Security policy

## Supported versions

Only the latest release receives fixes.

## Reporting a vulnerability

Please **do not open a public issue** for a security problem. Report it
privately through GitHub's
[private vulnerability reporting](https://github.com/master2sid2/sshm/security/advisories/new),
with the version (`sshm --version`), your platform, and steps to reproduce.

## What sshm protects

- **The vault at rest.** Hosts, users, passwords and key passphrases are stored in `~/.config/sshm/vault.enc`, encrypted with AES-256-GCM under a key derived from the master password with Argon2id (64 MiB, 2 passes, 4 threads). Without the password, or the cached key, the file is unreadable and tampering is detected. It is written with mode `0600`.
- **Credentials in process lists.** The SSH connection is made in-process; no `ssh` binary is run, so nothing appears in `ps` or execve audit logs.
- **The server's identity.** Host keys are checked against `~/.ssh/known_hosts` (and `/etc/ssh/ssh_known_hosts`), like OpenSSH with `StrictHostKeyChecking ask`: a changed or revoked key is refused before any password is sent, a new host needs its fingerprint confirmed with `yes`.
- **Exports.** `sshm export-ssh` never writes passwords or key passphrases.

## What it does not protect against

- **The first connection to a new host.** Accepting a fingerprint you have not checked trusts whoever answered. Compare it with the server's (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the server) before typing `yes`.
- **Certificates** (`@cert-authority`) are not supported; such hosts are treated as unknown.
- **Folder passwords are not encryption.** They hide a folder in the interface (and from search and export); everything in the vault is encrypted with the master key alone. Anyone with the master password, or the cached key, can read every folder.
- **The cached key.** While an unlock is cached (Linux session keyring), any process in your login session can open the vault without the password. `sshm lock` or logging out drops it.
- **Root, or malware running as you.** Anyone who can read your process memory, your keyring or your files can get what you can.
- **Revealed secrets.** `ctrl+r` shows a password on screen; it can end up in scrollback, screenshots or screen sharing.
- **Memory.** Secrets are not zeroed after use and may be swapped to disk.
- **Weak master passwords.** None is enforced beyond "not empty".
- **Concurrent use.** There is no locking between two running instances (or an `import-ssh` while the TUI is open): the last save wins.
- **Loss of the master password.** It cannot be recovered.

## Good practice

1. Choose a strong, unique master password.
2. Keep `~/.config/sshm/` at `0700` and back up `vault.enc`.
3. Run `sshm lock` when leaving a shared machine.
