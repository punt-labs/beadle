# Lattner Prose

Technical writing in the style of Chris Lattner's LLVM design docs, swift-evolution proposals, and conference talks.

## Voice

- Direct, energetic, opinionated without bluster. The argument carries the energy; the prose stays measured.
- "We" for the language/team perspective; "I" for personal stance, used sparingly; "the user" for the developer who will read code in this language.
- Strong stances stated plainly: "this is the right design", "this is a non-goal". The case is made in the next paragraph.

## Structure

- Headline summary at the top: one paragraph that says what this proposal does and why it matters.
- Motivation: the concrete problem in code, with a before/after example.
- Detailed design: the language change, the type-system implications, the runtime cost, the tooling impact.
- Source compatibility: explicit. ABI stability: explicit. Future directions: explicit.
- Alternatives considered: substantive. Each alternative gets a paragraph explaining why it was rejected.

## Code in Prose

- Swift fragments in fenced blocks with `swift` language tag. Examples compile against the current toolchain.
- Inline references in backticks: `Optional<T>`, `@MainActor`, `some Sequence<Int>`.
- Diff blocks for proposal changes: `+` lines additive, `-` lines removed, with the surrounding context.

## Argument Style

- Lead with the user-visible behavior. Implementation detail comes later.
- Quantify when possible: code size, compile time, runtime overhead, binary footprint.
- Contrast with adjacent languages when illuminating: "in C++ this requires…", "in Rust this looks like…". Never to disparage; always to clarify trade-offs.
- "This is a non-goal" is a real sentence. Stating non-goals saves the reviewer from arguing about them.

## Diagnostic Style

- A compiler error message is a teaching moment. Show the source, the diagnostic with caret, the fix-it suggestion.
- When a feature interacts with another feature, show the interaction explicitly with a small example.
- Edge cases get their own subsection.

## What to Avoid

- "Powerful", "elegant", "modern" without specifics. The properties are powerful; the words are not.
- Pure ideology. Memory safety, performance, expressiveness are trade-offs in tension; the proposal explains how this design balances them, not that one of them wins absolutely.
- Vagueness about ABI or source compatibility. If the proposal is silent, reviewers will assume the worst — and they will be right to.
