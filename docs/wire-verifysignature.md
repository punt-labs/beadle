# Wiring VerifySignature Into the Daemon

Design for the wiring gap DES-034 deliberately left open (tracked under
beadle-iru — the original migration note's "beadle-9zh" pointer was wrong;
that epic closed without ever covering this):
`internal/daemon.VerifySignature` (`internal/daemon/signature.go`) is
implemented and correct, but nothing calls it. `internal/daemon/command.go`'s
`LoadCommands` / `loadCommand` decode and validate a command YAML file today
without ever checking its signature, so an unsigned or hand-edited command
file loads and runs exactly as a legitimately owner-authorized one would.
This is the gap `prfaq.tex`'s Feature Appendix names as a launch-blocking
Must Do ("GPG command signing enforcement... it is not yet wired into
daemon startup, so nothing rejects an unsigned or tampered file today").

## Problem

Three things have to exist before `VerifySignature` can run against a real
command file at daemon startup, none of which exist today:

1. **A place to configure who the owner is.** `VerifySignature` takes
   `ownerKeyID` as a parameter — a full 40-hex OpenPGP fingerprint — but
   nothing in the daemon resolves that value. There is no daemon-level
   config file at all yet; `cmd/beadle-daemon/main.go`'s `run` command reads
   `email.json` (per operating identity) and calls `daemon.LoadCommands`
   directly against `<dataDir>/commands`, with no owner concept in between.
2. **A call site.** `loadCommand` (`command.go:92-109`) decodes YAML and
   calls `validateCommand`; `VerifySignature` is never invoked anywhere in
   that path.
3. **A decision for the no-ethos deployment.** DES-034's migration note
   (`docs/gpg-signature-verification.md`, item 2) assumed a
   "`fromDefault`-style flat-file fallback... remains available" as the
   no-ethos path for resolving `ownerKeyID`. That assumption does not hold:
   `identity.Resolver.ResolveHandle` (`internal/identity/resolve.go:106-111`)
   calls `fromEthos` only, with no fallback branch at all, and
   `fromDefault` (`resolve.go:211-228`) — the function DES-034's note had in
   mind — populates only `Identity.Email` from a plain-text file; it has no
   concept of a GPG key and cannot populate `Identity.GPGKeyID`
   (`identity.go:11`: "from beadle extension (optional)" — there is no
   non-ethos extension mechanism to fall back to). A deployment with no
   ethos installed at all therefore has no path to adopt this feature,
   full stop, under DES-034's plan as written. This design has to resolve
   that explicitly, not carry the assumption forward.

This document proposes the daemon-level config schema, the resolution and
validation sequence, the call site, and the audit-logging decision DES-034
deferred. It does not touch `internal/daemon/signature.go` — `VerifySignature`
itself is unchanged.

## Chosen approach

### 1. Daemon-level config: `daemon.json`

**Decision:** a new config file, `~/.punt-labs/beadle/daemon.json`, loaded
once at `beadle-daemon run` startup — a sibling of `email.json`, not a
field inside it, per DES-034 §1's reasoning: `email.json` is loaded
per-operating-identity (`LoadIdentityConfig`), and a daemon has exactly one
owner regardless of which identity's mailbox it happens to be polling right
now.

```go
package daemon

// Config holds daemon-instance configuration — settings that describe the
// daemon process itself, not any one email identity it operates as.
type Config struct {
    OwnerHandle   string `json:"owner_handle,omitempty"`
    OwnerGPGKeyID string `json:"owner_gpg_key_id,omitempty"`
}
```

Two mutually exclusive ways to name the owner's key, both optional at the
struct level but exactly one required for the daemon to start with signing
enforcement active:

- **`owner_handle`** — an ethos handle, resolved through
  `identity.Resolver.ResolveHandle(handle)` exactly as DES-034 §1 designed:
  read `<handle>.ext/beadle.yaml`'s `gpg_key_id` off the returned
  `Identity.GPGKeyID`. This is the path for any deployment with ethos
  installed, and it is preferred: the key follows ethos's identity record,
  so a key rotation recorded there is picked up without touching
  `daemon.json`.
- **`owner_gpg_key_id`** — a full 40-hex OpenPGP fingerprint, written
  directly into `daemon.json`. This is the no-ethos path this design adds
  to close the gap identified above. It carries no name, no email, nothing
  but the fingerprint `VerifySignature` already requires — an operator with
  no ethos installation gets it by running `gpg --list-secret-keys
  --with-colons` against their own keyring and copying the `fpr` field, the
  same value `beadle init` (feat:init, a separate Must Do item) will
  eventually write into `<handle>.ext/beadle.yaml` for the ethos path.

**Precedence: an error, never "one wins."** Configuring both is not "one
wins" — it is an ambiguous config, and `docs/gpg-signature-verification.md`
§5 already establishes the house pattern for that: `assertSingleOwnerKey`
treats an ambiguous key import as a hard failure rather than picking one
silently. This design applies the identical treatment one layer up, at
config load, before any key material is even touched — and, per the
partial-fail-closed decision below, "hard failure" here means command
loading is disabled, not that the daemon process refuses to start:

```go
// ResolveOwnerKeyID turns c's owner-key config into a validated fingerprint,
// or an error naming exactly why none is usable. It is exported because
// cmd/beadle-daemon/main.go (package main) calls it directly on the *Config
// LoadConfig returns; the fingerprint check inside it reuses signature.go's
// unexported fingerprintPattern, which only internal/daemon code can reach
// — main.go must never need to touch that var itself.
func (c *Config) ResolveOwnerKeyID(resolver *identity.Resolver) (string, error) {
    var keyID string
    switch {
    case c.OwnerHandle != "" && c.OwnerGPGKeyID != "":
        return "", fmt.Errorf("daemon.json: owner_handle and owner_gpg_key_id are both set — ambiguous, set exactly one")
    case c.OwnerHandle != "":
        id, err := resolver.ResolveHandle(c.OwnerHandle)
        if err != nil {
            return "", fmt.Errorf("resolve owner handle %q: %w", c.OwnerHandle, err)
        }
        if id.GPGKeyID == "" {
            return "", fmt.Errorf("owner identity %q has no gpg_key_id in its beadle extension", c.OwnerHandle)
        }
        keyID = id.GPGKeyID
    case c.OwnerGPGKeyID != "":
        keyID = c.OwnerGPGKeyID
    default:
        return "", fmt.Errorf("daemon.json: set owner_handle or owner_gpg_key_id — no default owner")
    }

    // Identical validation for both paths: whichever branch resolved a
    // value, it must pass the same full-fingerprint pattern VerifySignature
    // itself enforces (signature.go:46) before ResolveOwnerKeyID accepts
    // it — a malformed, short, or email-form identifier fails here, at
    // config load, with one clear error naming the misconfigured field,
    // never threaded through for VerifySignature to reject once per file.
    if !fingerprintPattern.MatchString(keyID) {
        return "", fmt.Errorf("daemon owner key %q is not a full 40-hex OpenPGP fingerprint", keyID)
    }
    return keyID, nil
}
```

**Fails closed for command loading only — never the whole daemon.**
"Preflight before execute" means there is no default owner: a `daemon.json`
that is absent, or present with neither field set, or set but unresolvable
(a bad `owner_handle`, a malformed `owner_gpg_key_id`), must never silently
mean "skip verification" — that is exactly the always-authorize backdoor
DES-034 closed for `VerifySignature` itself. But the scope of that failure
is *command loading*, not the daemon process. Verified directly against
`cmd/beadle-daemon/main.go`'s call graph: `commands` is built once and
consumed by exactly one downstream call, `daemon.NewMailHandler(...,
commands)` — the mail-triggered pipeline dispatcher. Mail polling
(`email.NewPoller`, started independently) has no code path through
`commands` at all, and this binary has no MCP server (`beadle-email serve`
is a separate binary). `LoadCommands` itself already treats an empty or
unreadable commands directory as non-fatal today
(`main.go:119-122`: `logger.Warn(...); commands =
make(map[string]*daemon.Command)`) — the daemon has run with zero loadable
commands since before this design existed, because "zero commands" and "no
command-triggered pipelines run" are the same observable state either way.

Failing the whole daemon over a misconfigured or absent `daemon.json` would
take mail polling down with it for no invariant-preserving reason — strictly
more disruptive than "you'll notice no commands run and the log names why,"
with no invariant the harsher failure protects that the softer one doesn't
already protect. **Total** fail-closed is reserved for the one thing the
invariant is actually about — command loading never returning a command
that skipped verification while claiming to have passed it — never for the
daemon process as a whole. `cfg.ResolveOwnerKeyID`'s error (either variant
above) is therefore handled the same way at the `run` call site: caught,
logged loudly, and treated as "command loading disabled," not propagated as
a `RunE` failure that would exit the process:

