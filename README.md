# Beadle

> Signed instructions. Declared permissions. Tamperproof audit trail.

[![Working Backwards](https://img.shields.io/badge/Working_Backwards-hypothesis-lightgrey)](./prfaq.pdf)

Beadle is an autonomous agent daemon that runs on your machine under your cryptographic control. Every action requires a GPG-signed instruction from the owner, every command declares its permissions upfront, and the audit log is tamperproof. It's the Unix philosophy applied to AI agents --- small, composable, auditable tools that do one thing well.

**Status:** Hypothesis stage. The PR/FAQ document defines the product vision. A proof-of-concept validates the core integration (GPG-signed prompts, Claude CLI invocation, Proton Bridge email delivery). The daemon itself is not yet built.

## How It Will Work

```bash
# Set up your identity and GPG keychain
beadle init

# Write a command document (Markdown with declared permissions)
cat > check-tests.md << 'EOF'
---
interpreter: bash
permissions: [read, execute]
---
cd ~/project && pytest tests/ -v --tb=short
EOF

# Sign and run it
beadle sign check-tests.md
beadle run check-tests.md

# Pipe commands together
beadle run "check-tests.md | summarize-results.md | file-issues.md"
```

## Design Principles

- **Zero agent authority.** The daemon has no independent decision-making. Every action requires a GPG-signed instruction from the owner.
- **Preflight before execute.** All permissions are validated before any command runs. No partial execution, no "ask forgiveness" patterns.
- **Three interpreters.** Bash, Claude, and Python --- declared per command document.
- **Tamperproof audit.** Append-only, GPG-signed log of every action. Only the owner can clear it.
- **Email as remote trigger.** Send a signed command to Beadle's Proton Mail address; get results back in your inbox. Works from any device.

## Why

Autonomous agents are going mainstream, but the trust model hasn't kept up. ClawdBot gained 85,000+ GitHub stars in a week and was immediately found to have authentication disabled by default, malicious extensions in its skill directory, and prompt injection paths that exfiltrated API keys. OWASP published a Top 10 for Agentic Applications. The problem is real.

Beadle's answer: the agent has zero authority of its own. Trust is earned through cryptographic proof, not granted by default.

## Project Structure

```
prfaq.tex           # Working Backwards PR/FAQ document (LaTeX)
prfaq.bib           # Bibliography (16 entries)
prfaq.pdf           # Compiled document
meetings/           # Hive meeting summaries
```

Source code will live in `src/punt_beadle/` once development begins.

## PR/FAQ Document

The full product vision is defined in [`prfaq.pdf`](./prfaq.pdf), written using the [Amazon Working Backwards](https://www.aboutamazon.com/news/workplace/working-backwards) methodology. It covers:

- Press release with customer and spokesperson quotes
- 16 FAQs (external and internal)
- Four-risks assessment (Value, Usability, Feasibility, Viability)
- Feature appendix (Must Do / Should Do / Won't Do)
- Kill switch metrics: <100 GitHub stars or <10 weekly PyPI installs at 6 months triggers archival mode

## License

TBD

---

Built by [Punt Labs](https://punt-labs.com). Earn trust to go fast.
