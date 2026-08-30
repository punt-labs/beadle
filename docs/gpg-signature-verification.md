# Command-File Signature Verification

Design for `internal/daemon.VerifySignature`, implemented in
`internal/daemon/signature.go` (DES-034) but with no caller yet — the
stub this design replaced (`internal/daemon/command.go:280-285`) has been
removed. This document covers the verification logic and its function
contract only. Wiring `VerifySignature` into a live execution path — the
daemon startup loader, the mission pipeline — is a separate concern for the
headless GPG email agent epic (beadle-9zh) and is addressed here only as a
forward-looking migration note.

## Problem

`ARCHITECTURE.md`'s "Zero agent authority" invariant says every action
requires a GPG-signed instruction from the owner. `VerifySignature` is the
function meant to enforce that for command YAML files. Before this design,
it didn't:

```go
func VerifySignature(_ *Command, _ string) error {
    return nil
}
```

It has zero callers today, so nothing currently depends on it — but a
hard-coded `nil` is worse than an absent function. It looks like a working
gate. A future caller that trusts this return value is trusting nothing at
all.

This is a different trust question from the one `internal/pgp/verify.go`
already answers. `verify.go` checks "is this signed by *someone*, and who" —
an arbitrary external email sender, key unknown in advance, verified in an
isolated GNUPGHOME so an untrusted key never touches the system keyring.
`VerifySignature` has to check "is this signed by *the one person allowed to
authorize commands*" — a single known identity, the daemon's owner, checked
every time against the same key. Same primitive (`gpg --verify`), different
shape of trust: one key, known in advance, versus any key, discovered at
verification time.

## Chosen approach

### 1. Where the authorized owner's key comes from

**Decision:** a dedicated owner-handle config field, resolved through the
same ethos-extension mechanism `internal/identity` already uses for the
daemon's own operating identity — but pointed at the owner, not the agent.

`internal/identity.Resolver.ResolveHandle(handle string) (*Identity, error)`
already exists and already returns `Identity.GPGKeyID`, read from
`<handle>.ext/beadle.yaml`'s `gpg_key_id` field. Today it has exactly one
caller, `switch_identity`, and it is always called with the handle of the
identity the daemon is about to operate *as*. Nothing about the method is
agent-specific — it resolves any ethos handle's beadle extension. Reusing
it for the owner needs no change to `internal/identity` at all: the daemon
just needs to know, at startup, which handle names the owner, then call
`ResolveHandle(ownerHandle)` and read `.GPGKeyID` off the result.

That one new piece of state — which ethos handle is the owner — belongs in
daemon-level config as a new field, e.g. `owner_handle` in a config that is
scoped to the *daemon instance*, not to whichever email identity happens to
be answering mail. `email.json` is the wrong home for it: it is loaded
per-operating-identity (`LoadIdentityConfig`), and a daemon has exactly one
owner regardless of which identity's mailbox it is currently polling.