```go
    daemonCfg, err := daemon.LoadConfig(filepath.Join(dataDir, "daemon.json"))
    var ownerKeyID string
    if err != nil && !os.IsNotExist(err) {
        logger.Error("daemon config unreadable, command loading disabled", "error", err)
    } else if err == nil {
        ownerKeyID, err = daemonCfg.ResolveOwnerKeyID(resolver)
        if err != nil {
            logger.Error("signature policy unavailable, command loading disabled", "error", err)
            ownerKeyID = "" // explicit: falls back to disabled, never to "trust anyway"
        }
    }
    // ownerKeyID == "" here means VerifySignature is never called at all
    // (see §3) -- LoadCommands loads unsigned files exactly as it does
    // today, the same steady state as an empty commands directory.
```

This is not a new decision — it is round 1 of this design, verified against
the same call graph and restated here because it is the load-bearing reason
the config-loading code above never returns a fatal error to `RunE`.
Rejected for the same reasons round 1 gave: refusing to start over a config
field unrelated to mail polling or MCP tooling is disproportionate, and
"preflight before execute" is satisfied exactly as well by disabling the one
thing preflight is checking.

### 2. Where resolution happens

**Decision:** once, at `beadle-daemon run` startup, in
`cmd/beadle-daemon/main.go` — mirroring the existing `newResolver()` /
`openDaemonLogFile()` pattern already in that file, not per-file inside
`LoadCommands`. `ownerKeyID` is a single value for the life of the daemon
process; re-resolving it on every file load would mean a hundred command
files trigger a hundred redundant `ResolveHandle` calls (or a hundred
identical no-op reads of `owner_gpg_key_id`) for no benefit.

