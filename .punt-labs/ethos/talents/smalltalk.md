# Smalltalk Engineering

Smalltalk/Pharo development. Image-based programming, Morphic UI, and
test-driven design in the style of Kent Beck's _Smalltalk Best Practice
Patterns_.

## Language

- Everything is an object. Every operation is a message send.
- Precedence: unary > binary > keyword. Parentheses override.
- Blocks are closures: `[ :x | x + 1 ]`. They are first-class objects.
- Cascades (`;`) send multiple messages to the same receiver.
- `^` returns from the enclosing method, not just the block.
- Strings use single quotes. Symbols use `#`. Comments use double quotes.
- Class definition is a message to the superclass, not a declaration.

## Beck's Patterns

_Smalltalk Best Practice Patterns_ (1997) defines the coding discipline:

- **Composed Method**: every method does one identifiable thing. If it
  has more than 5-7 lines, decompose. Name the extracted method for
  what it does, not how.
- **Intention Revealing Selector**: `sendMessage:` not
  `postToMessagesEndpoint:`. `textContent` not
  `collectTextBlocksAndJoin`. The name communicates the what; the body
  communicates the how.
- **Pluggable Behavior**: use blocks and symbols to parameterize
  behavior. `streamMessage:do:` takes a callback block. Tool handlers
  are blocks. The caller decides what to do; the framework decides when.
- **Collecting Parameter**: build results incrementally. The message
  accumulator receives streaming events one at a time and constructs the
  full message progressively, rather than collecting all events first.
- **Method Object**: when a method gets too complex (too many temps,
  nested conditionals), extract it into a separate class whose instances
  represent a single execution. The SSE decoder and tool runner are
  natural method objects.
- **Double Dispatch**: let objects negotiate behavior through message
  sends. Content blocks dispatch on their `type` field to a factory
  method, not through if-else chains.
- **Typed Collection**: use domain-specific collection wrappers when
  the collection has behavior beyond storage.
- **Query Method**: methods that answer a question should not have side
  effects. `textContent` answers a string; it does not modify the message.
- **Execute Around Method**: `[ code ] on: Error do: [ :e | handle ]`
  is the pattern for resource management and error recovery.

## Pharo 12

### Deprecated APIs

- `cls protocolNames` not `cls organization categories`
- `cls selectorsInProtocol:` not `cls organization listAtCategoryNamed:`
- `ref methodClass` not `ref actualClass` on CompiledMethod
- `CodeImporter evaluateFileNamed:` not `FileStream fileIn:` (removed)
- `self class compiler evaluate:` not bare `Compiler evaluate:`

### Tooling

- Tonel format for Iceberg integration (one `.class.st` per class)
- STONJSON for JSON (built in). NeoJSON is not in the default image.
- `SharedQueue` for concurrent producer/consumer

### Class Definition (fluid syntax — mandatory)

The old `subclass:instanceVariableNames:classVariableNames:package:` is
DEPRECATED. Always use the fluid syntax:

```smalltalk
(Object << #MyClass
  slots: { #instVar1. #instVar2 };
  package: 'MyPackage') install.
```

### Method Compilation

Always `compile:classified:` — never bare `compile:`. Protocols:
`accessing`, `json`, `tests`, `testing`, `printing`, `models`,
`instance creation`, `converting`, `private`. No `as yet unclassified`.

### JSON Serialization

No reflection (`instVarNamed:`, `allInstVarNames`). Each class implements
`asJson`/`fromJson:` with explicit accessors. Use collecting parameter
pattern for classes with many fields.

### Lint

`make lint` from the Bash tool is the canonical gate. Zero non-clean
lines required before any commit.

```bash
make lint 2>&1 | grep -v ': clean$' | grep -v '^$' || true
# expected output: nothing
```

`make lint` runs Renraku across all classes — including class-level
rules (`Unused instance variable`, `Class not referenced`, etc.) that
per-method `m critiques` does not see. Never use `m critiques` alone
as the lint gate; it misses the class-level rules and has historically
caused findings to slip past "lint clean" claims.

The `critiques` API is a valid debugging tool for investigating a
specific finding already identified via `make lint`. It is not the gate.

### Class Comments

Every class must have a comment. Use `ClassName comment: '...'` to set
it — do NOT redefine the class. Comments must include at least one
executable example using the `>>>` convention:

```smalltalk
  ClassName new
  >>> a ClassName
```

`>>> X` means "evaluating the preceding expression returns X." Use the
actual printed representation. For side-effect-only expressions (no
meaningful return value), use inline comments `"..."` instead of `>>>`.
See the Beck personality for the full standard.

### Test Runs

Only run project-scoped tests. Never the full Pharo test suite — it
leaks watchdog processes.

## Threading

Pharo uses cooperative green threads (Process). Critical rules:

- Never block the Morphic UI thread with network I/O
- Fork a Process for any blocking operation: `[ ... ] forkAt: Processor userBackgroundPriority`
- Use `WorldState defer: [ ... ]` for Morphic modifications from background
- Use `SharedQueue` or `Semaphore` for inter-process communication
- Processes at the same priority do not preempt each other — they yield
  only at I/O operations or explicit `Processor yield`
- `ZnClient` connections do not survive image save/restore

## Morphic

- Every visible thing is a Morph. Morphs form an owner/submorphs tree.
- `openInWorld` adds to desktop. `openInWindowLabeled:` wraps in SystemWindow.
- TableLayout for flow-based layout. `listDirection: #topToBottom`.
- FTTableMorph for tables. FTMultiColumnDataSource for multi-column data.
- Morphic is retained mode (damage-based redraw), not immediate mode.

## Testing (SUnit)

- SUnit is the test framework. Subclass `TestCase`.
- `setUp` / `tearDown` for fixture management.
- `self assert: actual equals: expected`
- `self should: [ code ] raise: ExceptionClass`
- TDD: write the failing test first, then the code that makes it pass.
- Every method gets a test. Every bug fix gets a regression test.
- Tests document behavior — if someone asks "what does X do?", the
  answer should be in a test.

## Iceberg (Git)

- Tonel format: one directory per package, one `.class.st` per class
- `IceRepository registry` to access registered repos
- Commit, push, fetch, branch — all via libgit2
- Merge not supported — use CLI
- SSH via ed25519 key