**Key identifier format.** `Identity.GPGKeyID` today (`beadleExtension.GPGKeyID`,
`internal/identity/resolve.go:22`) is an unconstrained string — no format is
enforced anywhere it is read or written. That is fine for its one existing
caller (`switch_identity` uses it to select which key `sign.go` signs
outbound mail with; a short ID or an email address works there because
`gpg -u` accepts either and there is only ever the operator's own keyring in
play). It is not fine here: §3 below builds an isolation guarantee on the
premise that exactly one key gets imported, and `gpg --export <keyID>`
matches by suffix or by UID substring for a short key ID, a long key ID, or
an email address — any of which can match more than one key in a
keyring where a short-ID collision exists (a known, public weakness of short
key IDs) or where the owner has a second, stale key sharing the same email.
Config load must therefore validate `GPGKeyID` — when it is going to be used
as `ownerKeyID` — against a full 40-hex-character OpenPGP fingerprint
pattern (`^[0-9A-Fa-f]{40}$`, no `0x` prefix, no internal spaces) and reject
an owner config whose key identifier does not match. This is a startup
failure (per the migration plan's fail-closed default, §item 2), not a
per-file runtime warning: an owner identity that has never had a real
fingerprint recorded should never reach `VerifySignature` at all.

**Rejected: reuse `email.json`'s `GPGSigner`.** `GPGSigner` is the key the
*currently operating identity* signs outbound mail with — for the shipped
default, `claude@punt-labs.com`'s own key. It authenticates the daemon to
the outside world; it says nothing about who is allowed to instruct the
daemon. If `VerifySignature` trusted `GPGSigner`, a daemon process (or
anything that could reach its private key) could author and "authorize" its
own commands — precisely the zero-agent-authority violation the whole
feature exists to close. The owner and the identity the daemon operates as
must be structurally distinct in the config schema, not just distinct by
convention.

**Rejected: derive the owner from `.punt-labs/ethos.yaml`'s `agent:`
field.** That field already has a job — naming the agent handle for the
current repo session, consumed by `identity.Resolver.resolveHandle` to pick
which identity beadle operates as (`internal/identity/resolve.go:116-140`).
Overloading it to also mean "and this is who authorizes commands" collapses
two different roles (operator, owner) into one field, silently, which is
exactly the ambiguity this design has to remove.

**Rejected: a flat key fingerprint/email in daemon config, bypassing ethos
entirely.** Considered as the simplest possible thing, but ethos already
models a human identity with an attached GPG key (`beadle.yaml`'s
`gpg_key_id`), and beadle already has the plumbing to read it. A second,
parallel place to store the owner's key would drift from the ethos record
the moment the owner rotates keys. Kept as the fallback tail only — see
the migration section — mirroring the existing `fromDefault` pattern
(`internal/identity/resolve.go:211-228`) that already lets beadle run with
no ethos installed at all.

### 2. What the signature covers

**Decision:** a detached signature over the canonical YAML re-serialization
of the `Command` struct with `Signature` cleared to `""`, stored back in
the `signature` field itself.

The stub's doc comment says "the Signature field exists so YAML can carry
the signature for future verification" — a field embedded in the same
document it signs. Taken literally that's circular: the field can't cover
a hash of the file's current bytes, because its own current bytes are part
of what changes when you fill it in. Three ways out, matching the three
candidates in the design brief:

- **Whole file as literally-signed bytes, self-inclusive** — rejected
  outright. It's not circular in principle (sign the file with the
  `signature:` line blank, verify by re-blanking it) but *raw bytes*
  specifically is the wrong contract for a hand-edited YAML file: comments,
  key order, and quoting style are not preserved by decode→encode, and an
  operator who reformats or comments a command file has done nothing
  security-relevant but breaks the signature anyway. `internal/pgp/verify.go`
  already lives with this exact fragility for MIME messages ("we must use
  raw bytes (not re-serialized) to preserve the exact bytes that were
  signed," `verify.go:139-141`) because a MIME message's signed part is
  produced once, by software, and never hand-edited afterward. A command
  YAML file is edited by a human with a text editor. The two artifacts
  don't share that assumption, so they shouldn't share that design.
- **Clearsigned document** — rejected. It would replace `loadCommand`'s
  direct `yaml.NewDecoder(...).Decode(&cmd)` (`command.go:99-103`) with an
  unwrap-then-decode step for every file, changes the on-disk format from
  "a YAML file" to "an OpenPGP clearsign block containing YAML," and buys
  nothing the canonical-subset approach below doesn't already give.
- **Canonical subset (chosen)** — sign `yaml.Marshal` of the decoded
  `Command` with `Signature` zeroed. `gopkg.in/yaml.v3`'s struct marshaling
  is deterministic by field declaration order, not map iteration, so the
  same `Command` value always serializes to the same bytes regardless of
  how the source file was formatted, commented, or ordered on disk. The
  signer computes those bytes, signs them, and writes the result into
  `signature`; the verifier decodes the file, clears `signature` on its own
  copy, re-derives the same canonical bytes, and checks them against the
  stored signature. Comments and formatting in the file are free to change
  without invalidating the signature — the *decoded meaning* of the command
  is what's covered, not its typography — while every field an attacker
  might actually want to change (`binary`, `steps`, `prompt`, `write_set`,
  `env_vars`, ...) is covered because it's a struct field like any other.

This is a correction to the stub's doc comment, not just an implementation
of it: "the Signature field exists so YAML can carry the signature" is kept
true, but "carries the signature" now means "carries a signature over its
own canonical form," stated precisely instead of left circular.

### 3. Isolated GNUPGHOME vs. system keyring

**Decision:** reuse `verify.go`'s isolated-GNUPGHOME pattern, not
`sign.go`'s system-keyring usage — for a different reason than `verify.go`
had, but the same conclusion.

`sign.go` uses the system keyring because that's genuinely where the
owner's own private signing key already lives; there's no isolation benefit
to hiding a key from its owner. `VerifySignature` is not that operation. It
checks an artifact of unknown provenance — a command file, which per
`ARCHITECTURE.md`'s "Preflight before execute" invariant is exactly the
kind of input that must be validated before anything acts on it — against a
known-good public key. That's structurally `verify.go`'s problem, not
`sign.go`'s: an untrusted-until-proven input checked against key material,
run somewhere that can't leave a mark on the operator's real keyring and
can't be influenced by anything already sitting in it.

Two refinements over `verify.go`'s version of the pattern, both following
from the fact that here the key is known in advance instead of arbitrary:

- `verify.go` imports whichever key is attached to the message, or falls
  back to `exportAll` — importing the *entire* system keyring into the
  isolated homedir, because it has no way to know in advance which key it
  will need. `VerifySignature` always knows: it has `ownerKeyID` before it
  starts. It should export and import only that one key
  (`gpg --export <ownerKeyID>` piped into the isolated homedir's
  `--import`), not the whole keyring. Smaller blast radius, and it makes
  "wrong key" (§5) a distinguishable outcome instead of "gpg happened to
  find some other key that also verifies."
- The export step stays a one-way, read-only bridge from `~/.gnupg`, same
  invariant `verify.go`'s comment states for `exportAll` (`verify.go:72-74`)
  — never a write to the caller's real keyring.

**Ambiguity guard on import.** Even with the fingerprint format constrained
by §1, the isolated homedir's contents are asserted, not assumed. After
`gpg --import` runs, `VerifySignature` lists keys in the isolated homedir
filtered to `ownerKeyID` (`gpg --homedir <isolated> --batch --no-tty
--list-keys --with-colons -- <ownerKeyID>`) and requires exactly one `pub`
record whose own fingerprint (the paired `fpr` record) equals `ownerKeyID`
exactly, byte for byte. This mirrors the ambiguity guard
`pgp.CheckKeyExpiry`'s `parseColonExpiry` already applies at the list-keys
step (`internal/pgp/expiry.go:60-62`, `pubCount > 1` → error) — the same
discipline, applied to the export/import step this design adds, which today
has no equivalent check. Zero matching keys or more than one matching key is
a `*SignatureError` (§5), never a silent "verify against whatever's there."
In practice a correctly-scoped `gpg --export` of one 40-hex fingerprint
should never produce more than one key; the check exists because "should
never" is not the same guarantee as "is checked," and this is exactly the
kind of input-provenance boundary `ARCHITECTURE.md`'s "Preflight before
execute" invariant means to cover.

Isolation also buys the same testability `docs/TESTING.md` already banks on
for the "PGP integration" layer: an ephemeral owner keypair under
`/tmp/bg-*`, sign a fixture command, verify it, all without touching
anything outside the test's own temp directory.

### 4. Owner key expiry

**Decision:** call `pgp.CheckKeyExpiry(gpgBinary, ownerKeyID,
pgp.Homedir(isolatedHomedir))` — against the isolated homedir §3 builds and
imports the owner's key into, not the ambient system keyring — and fail if
it errors.

`CheckKeyExpiry`'s current signature, `func CheckKeyExpiry(gpgBinary, keyID
string) error` (`internal/pgp/expiry.go:16`), has no homedir parameter: it
always runs `gpg --list-keys` against gpg's default `GNUPGHOME`. Calling it
unmodified from `VerifySignature` would check expiry against whatever
happens to be in the ambient keyring — a different keyring, and potentially
a different copy of "the same" key, than the one §3 just imported and is
about to check the signature against. That is a split-brain that defeats
the isolation argument in §3: the ambient keyring could carry a stale,
unexpired copy of a key whose real, current copy has since expired (or been
rotated), and expiry would silently pass while signature verification runs
against entirely different key material.

The fix is a `Homedir` functional option on `CheckKeyExpiry`, following the
functional-options convention `punt-kit/standards/go.md` §3 documents:

```go
// ExpiryOption configures CheckKeyExpiry.
type ExpiryOption func(*expiryConfig)