(The config-loading call site itself is shown in §1 above — it never
returns a fatal error from `run`'s `RunE`, per the partial-fail-closed
decision there. What follows here is unchanged from that:)

```go
    cmdDir := filepath.Join(dataDir, "commands")
    commands, err := daemon.LoadCommands(cmdDir, gpgBinary, ownerKeyID)
```

This changes `LoadCommands`'s signature from `LoadCommands(dir string)
(map[string]*Command, error)` to `LoadCommands(dir, gpgBinary, ownerKeyID
string) (map[string]*Command, error)`, threading the two new parameters
down to `loadCommand` and from there into `VerifySignature`. `gpgBinary` is
already a value `main.go` needs to resolve for other reasons (the
`internal/pgp` package's functions all take it as their first parameter);
this design assumes it arrives the same way `sign.go`'s callers already get
it and does not re-litigate that resolution here.

**`ownerKeyID == ""` is the explicit, load-bearing "disabled" signal —
`loadCommand` must check it before calling `VerifySignature`, never pass it
through unconditionally.** `VerifySignature`'s own first check
(`internal/daemon/signature.go:82-87`) validates `ownerKeyID` against a
full-40-hex-fingerprint pattern and returns a `*SignatureError{Reason:
ReasonInvalid}` for anything that doesn't match — including an empty
string. Calling `VerifySignature` unconditionally with `ownerKeyID == ""`
would therefore reject every command file the moment `daemon.json` is
unset, exactly the opposite of §6's "zero behavior change when unset"
promise — a bug this design must not ship, not a hypothetical one, since
`ownerKeyID`'s empty-string value is precisely what §1 arrives at whenever
signing enforcement isn't configured or fails to resolve. §3 below gates
the call on this explicitly.

### 3. The call site inside `loadCommand`

**Decision:** call `VerifySignature` immediately after the YAML decode and
before `validateCommand` — the earliest point at which `Command.Signature`
and the rest of the decoded struct both exist, exactly where DES-034's
migration note (item 3) placed it:

```go
func loadCommand(path, gpgBinary, ownerKeyID string) (*Command, error) {
    data, err := os.ReadFile(filepath.Clean(path))
    if err != nil {
        return nil, fmt.Errorf("read %s: %w", path, err)
    }

    var cmd Command
    dec := yaml.NewDecoder(strings.NewReader(string(data)))
    dec.KnownFields(true)
    if err := dec.Decode(&cmd); err != nil {
        return nil, fmt.Errorf("parse %s: %w", path, err)
    }

    if ownerKeyID != "" {
        if err := VerifySignature(&cmd, gpgBinary, ownerKeyID); err != nil {
            return nil, fmt.Errorf("verify signature %s: %w", path, err)
        }
    }
    // ownerKeyID == "" means signing enforcement is not configured (§1) --
    // VerifySignature is never called, and loadCommand behaves exactly as
    // it does today. This is the one branch that keeps §6's "zero behavior
    // change when unset" true; skipping it would reject every file, not
    // zero files, the moment daemon.json is absent.

    if err := validateCommand(&cmd); err != nil {
        return nil, fmt.Errorf("validate %s: %w", path, err)
    }
    return &cmd, nil
}
```

Signature verification runs before schema validation on purpose: a command
file that fails both should be reported as an authorization failure, not a
schema error, because an operator reading the log needs to know "this file
was not authorized" before "this file also happens to be malformed" — the
first is a security event, the second is not.

### 4. How a rejected file is logged — the decision DES-034 deferred

**Decision:** a `*SignatureError` result is logged at `slog.Error`, distinct
from `LoadCommands`'s existing `slog.Warn` treatment of an ordinary
YAML/validation failure, and carries the `SignatureReason` as a structured
field so it is grep-able and alertable on:

```go
func LoadCommands(dir, gpgBinary, ownerKeyID string) (map[string]*Command, error) {
    entries, err := os.ReadDir(dir)
    if err != nil {
        return nil, fmt.Errorf("read command dir %s: %w", dir, err)
    }

    cmds := make(map[string]*Command)
    for _, e := range entries {
        if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
            continue
        }
        path := filepath.Join(dir, e.Name())
        cmd, err := loadCommand(path, gpgBinary, ownerKeyID)
        if err != nil {
            var sigErr *SignatureError
            if errors.As(err, &sigErr) {
                slog.Error("reject command file: signature verification failed",
                    "path", path, "reason", sigErr.Reason, "detail", sigErr.Detail)
            } else {
                slog.Warn("skip invalid command file", "path", path, "error", err)
            }
            continue
        }
        if _, dup := cmds[cmd.Name]; dup {
            slog.Warn("skip duplicate command name", "name", cmd.Name, "path", path)
            continue
        }
        cmds[cmd.Name] = cmd
    }
    return cmds, nil
}
```

This is a level distinction, not a routing decision — both outcomes still
end in "skip this file, keep loading the rest," matching today's
fail-open-on-one-file / never-fail-the-daemon posture (§1) that
`prfaq.tex`'s Must Do item describes ("reject unsigned or tampered
files," singular, not "refuse to start"). `slog.Error` is the mechanism by
which this becomes visible to whatever log-monitoring the operator already
has, without this design inventing a new alerting channel.

**The tamperproof audit log (`ARCHITECTURE.md`'s invariant) is out of
scope for this wiring, on a corrected premise from DES-034's note.**
DES-034 item 4 pointed at `docs/audit-beadle.tex` as the place a rejected
signature "belongs... not just a warn-level skip." That pointer does not
hold: `docs/audit-beadle.tex` is a March 2026 CLI/UX standards-compliance
report (confirmed by reading its own header), not an audit-log design, and
no audit-log implementation exists anywhere in `internal/` today — a repo
search for `audit` under `internal/` turns up only `internal/enable`
(plugin-enablement bookkeeping), `internal/daemon/signature.go`, and
`internal/daemon/mission.go`, none of which implement an append-only signed
log. `ARCHITECTURE.md`'s "Audit log is tamperproof" invariant therefore
describes a target state with no current implementation to hook into. This
design's `slog.Error` treatment is the interim mechanism until that
invariant has a real implementation to wire a rejected-signature event
into; building that implementation is its own mission, out of scope here,
and should not be inferred as already covered by this design.

### 5. Interaction with an ethos-configured `owner_handle` whose extension is later populated

Not a new decision — restating DES-034 §migration item 2's existing rule so
it is visible at the one call site that now enforces it: an `owner_handle`
that resolves via ethos but whose `<handle>.ext/beadle.yaml` has no
`gpg_key_id` yet (the pre-`beadle init` state) is a startup failure, not
"skip verification for now." An absent owner key must never be read as "no
verification needed," on either the `owner_handle` or `owner_gpg_key_id`
path — `ResolveOwnerKeyID` above returns an error for both cases (empty
`GPGKeyID` on the ethos path per its own branch; the config-level "neither
field set" default case for the no-ethos path).

## Rejected alternatives

| Question | Chosen | Rejected |
|---|---|---|
| No-ethos owner-key path | `owner_gpg_key_id` — a direct fingerprint field in `daemon.json`, mutually exclusive with `owner_handle` | leave unaddressed / assume DES-034's stated `fromDefault`-style fallback "remains available" (it does not: `fromDefault` has no GPG-key concept, `ResolveHandle` has no fallback branch); scope the no-ethos case out entirely as a stated future gap |
| Precedence when both `owner_handle` and `owner_gpg_key_id` are set | an error — ambiguous config disables command loading, same treatment `assertSingleOwnerKey` already gives an ambiguous key import | `owner_handle` silently wins; `owner_gpg_key_id` silently wins |
| Where `daemon.json` lives | a new sibling file to `email.json`, one per daemon instance | a field inside `email.json` (wrong scope — loaded per operating identity, but a daemon has one owner regardless of which identity's mailbox it polls) |
| When `ownerKeyID` is resolved | once at `beadle-daemon run` startup, threaded into `LoadCommands` | per-file inside `loadCommand` (redundant re-resolution, no benefit) |
| Call-site ordering inside `loadCommand` | `VerifySignature` before `validateCommand`, gated on `ownerKeyID != ""` | after `validateCommand` (misreports an authorization failure as a schema error first); calling `VerifySignature` unconditionally (rejects every file once unset, since an empty `ownerKeyID` fails the fingerprint check) |
| Logging a rejected signature | `slog.Error` with a structured `reason` field, distinct from the existing `slog.Warn` YAML-error path | route into `docs/audit-beadle.tex`'s audit log (no such implementation exists to route into — a corrected premise from DES-034's note); leave at the same `slog.Warn` level as an ordinary parse error (loses the security/hygiene distinction) |
| Startup fail-closed scope | partial — an absent or unresolvable owner config disables command loading only, verified against `cmd/beadle-daemon/main.go`'s call graph | total — refuse to start the whole daemon over a config field mail polling and MCP tooling have no dependency on |

## Implementation plan

1. Add `internal/daemon/config.go`: the `Config` struct, `LoadConfig(path
   string) (*Config, error)` (JSON, following `internal/email/config.go`'s
   pattern), and `ResolveOwnerKeyID(resolver *identity.Resolver) (string,
   error)` per §1 above, including the fingerprint-format validation.
2. Change `LoadCommands` and `loadCommand`'s signatures to accept
   `gpgBinary, ownerKeyID string`, and add the `VerifySignature` call per
   §3 — gated on `ownerKeyID != ""`, never called unconditionally.
3. Split `LoadCommands`'s error handling per §4: `errors.As` for
   `*SignatureError`, `slog.Error` with structured fields on that branch,
   unchanged `slog.Warn` otherwise.
4. Update `cmd/beadle-daemon/main.go`'s `run` command per §1–§2: load
   `daemon.json`, resolve `ownerKeyID`, and on any resolution failure
   (absent file, both fields set, unresolvable handle, malformed
   fingerprint) log at `slog.Error` and continue with `ownerKeyID == ""` —
   never propagate the error out of `RunE`. This is settled, not an open
   question: verified against the actual call graph that mail polling and
   MCP tooling have no dependency on `commands`, so the daemon must keep
   running either way.

## Testing implications

- `internal/daemon/config_test.go` (new): table-driven, covering — both
  fields empty (error), both set (error), each set alone with a
  well-formed fingerprint (success on the direct path; a fake
  `identity.Resolver` fixture with a temp ethos dir for the handle path),
  a malformed fingerprint on either path (error), an `owner_handle` whose
  resolved identity has an empty `GPGKeyID` (error).
- `internal/daemon/command_test.go`: extend `LoadCommands`'s existing table
  tests with a signed-and-valid fixture (loads), an unsigned fixture
  (rejected, `slog.Error` path — assert via a captured `slog` handler, not
  just the returned map), a fixture signed by a second, unrelated keypair
  (rejected as wrong-key), and confirm a rejected file does not appear in
  the returned map while a sibling valid file still does (partial-failure
  behavior unchanged from today).
- No changes needed to `internal/daemon/signature_test.go` — this design
  does not modify `VerifySignature` or its existing test coverage.

## Proposed DESIGN.md ADR entry

```markdown
## DES-035: Wiring VerifySignature into daemon startup — daemon.json, owner_gpg_key_id, and the no-ethos owner-key path

