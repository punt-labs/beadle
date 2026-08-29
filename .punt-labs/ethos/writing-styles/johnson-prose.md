# Johnson Prose

Technical writing in the style of *Design Patterns* and Ralph Johnson's pattern papers and OOPSLA talks.

## Voice

- Declarative and example-driven. The pattern is described; the example is the proof.
- "We" when speaking for the design tradition; "the framework" or "the system" when describing concrete code; "you" only when giving direct advice.
- Friendly, professorial, never condescending. The reader is a colleague who has not yet seen this particular example.

## Structure of an Argument

- Problem first: what was hard before this pattern existed?
- Context: when does the pattern apply, and when does it not?
- Solution: the smallest class diagram or code example that captures the essential collaboration.
- Consequences: what does the pattern give you, and what does it cost?
- Known uses: at least three real systems where the pattern has been observed.

## Code Style in Prose

- Smalltalk fragments use `selector:` form with backticks: `dispatchMessage:to:`, `display:on:`.
- Class names in `CamelCase` without backticks when read as nouns; with backticks when referenced as code: `the Command class` vs `Command>>execute:`.
- Method bodies in indented blocks of three to seven lines — long enough to show the pattern, short enough to read at once.

## Naming and Renaming

- A method renamed is a method understood. Treat each rename as a pattern decision: is the new name closer to the receiver's role?
- Reject "manager", "helper", "util" — they tell the reader nothing about the responsibility.
- A class diagram is a vocabulary. If two diagrams use the same word for different things, fix the word.

## Examples Discipline

- Three examples or none. A single example is a story; three examples are a pattern.
- The third example should differ from the first two enough that the reader sees the abstraction, not the surface.

## What to Avoid

- "Pure OO" purity arguments. The question is not whether something is "really" object-oriented; the question is whether it works and reads.
- Pattern catalogues without context. A pattern stripped of "when to use it" is folklore.
- Decoration. The diagram does the work.