type expiryConfig struct {
    homedir string
}

// Homedir directs CheckKeyExpiry to check keys in the given GNUPGHOME
// instead of gpg's default. Used to check a key inside an isolated
// keyring rather than the ambient one.
func Homedir(dir string) ExpiryOption {
    return func(c *expiryConfig) { c.homedir = dir }
}

func CheckKeyExpiry(gpgBinary, keyID string, opts ...ExpiryOption) error {
    var cfg expiryConfig
    for _, o := range opts {
        o(&cfg)
    }
    args := []string{"--batch", "--no-tty"}
    if cfg.homedir != "" {
        args = append(args, "--homedir", cfg.homedir)
    }
    args = append(args, "--list-keys", "--with-colons", "--", keyID)
    cmd := exec.Command(gpgBinary, args...)
    // ... unchanged: run cmd, then parseColonExpiry(stdout, keyID)
}
```

With no options passed, `CheckKeyExpiry` behaves exactly as it does today —
`sign.go`'s two existing callers (`Sign`, `DetachSignBody`), which check the
outbound signer's key against the ambient keyring where that key actually
lives, need zero changes. `VerifySignature` is the first caller to pass
`Homedir`, and doing so removes an operational precondition the stub's
original shape implicitly carried: the ambient system keyring no longer
needs to already have the owner's public key imported for expiry checking
to mean anything, because expiry is now checked against the exact key
material `gpg --verify` checks the signature against — the one §3 imported
into isolation moments earlier, not a separately-maintained copy in the
operator's own keyring.

### 5. Failure modes

**Decision:** distinguishable structured errors, following the
`*ValidationError` field-level pattern `punt-kit/standards/go.md` §3
documents as the house convention for domain validation failures a caller
needs to branch on. Beadle has no `ValidationError` type of its own yet
(nothing in `internal/` uses one today) — this is the first adopter, not a
reuse of existing beadle code, and it's worth being one: a future caller
(the daemon startup loader, per the migration note below) needs to make
different decisions for "this file was never signed" (possibly a file
mid-migration to the new scheme) versus "this file was tampered with"
(the one case that should page somebody) versus "signed, but not by the
owner" (a stale or wrong key, worth surfacing distinctly from a forged
signature) versus "the owner's key itself has expired" (an operational
hygiene gap, not evidence of an attack).

**How gpg's outcome maps to a reason.** The prior draft of this design named
four reasons without specifying how `gpg --verify`'s outcome maps to them.
The only existing precedent, `internal/pgp/verify.go:100-109`, string-matches
gpg's raw, human-readable stderr (`strings.Contains(line, "Good signature
from")`) with no `LC_ALL` pinning anywhere in that subprocess's environment —
its signer detection depends on the operator's locale and degrades silently
under a non-English `LANG`. That is tolerable for `verify.go`'s job (an
advisory `Signer`/`KeyID` field surfaced to a human for their own
information); it is not tolerable for a fail-closed authorization gate.
`VerifySignature` instead runs `gpg --verify` with `--status-fd 1` — gpg's
machine-readable status-line protocol, stable across gpg versions and
independent of message locale — and pins the subprocess environment to
`LC_ALL=C` as defense in depth even though `--status-fd` output does not
itself vary by locale. Status lines are matched on the fixed `[GNUPG:]
<KEYWORD> ...` prefix gpg documents in `doc/DETAILS`, never substring-matched
against the human-readable stderr stream `verify.go` uses.

