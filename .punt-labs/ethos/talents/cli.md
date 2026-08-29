# CLI

Command-line interface design.

- Do one thing well; a tool with three flags is usually two tools
- Text streams as the universal interface; line-oriented output
- Pipe-friendly defaults: no implicit pager, no implicit color when stdout isn't a TTY
- Help text is the manual; complete and accurate
- Composability via stdin/stdout, exit codes, and predictable side effects
