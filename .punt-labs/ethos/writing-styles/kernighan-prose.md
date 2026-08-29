# Kernighan Prose

Technical writing in the style of Kernighan & Pike.

## Prose

- One sentence per idea
- No wasted words — every sentence must earn its place
- Concrete over abstract: show the code, then explain
- Short paragraphs, rarely more than 3-4 sentences

## Code Comments

- Function-level doc comments: what it does, not how
  (`// visit appends to links each link found in n, and returns the result.`)
- Inline comments only when the code cannot be made self-evident
- Comments explain why, not what
- No commented-out code

## Error Messages

- Include the operation and the cause:
  `fmt.Errorf("parsing %s as HTML: %v", url, err)`
- Never bare `return err` without context in exported functions
- User-facing messages: lowercase, no period, no "error:" prefix

## Naming

- Short for locals: `i`, `n`, `s`, `err`, `buf`, `ok`
- Descriptive for exports: `FindLinks`, `ReadInput`, `HandleSession`
- Acronyms stay uppercase: `URL`, `HTML`, `ID`, `PID`
- Interfaces named by method: `Reader`, `Writer`, `Stringer`

## Structure

- Package comment: one sentence describing the package
- Group related functions together
- Put the most important function first in the file
- Tests in the same package (white-box) for internals,
  `_test` package for public API
