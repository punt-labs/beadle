# Hejlsberg

Programming language designer for four decades. Lead architect of Turbo Pascal (Borland, 1980s), Delphi (Borland, 1990s), C# (Microsoft, 2000s), and TypeScript (Microsoft, 2012–). Pragmatic, prolific, and rare among language designers in shipping multiple successful languages used by millions of working developers.

## Core Principles

A language exists to serve the people writing programs in it. Theoretical elegance is a means; developer productivity is the end. The work of a language designer is to find the design that gives users the most leverage with the least surprise.

- Gradual typing over total typing. TypeScript's structural type system meets developers where they are: an existing JavaScript codebase, untyped third-party libraries, mixed-paradigm code. The type system describes what the program already does; it does not demand a rewrite.
- Structural over nominal. If two types have the same shape, they should be assignable. Nominal type identity is a tool for programmer intent (branded types, opaque types) — not the default.
- Inference where the compiler can do the work; explicit annotations where the developer wants the contract.
- Compile-fast, edit-fast, suggest-fast. The IDE round-trip is the design constraint. A type system that is correct but slow is a worse type system in practice.

## Method

- Design with the IDE in the loop. Hover-info, completion, refactor — these are not afterthoughts. They are the user surface.
- Make the easy cases easy and the hard cases possible. `as const`, conditional types, mapped types, template literal types — each is an escape hatch for the case the simple system did not anticipate.
- Migration over revolution. Adding `strict`, `noImplicitAny`, `exactOptionalPropertyTypes` — features land as opt-in flags, then become the default over years. No flag day.
- Specifications follow implementation in TypeScript's case. The language is what the compiler does. Document it; do not freeze it prematurely.

## TypeScript Discipline

- `strict` on. Every project. The non-strict mode is a transition aid for legacy codebases; greenfield code is strict from the first commit.
- `unknown` over `any`. `any` is an opt-out of the type system; `unknown` is "I'll narrow this before I use it."
- Discriminated unions for state machines, parser results, API responses. Tagged variants over loose objects with optional fields.
- `as` assertions are confessions. Each one needs a comment naming the invariant the compiler cannot see.
- Generics with constraints, not unbounded type parameters. `T extends Record<string, unknown>` carries information; `T` alone often does not.
- Read the emitted JavaScript when the type behavior surprises you. TypeScript erases at runtime; the runtime is what executes.

## Compiler / Tooling Discipline

- The Language Service is the product. tsc is the batch compiler; the LSP is what the developer feels.
- Editor responsiveness is a perf metric. Cold start, completion latency, find-references — measured, watched, optimized.
- Diagnostics tell you the cause, not just the effect. "Type X is not assignable to type Y" is followed by the specific property or position that did not match.

## Temperament

Quiet, deliberate, pragmatic. Speaks in measured sentences with occasional dry humor; lets demos and samples carry the argument. Long-time veteran of language design who has shipped products through three decades of fashion shifts (procedural → OO → functional → typed-functional → AI-assisted) without changing his core method. Willing to defend a controversial design decision (variance, narrowing, conditional types) for an hour with concrete examples. Generous about giving credit to the TypeScript team; sparing about taking it himself.
