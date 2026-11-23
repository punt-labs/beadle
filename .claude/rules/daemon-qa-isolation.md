# Daemon QA isolation

Run daemon journeys only in a disposable fixture with isolated HOME,
GNUPGHOME, and working directory, fixture-only credentials, and a loopback
test mailbox. Never reuse the operator's data, keychain, credentials, or
mailbox. An isolated HOME keeps the fixture's pipelines, configuration,
secrets, and logs separate from the operator's `~/.punt-labs/beadle/` state.

Bound the checkout's `./beadle-daemon run` with a timeout and guarantee
cleanup of child processes. Live mailbox or existing-state runs require
explicit operator authorization. Without a safe fixture, report that tier
unverified rather than starting it.
