# Example daemon recipes

A recipe is a `beadle-daemon` command file: a GPG-signed YAML document that
declares one thing the daemon is authorized to run when a trusted, permitted
sender's email matches it. Nothing in `beadle-daemon` runs unless it was
authorized this way -- see `docs/ARCHITECTURE.md`'s "zero agent authority"
invariant.

The two files here are worked examples, not fixtures: copy one, adjust it,
sign it, and it is a real recipe.

## sysreport.yaml

A `cli`-runner recipe. The daemon runs `beadle-sysreport` directly -- no
worker session, no LLM, no MCP servers -- and returns its JSON stdout as the
reply.

`beadle-sysreport` (`scripts/beadle-sysreport`) is a shell script, not a
compiled binary, so it has to be deployed by hand:

```bash
cp scripts/beadle-sysreport ~/.local/bin/beadle-sysreport
chmod +x ~/.local/bin/beadle-sysreport
```

The daemon's `cli` runner only resolves binaries out of `~/.local/bin` or its
own install directory (`internal/daemon/handler.go`'s `BinaryWhitelist`) --
this is deliberate and is not something a recipe can widen.

## docs-ask.yaml

A `claude`-runner recipe. The daemon spawns a Claude Code worker with the
`context7` MCP server available, so it can look up current
library/framework/API documentation instead of guessing from training data.
The question comes in as the `question` stage arg.

`context7` needs an API key. Set `CONTEXT7_API_KEY` in the daemon's own
process environment (systemd unit, launchd plist, or shell profile that
starts `beadle-daemon`) -- the recipe's `env_vars: [CONTEXT7_API_KEY]`
declares it as allowed to pass through, and the value reaches the worker's
subprocess environment without ever being written to a log line or an error
message. It never appears in this recipe file or in
`internal/daemon/templates.go`'s MCP registry either: the registry's
`context7` entry carries the literal placeholder `${CONTEXT7_API_KEY}`,
which Claude Code expands from the worker's own environment at spawn time.

## Signing a recipe

Both files here are **unsigned** -- `beadle-daemon` will not load a command
file without a valid signature from the key named in `daemon.json`. Two
steps, once per machine:

```bash
# 1. Name the key that authorizes command files. Either an ethos handle
#    (its beadle extension's gpg_key_id is used) or a literal fingerprint.
beadle-daemon init --handle claude
# or:
beadle-daemon init --fingerprint 0123456789ABCDEF0123456789ABCDEF01234567

# 2. Sign a recipe with that same key. This canonicalizes the file the
#    same way beadle-daemon verifies it, signs it with your system GPG
#    keyring, and proves the round trip verifies before writing anything.
beadle-daemon sign ~/.punt-labs/beadle/commands/sysreport.yaml \
  --signer 0123456789ABCDEF0123456789ABCDEF01234567
```

`sign` takes the full 40-hex fingerprint, not an email or short key ID --
the same identifier is used both to sign and to immediately re-verify the
result, so there is nothing to independently resolve or mismatch. `gpg
--list-keys --with-colons` shows a key's fingerprint on the `fpr` line
immediately after its `pub` line.

Deploy signed recipes to `~/.punt-labs/beadle/commands/` (the directory
`beadle-daemon run` loads from); re-sign a file whenever its content
changes -- the signature covers the whole command definition.

## Checking a signature later

`sign` already proves the round trip verifies at sign time. `verify` answers
the question that comes up afterward -- "why isn't my recipe loading?" --
without starting the daemon and reading logs:

```bash
beadle-daemon verify ~/.punt-labs/beadle/commands/sysreport.yaml
```

With no `--signer`, `verify` resolves the authorizer key from `daemon.json`
-- the exact key `beadle-daemon` itself trusts -- so it answers "would the
daemon load this file," not just "did signing succeed." Pass `--signer` to
check against a different key instead. It reports one of `good`,
`missing`, `wrong-key`, `key-expired`, or `invalid`, and exits non-zero on
anything but `good`.

## A known gap

`beadle-daemon` is not currently shipped in release binaries (`beadle-5ma`).
An operator running only the released `beadle-email` plugin cannot reach
`init`, `sign`, `verify`, or `run` at all. Building these tools was what
proved that gap actually blocks every recipe, not just these two -- see the
mission result for `beadle-7gm`.
