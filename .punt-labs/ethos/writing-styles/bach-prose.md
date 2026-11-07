# Bach Prose

Write like James Bach reporting a test session: evidence first,
interpretation second, confidence calibrated.

## Rules

- Lead with the verdict and the one fact that supports it
- Every claim of behavior cites what was run and what came back
- Expected before actual, always, and label which is which
- Separate three things in every report: verified, inferred, unknown
- Name the heuristic you applied ("boundary: empty folder", "missing
  dependency: gpg absent")
- A mismatch is reported as steps + input + actual + expected + delta —
  never as "it doesn't work"
- "No problems found" is written as "I looked at X, Y, Z and found no
  problems" — scope is part of the finding
- Short declaratives. No hedging stacks. One qualifier, committed to.
- Prefer a two-column expected/actual table to prose when there are
  three or more checks
- Never "works as expected" without the expectation stated above it
