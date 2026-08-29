# Cox Prose

Technical writing in the style of Russ Cox's Go blog posts and research.swtch.com essays.

## Voice

- Long-form, deliberate, paragraph-shaped. The argument has time to breathe.
- "We" for the Go team's collective decisions; "I" for personal opinions or experiments; "you" for direct advice to the reader.
- No throat-clearing. The first sentence states the topic; the second states what is at stake.

## Structure

- Open with the problem stated in a single paragraph.
- Walk through the history. What did we try first? Why did it not work?
- Present the design. Each design decision is a separate paragraph with motivation, alternative considered, and chosen path.
- Close with what is still hard, and what users should do today.

## Sentence Shape

- Mid-length, complete, declarative. Subordinate clauses for nuance, never for hedging.
- Numbered lists for ordered procedures; bulleted lists rarely. Most arguments work as paragraphs, not bullets.
- Tables when the data is tabular. Three columns with headers, no merged cells.

## Code in Prose

- Go fragments inline in backticks: `go.mod`, `package foo`, `errors.Is(err, ErrFoo)`.
- Multi-line examples in code blocks with language tags. Examples compile.
- Diagrams are SVG with text labels — never PNG screenshots of whiteboard photos.

## Diagnostic Style

- Show the symptom: the actual command output, the actual error message.
- Then the analysis: what does this tell us? What does it not tell us?
- Then the fix, with a test that would have caught it.

## Footnotes and Asides

- Footnotes for tangential history or attribution; never for substantive argument.
- Asides in parentheses are rare. If a thought is worth including, it is worth a sentence in the main flow.

## What to Avoid

- "Cargo cult" arguments — "everyone uses X" is not a reason. The reason is the property X provides.
- Triumphalism. Go is a tool with trade-offs. So is the alternative.
- Vague benchmarks. Cite the workload, the hardware, and the standard deviation.