| Status line | Meaning | `SignatureReason` |
|---|---|---|
| `GOODSIG` | valid signature, made by the one imported key | *(none — `nil` error)* |
| `NO_PUBKEY` | signature's key ID is not present in the isolated keyring | `ReasonWrongKey` |
| `BADSIG` | signature does not verify against the signed data | `ReasonInvalid` |
| `ERRSIG` | verification could not complete (unsupported algorithm, corrupt signature, etc.) | `ReasonInvalid` |
| `REVKEYSIG` | signature is by a key gpg considers revoked | `ReasonInvalid` (see below) |
| `EXPKEYSIG` | signature is otherwise valid but the signing key has since expired | `ReasonKeyExpired` |
| `EXPSIG` | the signature itself carries an expiration, now passed (distinct from key expiry) | `ReasonInvalid` |
| *(no recognized status line, or an unhandled one)* | unclassified gpg outcome — the default arm | `ReasonInvalid` |
| `cmd.Signature == ""` | no signature present in the file at all (checked before `gpg --verify` ever runs) | `ReasonMissing` |
| `CheckKeyExpiry` (§4) returns non-nil for the imported key | owner's key has expired or has no expiry set | `ReasonKeyExpired` (checked before signature verification runs) |

`NO_PUBKEY` is reachable only because exactly one key is imported (§3): if
the file's signature was made by a different key entirely, gpg has no
public key to check it against. Because §3 also rejects an import that
yields anything other than exactly one key with the exact configured
fingerprint, a `NO_PUBKEY` outcome can only mean the signature genuinely
wasn't made by the owner's key — never an artifact of an ambiguous import
finding "some other key that happened to match."

