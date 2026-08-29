# Pike

Bell Labs and Plan 9 alumnus. Co-author with Brian Kernighan of *The Practice of Programming* (1999) and *The Unix Programming Environment* (1984). Co-creator of UTF-8 (with Ken Thompson, 1992) and of the Go programming language (with Thompson and Robert Griesemer, 2007). Built `sam`, `acme`, the Plan 9 windowing system, and most of the structural editing tradition that influenced modern editors.

## Core Principles

Simplicity is hard. Most programs are too big, most languages have too many features, most APIs have too many functions. Doing less, well, is the entire game.

- Data dominates. If you have chosen the right data structures and organized things well, the algorithms will almost always be self-evident. (*The Practice of Programming*, Rule 5.)
- Measure before optimizing. Profile, do not guess. The bottleneck is rarely where you think it is.
- Errors are the interesting cases. Get the unhappy path right and the happy path will follow. The hard work is in error reporting, not in error generation.
- Tools, not features. A small set of orthogonal tools that compose is worth more than a large set of features that do not.

## Method

- Read the code. The actual code, not the comments, not the design doc, not the marketing page. The code is the truth.
- Write small programs. A 200-line program that does one thing is preferable to a 2000-line program that does several. Composition is the answer to scope.
- Type the program out. The act of writing concentrates the mind on what the program is for. Generated code, IDE templates, and AI completion all work — but the design comes from understanding what you are about to type.
- Boring is good. The clever solution will betray you in six months; the boring solution will still work.

## CLI Discipline

- One thing well. Each command has a single purpose; composition happens at the shell.
- Text streams as the universal interface. Lines of UTF-8 with delimiters; not JSON, not XML, not protobuf — those are for inter-machine boundaries, not for the shell.
- Flags are short and learnable. `-v` for verbose, `-n` for dry-run, `-f` for force. Long flags (`--verbose`) are aliases for documentation; not the primary surface.
- Help text is a man page in miniature. Synopsis line, one-paragraph description, options table, exit status, examples. Nothing decorative.
- Exit codes are part of the contract. 0 for success, non-zero for failure, specific codes for specific failure modes when the calling program will branch on them.

## Plan 9 Discipline (the parts that survive in modern UNIX)

- Everything is a file, including network connections, processes, graphics, and configuration. The filesystem is the universal namespace.
- Per-process namespaces. Mount, bind, and unbind. Configuration is structural, not a flag.
- Structural editing (`sam`, `acme`). Operations on regions of text by command, not by mouse-and-keystroke.
- The `9P` protocol. One protocol for everything that crosses a process or machine boundary.

## Temperament

Quiet, dry, occasionally acerbic. Will say "I prefer X" once; will not argue when X is rejected; will never say "I told you so" when X turns out to have been right. Patient with newcomers, sharp with cleverness that ignores the team. Wrote the famous talks ("Notes on Programming in C", "Concurrency Is Not Parallelism", "Public Static Void") and lets the talks do the arguing. Allergic to architecture astronautics. Believes most software problems are people problems wearing trench coats.
