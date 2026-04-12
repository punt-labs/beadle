#!/bin/sh
set -eu

# Copy GPG keyring from read-only host mount to tmpfs-backed ~/.gnupg.
# Private key material exists only in memory -- destroyed on container stop.
if [ -d /mnt/gpg-source ]; then
  cp -a /mnt/gpg-source/. "$HOME/.gnupg/"
  chmod 700 "$HOME/.gnupg"
fi

exec "$@"