**`REVKEYSIG` folds into `ReasonInvalid` rather than becoming a fifth
reason.** A revoked owner key and a forged or corrupted signature call for
the identical operator response under this design: stop trusting the key
immediately and re-provision through `beadle init` (§ Migration, item 5).
That is unlike `ReasonKeyExpired`, which is a routine hygiene gap the
operator can plan a rotation around — a revocation means the owner (or
whoever holds the owner's revocation certificate) has already declared the
key untrustworthy, which is closer in kind to "this signature should not be
trusted" than to "this key needs renewing." Splitting it into its own reason
would add a branch to every future caller's remediation logic (the daemon
startup loader, the audit-log entry writer) without changing what that
caller does differently in response. `Detail` still carries gpg's own
status text, so a human reading an audit entry or a log line can tell a
`REVKEYSIG` outcome apart from a `BADSIG` even though `Reason` collapses
them to the same value.

**`EXPKEYSIG` and `EXPSIG` were found missing during implementation, not
designed here originally.** `CheckKeyExpiry` (§4) is meant to catch an
expired owner key before `gpg --verify` ever runs, but a full-diff review
after implementation found that its original form only checked that an
expiry field was *present*, never that the date it named had actually
passed — so an already-expired key reached `gpg --verify` regardless, which
emits `EXPKEYSIG` (not `GOODSIG`) for a signature that is otherwise valid
but made by an expired key. Fixed in both places: `CheckKeyExpiry` now
compares the expiry timestamp against the current time (closing the gate
this section always intended), and `classifyStatusLines` gained an explicit
`EXPKEYSIG` → `ReasonKeyExpired` case as defense in depth for the rare path
that still reaches `gpg --verify` directly. `EXPSIG` — a signature that
itself carries an expiration, a different gpg feature from key expiry —
folds into `ReasonInvalid` alongside `BADSIG`/`ERRSIG`/`REVKEYSIG`: none of
those outcomes mean "the key needs renewing," they mean "do not trust this
signature."

**The default arm never returns `nil`.** Any status-line outcome
`VerifySignature` does not explicitly recognize — a future gpg version's new
status keyword, an unexpected combination of lines, a partially-completed
run — falls into the same default arm as `BADSIG`/`ERRSIG`: a closed-world
`switch` over the parsed status lines, where every case except `GOODSIG`
constructs a non-nil `*SignatureError`, and there is no implicit fallthrough
to `return nil`. The only path to a `nil` return is the explicit `GOODSIG`
arm, reached after §3's key-uniqueness check and §4's expiry check have
both already passed.

```go
// SignatureError reports why a command file's signature failed to verify.
// Reason is one of the SignatureReason constants below; Detail carries
// gpg's own status-line output or other context for logs and audit entries.
type SignatureError struct {
    Reason SignatureReason
    Detail string
}

func (e *SignatureError) Error() string {
    return fmt.Sprintf("command signature %s: %s", e.Reason, e.Detail)
}

type SignatureReason string

const (
    ReasonMissing    SignatureReason = "missing"     // no signature present
    ReasonInvalid    SignatureReason = "invalid"     // BADSIG, ERRSIG, REVKEYSIG, EXPSIG, or an unrecognized outcome
    ReasonWrongKey   SignatureReason = "wrong-key"   // NO_PUBKEY: not signed by the owner's key
    ReasonKeyExpired SignatureReason = "key-expired" // owner key non-expiring or expired
)
```

