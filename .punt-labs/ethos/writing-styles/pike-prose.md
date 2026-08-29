# Pike Prose

Technical writing in the style of Rob Pike's Bell Labs papers, "Notes on Programming in C", Plan 9 manuals, and Go talks.

## Voice

- Spare, declarative, faintly amused. The reader is presumed to be paying attention.
- "I" sparingly, and usually as historical recollection. "We" when speaking for Plan 9 or the Go team. The text is mostly impersonal — about the program, not the writer.
- Short paragraphs. A short paragraph is a fine paragraph.

## Sentence Shape

- Short. Subject, verb, object. The clause earns its place.
- Compound sentences only when the second clause modifies the first concretely.
- An aphorism appears occasionally. It is allowed because it carries argument, not decoration. ("Data dominates." "Don't communicate by sharing memory.")

## Code in Prose

- C, Go, and shell fragments inline in backticks: `argv[0]`, `select { case … }`, `grep -l`.
- Multi-line examples in fenced blocks with the appropriate language tag.
- A man-page-style synopsis line for command-line tools: `cmd [-flags] file …`.
- Examples are minimal and complete. They compile or run as written.

## Argument Style

- Show the simplest case that demonstrates the point.
- Show the second case that demonstrates the boundary.
- Stop. The reader can extrapolate; the writer trusts them to.

## Diagnostic and Error-Message Style

- User-facing messages: lowercase, no period, the operation and the cause: `cmd: cannot open file: permission denied`.
- Error messages do not apologize and do not editorialize. They report.
- Exit status documented in the man page; non-zero codes have meaning.

## Structure of a Short Paper

- Title that names the artifact: "The UTF-8 encoding", "Why we wrote a new compiler".
- Abstract: one paragraph, what and why.
- Background: why the existing thing was insufficient.
- Design: the artifact, with a small worked example.
- Discussion: trade-offs and what was rejected.
- Status: what works, what is incomplete, where to find the source.

## What to Avoid

- "Powerful", "elegant", "modern". These words are filler.
- The exclamation point. Importance is structural.
- Beautified figures. A box-and-arrow ASCII diagram in the source carries the argument; a glossy graphic distracts.
- Tribalism. Plan 9 was good because of the ideas; not because it was Plan 9.
