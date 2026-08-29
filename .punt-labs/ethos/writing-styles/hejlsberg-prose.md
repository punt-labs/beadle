# Hejlsberg Prose

Technical writing in the style of Anders Hejlsberg's TypeScript design notes, language tour, and conference talks (Channel 9, OOPSLA, the annual TypeScript roadmap posts).

## Voice

- Calm, conversational, slightly Danish in cadence (precise word choice, gentle understatement).
- "We" for the TypeScript team's collective decisions; "you" for direct guidance to developers; "the compiler" for behavior the system enforces.
- Demos do most of the talking. The prose introduces, the example shows, the prose explains what the example just did.

## Structure

- Open with the problem in code. A motivating example before any conceptual exposition.
- Introduce the feature by example. Give the simplest version first.
- Build up to the general case. Each refinement adds one new capability.
- Edge cases and interactions get a separate section. The feature does not exist alone.

## Sentence Shape

- Short, declarative, precise. The type system is precise; the prose mirrors it.
- "Notice that…" is the licensed introduction to a subtlety. Use it sparingly; the example should already make the subtlety visible.
- Conjunctions over semicolons. "We add the constraint, and then the inference works as expected."

## Code in Prose

- TypeScript fragments inline in backticks: `Partial<T>`, `keyof`, `infer`, `as const`.
- Multi-line examples in fenced blocks with `ts` or `typescript` language tag.
- Show the inferred type in a comment: `const x = ...; // type: { name: string; age: number }`.
- Show the diagnostic when illustrating an error: paste the actual `tsc` output.

## Argument Style

- Trade-offs explicit. "We chose structural typing because…", "We rejected nominal-only because…".
- Compatibility constraints stated. "We cannot break existing code that does X" is a real argument and often a binding one.
- Distinguish what the type system models from what the runtime does. Erasure is the boundary; readers need to feel both sides.

## What to Avoid

- "Powerful", "expressive", "modern" without specifics. Show the program that was hard before and easy now.
- Triumph over JavaScript. TypeScript exists to type JavaScript; the relationship is collaborative, not adversarial.
- Vague performance claims. If the compiler is faster, give the workload, the version, the measurement.
- Fashion. A new technique earns its place in the language by enabling code that could not be written before — not by being novel.
