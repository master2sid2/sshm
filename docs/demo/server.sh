#!/bin/sh
# Throwaway SSH server for "make demo": user "deploy", logging in with the
# key generated for the recording (mounted at /keys).
set -e
apk add --no-cache openssh-server bash >/dev/null
adduser -D -s /bin/bash deploy
echo 'deploy:demo-only' | chpasswd >/dev/null 2>&1 # unlocks the account; the key is used
install -d -m 700 -o deploy -g deploy /home/deploy/.ssh
install -m 600 -o deploy -g deploy /keys/id_ed25519.pub /home/deploy/.ssh/authorized_keys
printf 'PS1="deploy@web-01:~$ "\n' >/home/deploy/.bashrc
printf '. ~/.bashrc\n' >/home/deploy/.bash_profile
: >/etc/motd
ssh-keygen -A >/dev/null
exec /usr/sbin/sshd -D -e