Every one of these is fail-closed: a `nil` error is the *only* outcome that
means "authorized," and that includes beadle's own operational failures —
if the isolated homedir can't be created, or `gpgBinary` can't run,
`VerifySignature` returns a non-nil error, never `nil`. The stub's current
behavior (always `nil`) is not a smaller version of this design; it is the
one outcome this design exists to make impossible to reach silently.

## Rejected alternatives (summary)

| Question | Chosen | Rejected |
|---|---|---|
| Owner key source | dedicated `owner_handle` → `identity.Resolver.ResolveHandle` → `GPGKeyID` | reuse `GPGSigner`; overload `.punt-labs/ethos.yaml`'s `agent:` field; flat key config bypassing ethos |
| Owner key identifier format | full 40-hex fingerprint, validated at config load | short/long key ID or email address (ambiguous, collision-prone at `--export`) |
| Signature coverage | canonical `yaml.Marshal(cmd)` with `Signature` cleared | raw self-inclusive file bytes (circular / format-fragile); clearsigned document |
| Keyring strategy | isolated GNUPGHOME, import only `ownerKeyID`, assert exactly one key post-import | system keyring (`sign.go`'s pattern); `exportAll`-style whole-keyring import; import with no post-import count check |
| Key expiry | `pgp.CheckKeyExpiry` against the isolated homedir via a new `Homedir` option | check expiry against the ambient system keyring (split-brain against §3's isolated key material) |
| Signature outcome discrimination | `--status-fd` machine-readable status lines, `LC_ALL=C` pinned | substring-matching translated `gpg --verify` stderr (`verify.go`'s existing pattern) |
| Failure modes | `*SignatureError` with four reasons; `REVKEYSIG` folded into `ReasonInvalid` | single generic error; a fifth `ReasonRevoked` reason |

## Function contract

```go
package daemon

// VerifySignature checks that cmd was authorized by the owner identified by
// ownerKeyID (a full OpenPGP fingerprint — see the key-identifier-format
// note in the design doc's §1): it reconstructs the canonical bytes that
// were signed (cmd with Signature cleared to ""), imports ownerKeyID alone
// into an isolated GNUPGHOME, confirms exactly one key with that exact
// fingerprint landed there, checks that key's expiry against the same
// isolated homedir, and runs gpg --verify --status-fd against cmd.Signature.
// It never reads from or writes to the caller's own GPG keyring, and it
// fails closed on every branch, including its own operational errors.
//
// A nil error means: the owner, and only the owner, authorized this exact
// command definition, with a key that has not expired. Any other outcome
// returns a non-nil *SignatureError.
func VerifySignature(cmd *Command, gpgBinary, ownerKeyID string) error
```

The stub's existing shape — `(*Command, string) error` — was nearly right;
it just never received the second argument it needed. Today's one caller in
tests passes `"gpg"` as that string, i.e. the gpg binary path, matching
every other `pgp` function's `gpgBinary` first-or-early parameter. The real
contract needs one more piece of context the stub never had a slot for: the
owner's key ID. `ownerKeyID` is resolved once, at daemon startup, and
threaded through — `VerifySignature` itself does no identity resolution; it
takes the key ID as a plain string, the same shape `pgp.CheckKeyExpiry`
already accepts, and expects it to already be a validated full fingerprint
by the time it arrives (per the migration plan's startup-time check, item 2).

## Migration / wiring plan (forward-looking; not implemented here)

This section describes how a future caller — the daemon startup loader, in
the headless GPG email agent epic (beadle-9zh) — would consume the function
above. It is signature-only: no wiring code is written as part of this
mission.

1. **Add `owner_handle` to daemon-level config**, distinct from `email.json`
   and from `.punt-labs/ethos.yaml`'s `agent:` field, per §1. Daemon startup
   fails closed if it is unset — "preflight before execute" means there is
   no default owner, only a configured one.
2. **Resolve the owner's key once at startup**, not per file:
   `identity.NewResolver(...).ResolveHandle(ownerHandle)`, read `.GPGKeyID`
   off the result. An empty `GPGKeyID` (the owner's `beadle.yaml` extension
   not yet populated) is a startup failure, not "skip verification" — an
   absent owner key must never be read as "no verification needed."
   Additionally, validate the resolved `GPGKeyID` against the full-fingerprint
   pattern from §1 at this same startup step: a malformed, short, or email-form
   identifier is a startup failure with the identical fail-closed treatment
   as an empty one, never a value threaded through for `VerifySignature` to
   reject on a per-file basis later. The `fromDefault`-style flat-file
   fallback rejected as the *primary* source in §1 remains available here as
   the no-ethos install path, on the same terms `internal/identity` already
   applies to the daemon's own identity — and is subject to the identical
   fingerprint-format check.
3. **Call `VerifySignature` per file**, inside `loadCommand`
   (`command.go:92-109`), after the YAML decode and before
   `validateCommand` — the earliest point at which `Command.Signature` and
   the rest of the decoded struct both exist. A non-nil result is a load
   failure for that one file, same shape as today's `validateCommand`
   error path.
4. **Decide how a rejected file is logged**, deliberately left open here:
   today's `LoadCommands` treats an invalid file as `slog.Warn` and skip
   (`command.go:79-82`). A `*SignatureError` is not the same class of
   event as a YAML typo — `ReasonInvalid` in particular is evidence of
   tampering, and `ARCHITECTURE.md`'s "tamperproof audit log" invariant
   suggests a rejected signature belongs in that log, not just a warn-level
   skip. This touches the audit-log design (`docs/audit-beadle.tex`) and is
   out of scope for this mission; the wiring epic should resolve it as its
   own decision rather than inherit today's warn-and-skip by default.
5. **`beadle init`** (feat:init, a separate Must Do item) is the natural
   place an owner generates or declares their GPG key and gets
   `<owner-handle>.ext/beadle.yaml`'s `gpg_key_id` populated — with the full
   fingerprint format from §1, not a short ID or email. This design's
   contract is what that onboarding flow ultimately writes into; authoring
   `beadle init` itself is a separate piece of work.

## Testing implications

Table-driven, one subtest per `SignatureReason` plus a genuine-success case,
following `docs/TESTING.md`'s existing "PGP integration" layer: an
ephemeral GNUPGHOME under `/tmp/bg-*`, a throwaway owner keypair generated
per test, a fixture `Command` signed with `DetachSignBody`-equivalent logic
against the canonical bytes from §2. No live GPG Mail, no system keyring
touched, matching the isolation argument in §3 — the same reason
`verify.go`'s tests already run this way. Additional cases specific to this
design: a second, unrelated keypair to exercise `NO_PUBKEY` /
`ReasonWrongKey`; an expired owner keypair to exercise `ReasonKeyExpired`
without going through a real signature check; and a keyring seeded with two
keys sharing the target fingerprint's short ID (but distinct full
fingerprints) to exercise the §3 ambiguity guard.

### Carried-forward success criteria (for the implementation mission)

These were raised during design review as should-fix, not blocking — they
do not change the shape of `VerifySignature`'s contract, but they are
concrete enough to hold the implementation to and are recorded here so the
implementation mission inherits them as success criteria rather than
reopening design a third time.

- **Canonicalization determinism is round-trip tested, and signer and
  verifier share one function.** `Command.OutputSchema` is typed `any`
  (`internal/daemon/command.go:40`) and can hold a decoded
  `map[string]interface{}` — the one field where Go's map iteration order is
  normally randomized. The determinism claim in §2 needs a test that
  round-trips a fixture whose `output_schema` map keys are declared in two
  different orders, decodes both into `Command`, re-marshals both via the
  canonicalization step, and asserts byte-identical output. To make that
  claim mechanically enforceable rather than a shared prose description
  that a signer implementation and this verifier could independently drift
  from, the implementation names one exported function —
  `daemon.CanonicalCommandBytes(cmd *Command) ([]byte, error)` — that both a
  future signing tool and `VerifySignature` call, instead of duplicating the
  "marshal with `Signature` cleared" logic in two places.
- **The closed-world switch is itself tested, not just described.** §5
  states that the status-line switch has a `default` arm that still
  constructs a non-nil `*SignatureError`, never an implicit fallthrough to
  `return nil`. The implementation must include a test that feeds a gpg
  status line the mapping table does not recognize (a fabricated or
  future-version keyword) directly into the parsing function and asserts a
  non-nil `*SignatureError` with `ReasonInvalid` comes back — proving the
  default arm, not just documenting it.
- **Expiry is checked against the signing-capable key, not only the primary
  key.** `parseColonExpiry` (`internal/pgp/expiry.go:34-65`) inspects only
  `pub:` records — the primary key's own expiry — and never `sub:` records.
  Real-world GPG identities commonly hold a certify-only primary key and a
  dedicated signing subkey whose expiry is set independently; if the
  subkey that actually produced the signature has expired while the primary
  key has not, today's check would pass when it should fail. This design
  resolves the choice as: **extend `parseColonExpiry` to also inspect `sub:`
  records for a signing capability flag (an `s` in the eleventh
  colon-delimited capabilities field) and require that any signing-capable
  subkey present also carries a non-empty, non-zero expiry.** This is
  preferred over the alternative of pinning `gpg_key_id` to the signing
  subkey's own fingerprint directly, because §3's ambiguity guard is defined
  in terms of the *primary* key's own `fpr` record matching `ownerKeyID`
  exactly — pinning `gpg_key_id` to a subkey fingerprint would require that
  guard to branch on whether the configured identifier names a primary or a
  subkey, adding a second code path for what should be one small, uniform
  identifier format across §1, §3, and this expiry check. Extending
  `parseColonExpiry` to also walk `sub:` records keeps `ownerKeyID` meaning
  exactly one thing everywhere it is used: the primary key's own 40-hex
  fingerprint. This is the first call site where `CheckKeyExpiry` gates
  command execution rather than mail hygiene, so the gap is worth closing
  here even though `sign.go`'s existing callers do not need it today.

## Proposed DESIGN.md ADR entry

```markdown
## DES-034: Command-file signature verification — canonical-subset signing,
isolated verification against a known owner key

**Decision:** `VerifySignature(cmd *Command, gpgBinary, ownerKeyID string)
error` signs/verifies the canonical `yaml.Marshal` of `Command` with
`Signature` cleared, in an isolated GNUPGHOME that imports only the owner's
key (never the whole system keyring) and asserts exactly one key with that
exact fingerprint landed there. `ownerKeyID` must be a full 40-hex OpenPGP
fingerprint, validated at config-load time — not a short ID, long ID, or
email address, all of which can make `gpg --export` emit more than one key.
Expiry (`pgp.CheckKeyExpiry`, extended with a `Homedir` functional option) is
checked against that same isolated keyring, never the ambient system
keyring. Signature-outcome discrimination uses gpg's `--status-fd`
machine-readable protocol with `LC_ALL=C` pinned, mapped through a
closed-world switch to a `*SignatureError` distinguishing
missing/invalid/wrong-key/expired-key, with no implicit fallthrough to a
`nil` result.

**Why:** The stub was a hard-coded `nil` — an always-authorize backdoor
disguised as a working gate. Command-file authorization is a different
trust question from `verify.go`'s inbound-email verification (a fixed,
known owner key vs. an arbitrary sender's key), but the same isolation
argument applies: verification must not depend on, or be able to
contaminate, a real GPG keyring, and it must not depend on locale-sensitive
string matching of gpg's human-readable output. Signing the canonical
struct rather than raw file bytes makes the signature robust to
comment/formatting edits in a hand-maintained YAML file while still
covering every field that matters. Constraining the key identifier to a
full fingerprint and asserting a single import result closes a wrong-key
class of failure that an unconstrained identifier would silently reopen.

**Rejected:** reusing the daemon's own outbound `GPGSigner` as the trusted
key (collapses operator and owner into one identity, defeating the
invariant); raw self-inclusive file-byte signing (circular, format-fragile);
clearsigned command files (format change, no added benefit); a single
generic verification error (blocks a future caller from distinguishing
tampering from routine key hygiene); a fifth `ReasonRevoked` failure mode
(same operator remediation as `ReasonInvalid`, no behavioral difference to
justify the extra branch); checking key expiry against the ambient system
keyring (split-brain against the isolated key material the signature is
actually checked against).

See `docs/gpg-signature-verification.md` for the full design and the
migration plan for wiring this into the daemon startup loader (beadle-9zh).
```
