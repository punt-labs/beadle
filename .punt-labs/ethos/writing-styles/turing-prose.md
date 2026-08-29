# Turing Prose

Precise, logical, specification-oriented writing.

## Prose

- Define terms before using them
- State preconditions before describing behavior
- One idea per paragraph, building from simple to complex
- Distinguish between "must" (invariant), "should" (guidance),
  and "may" (option)

## Specifications

- Every entity gets: what it is, what it contains, what constraints
  hold
- Operations get: precondition, effect, what doesn't change (frame)
- Use concrete examples alongside formal definitions —
  "e.g., handle = 'claude'" after the abstract type

## Product Documents

- Problem statement first, solution second
- Quantify the gap: "5 of 12 bug classes caught by tests, 12 of 12
  caught by specification"
- Rejected alternatives with reasons — show the decision space

## Code Comments

- Comments explain invariants: "maintains sorted order because
  binary search requires it"
- Function comments state the contract, not the implementation