**Decision:** a new `~/.punt-labs/beadle/daemon.json` (`internal/daemon.Config`)
holds exactly one owner-key source: `owner_handle` (resolved through
`identity.Resolver.ResolveHandle`, per DES-034 §1) or `owner_gpg_key_id` (a
full 40-hex fingerprint written directly into config). Both fields set,
neither set, or either failing to resolve to a valid fingerprint disables
command loading — never the daemon process, which has no other dependency
on the commands map (verified against `cmd/beadle-daemon/main.go`'s call
graph: mail polling and the MCP server, a separate binary, do not consume
it). Whichever path resolves a value, it is validated against the same
fingerprint pattern `VerifySignature` itself enforces before it is accepted.
`ownerKeyID` is resolved once at `beadle-daemon run` startup and threaded
into `LoadCommands(dir, gpgBinary, ownerKeyID)` → `loadCommand`, which
calls `VerifySignature` — gated on `ownerKeyID != ""`, so an unconfigured
daemon behaves exactly as it does today — immediately after YAML decode and
before `validateCommand`. A rejected signature logs at `slog.Error` with a
structured `reason` field, distinct from the existing `slog.Warn` treatment
of an ordinary parse/validation failure.

**Why:** DES-034 implemented `VerifySignature` correctly but left it with
no caller and an inaccurate assumption in its migration note — that a
`fromDefault`-style flat-file fallback already covers the no-ethos case.
It does not: `fromDefault` populates only an email address, with no GPG-key
concept, and `ResolveHandle` has no fallback branch at all. Without
`owner_gpg_key_id`, a deployment with no ethos installed would have no way
to adopt this feature — a real completeness gap in a codebase that
otherwise treats ethos as optional everywhere else (see the README's
identity section). `owner_gpg_key_id` closes that gap with the smallest
addition that matches the format `VerifySignature` already demands: a bare
fingerprint, obtainable from `gpg --list-secret-keys --with-colons` with no
ethos apparatus required.

**Rejected:** silently preferring one field over the other when both are
set (hides a misconfiguration instead of failing loudly); a field inside
`email.json` instead of a dedicated `daemon.json` (wrong scope — a daemon
has one owner independent of which identity's mailbox it polls);
re-resolving `ownerKeyID` per command file instead of once at startup
(redundant, no benefit); verifying the signature after schema validation
instead of before (misreports an authorization failure as a schema error);
calling `VerifySignature` unconditionally regardless of whether an owner
key resolved (rejects every command file the moment `daemon.json` is
unset, since an empty `ownerKeyID` fails the fingerprint check — the
opposite of the intended zero-behavior-change default); failing the whole
daemon process when the owner key can't be resolved (disproportionate —
verified that no other daemon subsystem depends on command loading
succeeding); routing a rejected signature into `docs/audit-beadle.tex`'s
audit log (no such implementation exists — that document is a CLI/UX
compliance report, not an audit-log design, corrected here from DES-034's
note); scoping the no-ethos gap out silently instead of deciding it in
this document.

See `docs/wire-verifysignature.md` for the full design, the rejected-
alternatives table, and the implementation plan.
```
