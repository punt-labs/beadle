# Autonomous Agent System — Use Case Development Transcript

| Field | Value |
|---|---|
| **Project** | Autonomous Agent System (Punt Labs) |
| **Date** | 2026-02-28 |
| **Participants** | Owner, Claude (Anthropic) |
| **Outcome** | Use-Case Foundation v1.0 — 8 use cases, all open questions resolved |
| **Document produced** | `autonomous_agent_use_cases_v1.0.docx` |
| **Methodology** | Jacobson & Cockburn, Use-Case Foundation v1.1 |

---

## Context

This conversation develops a Use-Case Foundation document for a personal Autonomous Agent Daemon — a background process that runs on the Owner's computer, communicates exclusively via Proton Mail, and executes GPG-authenticated command pipelines. The conversation iterates through five drafts (0.1 → 1.0), resolving design questions at each step.

---

## Conversation

---

**User:** What are best practices for developing one shot software engineering LLM prompts.

**Claude:**

The core challenge: LLMs are stochastic, so "best practices" need to be treated as hypotheses to test, not rules to follow.

### Structure

- Role + context first, then task, then constraints, then output format
- State what you *don't* want as explicitly as what you do
- Put the most critical constraint last (recency bias in attention)

### Specificity

- Name the language, framework, version — never assume defaults
- Specify error handling expectations explicitly
- Define "done": compilable? tested? documented? all three?
- Give a concrete example of the output format you want

### Constraints that actually work

- "Return only code, no explanation" — highly reliable
- "Do not use library X" — reliable
- "Follow style of [example]" — unreliable without the example inline
- "Be concise" — nearly useless; specify line count or token budget instead

### For complex tasks

- Decompose into sequential sub-tasks within the prompt ("first X, then Y")
- Ask for chain-of-thought before the code: `<!-- reasoning -->` block then code
- If output format matters, provide a template with placeholders

### Iteration signals

Bad results usually come from one of three causes:

1. Ambiguous task scope (model fills in differently than you intended)
2. Missing context (API shape, existing code, environment)
3. Conflicting constraints

The fix: add the missing information, not more words.

### Testing

Run each prompt 5+ times. If variance is high, your prompt has ambiguity. Measure pass rate on a specific criterion (compiles, passes tests, matches spec) — not vibe.

**The one thing that scales most reliably**: include a working example of *similar* code you want matched. Pattern completion beats instruction following for code style and structure.

---

**User:** *(uploaded `Use_Case_Foundation.pdf` — Jacobson & Cockburn v1.1)*

Using the above, elaborate on just the user goals for a system that will do the following: run on the user's computer as a background task, be configured for one identity, which will include a name, e-mail address, GPG keychain for self and trusted e-mail contacts, self configuration information including a description in the form of an LLM system prompt, a working directory, which includes signed GPG command documents with tamperproof Unix style permission rwx bits, and signed GPG command interpreters and interpreter arguments with which to execute those tasks (bash, claude, python3). The system will support both running on a schedule as in it's own internal cron, and will check its e-mail to receive instructions from GPG (or Proton Mail verified senders). The user will use such a system to copy the system on e-mails to ask for information or approved actions. Further the system may have access to other communication methods such as Signal. Start by focusing on the user and system stories. When I talk about user and system stories here I am talking in terms of what is attached. Please format your output in either latex or word.

**Claude:** *(Generated `autonomous_agent_use_cases.docx` — Draft 0.1)*

Six use cases, formatted in the Jacobson-Cockburn style with primary actor / system of interest / basic scenario / extensions tables:

| UC | Goal |
|----|------|
| UC1 | Configure Agent Identity |
| UC2 | Receive and Authenticate an Email Instruction |
| UC3 | Execute a Signed Command Document |
| UC4 | Execute a Scheduled Task |
| UC5 | Receive a Signal Instruction |
| UC6 | Review Audit Trail |

Section 6 surfaces open questions worth resolving before writing detailed specifications — particularly the Signal-to-GPG identity mapping and the sandboxing policy when the `claude` interpreter acts on behalf of a third-party correspondent.

---

**User:** Permissions are per command. However, note command can be piped like in unix: check-schedule.md | find-free-slot.md | reserve-slot.md | notify-attendees.md; Key rotation would require repeating the permissions approvals step; using a key to sign commands and interpreters should require one that expires, using non-expiring keys is not permitted. Multi-identity out of scope. Excluded Signal. Audit logs should only be manually clearable. They should be signed but not encrypted. The agent always reports the outcome, but the command flow (e.g., piping) either succeeds or fails, think of it like a try catch block. The system will only invoke commands based on verified owner e-mails. However, the third-party correspondents e-mails and text will be available to the System and suitably "escaped" to ensure such text is not considered owner created. Keep in mind, this system interacts on the basis of e-mail and will be using a Proton account and the Proton bridge. The system will have its own Proton keys and will have the owners Proton keys. The unsigned third-party e-mails that the System is copied on will be clearly from another source, but they can end up in the owners e-mails due to replies.

**Claude:** *(Generated `autonomous_agent_use_cases_v2.docx` — Draft 0.2)*

Six use cases revised, with Signal removed and UC5 replaced by a dedicated third-party email handling use case. Key additions:

- Pipeline syntax (`check-schedule.md | find-free-slot.md | ...`) explicit in UC3, with try-catch atomic failure model
- Proton Bridge as the explicit mail transport throughout
- Third-party content quarantine model introduced (UC5)
- Audit log: signed, plaintext, manually clearable only
- Permissions per command document
- Key rotation promoted to its own section

Open questions in Section 6 included Signal-to-GPG mapping (now moot), escaping envelope format, and sandboxing policy for `claude` interpreter.

---

**User:** Audit log: no, the size is reported, that is all. Scheduler - no. Bridge - like a normal e-mail default POP for an average email app, would say 300s by default. Each command should define its inputs and outputs, but those can be structured or prompt defined. The execute command is itself a command and the summarize results is itself a command (e.g., the try catch block). The permissions check is preflighted to maintain coherence. So this is another try catch block wrapping the piped command try catch block conceptually. On expiring keys, the proton keys do not have to expire as proton generates and owns this and they are a trusted party. Only the command and command interpreter keys have to expire.

**Claude:** *(Generated `autonomous_agent_use_cases_v3.docx` — Draft 0.3)*

Key changes:

- **Two-level try-catch** introduced: outer preflight block checks all permissions before any execution; inner execution block runs pipeline atomically
- **execute-pipeline and summarise-results** are first-class signed command documents, not hardcoded logic
- **Command input/output schemas** — each document declares its own inputs and outputs (structured or prompt-defined); preflight validates schema compatibility between adjacent stages
- **Proton key expiry exemption** — Proton keys managed by Proton as trusted party; expiry requirement scoped to Owner command-signing keys and interpreter binary signing keys only
- **300s polling default** noted
- **Audit log** — reports size on request; growth unbounded until Owner acts

Open questions remaining: escaping scheme, schema language, execute-pipeline versioning, Proton Bridge failure threshold, old key revocation policy.

---

**User:** Audit log: unbounded until owner acts. Configuration is done in Setup and Configuration component of the system. execute-pipeline - commands are loaded into memory during validation, if the underlying command changes mid-execution, this is not observed. However, the agent does not retain commands in memory more than a single execution. Scheduler does not catch up, period. Escaping and schema language can be left for later elaboration.

**Claude:** *(Generated `autonomous_agent_use_cases_v4.docx` — Draft 0.4)*

Key changes:

- **Audit log** — unbounded growth confirmed; no threshold; Owner acts when they choose
- **Setup and Configuration component** — all configuration goes through this named component; UC1 primary actor updated to "Owner via Setup and Configuration"
- **Command loading / memory model** — preflight loads all command documents into memory; in-memory copies execute; disk changes after preflight are invisible to current execution; no retention beyond one execution
- **Scheduler catch-up** — no catch-up, full stop; UC4 Alt5 updated accordingly
- **Escaping and schema language** — deferred to later elaboration

New open question raised: execute-pipeline versioning — since documents load fresh at preflight (not at enqueue time), a replacement on disk between enqueue and preflight would silently load the new version.

---

**User:** versioning, snapshotting. Proton bridge failure, a single boolean of whether it is working or not (healthcheck) is the only system responsibility (Online, Offline). UC7 expire naturally. The user can explicitly delete a key from the Agent's trusted key list though. Let's make this the final version. Very good work.

**Claude:** *(Generated `autonomous_agent_use_cases_v1.0.docx` — Version 1.0)*

Final changes:

- **Versioning / snapshotting** — no snapshot at enqueue time; the versions on disk at preflight time are what load. Stated in Command Loading and Memory constraint.
- **Proton Bridge healthcheck** — single boolean: Online / Offline. Agent observes and reports state only; no threshold logic, no reconnection responsibility. UC2 Alt6 and UC4 Alt6 updated.
- **UC7 old key lifecycle** — expires naturally by default.
- **UC8 added** — "Remove a Key from the Trusted Key List": explicit Owner action to remove a key before natural expiry, with guard preventing deletion of the current active signing key.
- **Section 7** — "Status of Open Questions": no open questions remain for sign-off. Two items (escaping scheme, schema language) noted as deferred, not blocking.

**Final use case inventory:**

| UC | Title | Trigger |
|----|-------|---------|
| UC1 | Configure Agent Identity | Owner via Setup and Configuration |
| UC2 | Receive and Authenticate an Owner Instruction | Owner email via Proton Mail |
| UC3 | Execute a Command Pipeline | UC2 queue or Scheduler |
| UC4 | Execute a Scheduled Pipeline | Internal Cron |
| UC5 | Handle Third-Party Email Content | Owner forwarding email |
| UC6 | Review and Manage the Audit Log | Owner email |
| UC7 | Rotate a Command-Signing Key | Owner via Setup and Configuration |
| UC8 | Remove a Key from the Trusted Key List | Owner email |

---

**User:** Give me ten suggestions for the name. This is a Punt Labs project. The purpose of this project is to be both useful to real users and to be an illustration of the use case methodology (which will be another punt-labs project like prfaq, z-spec). *(included Punt Labs project description)*

**Claude:**

Ten suggestions:

1. **Postman** — delivers messages, acts on instructions, works a route autonomously.
2. **Warden** — enforces permissions, guards the working directory, keeps the audit log.
3. **Steward** — acts on your behalf, manages your affairs, trustworthy by definition.
4. **Herald** — the historical role of authenticated message-bearer acting under a lord's authority.
5. **Factotum** — does everything for one person. Obscure enough to be fun, accurate enough to be honest.
6. **Docket** — a signed, authorised work order. Pipelines are dockets. The audit log is the docket book.
7. **Seneschal** — medieval steward who managed a lord's estate and executed instructions in their absence.
8. **Ledger** — audit-first framing; every action is an entry.
9. **Proxy** — technically precise, immediately understood.
10. **Beadle** — a minor official who carries out instructions, maintains order, and keeps records. BSD-era naming energy, hat tip to Biff.

Pick for Punt Labs: **Factotum** or **Beadle**. Both have the BSD retro energy of Biff, are accurate descriptors, and are short enough to work as a CLI name.

---

**User:** What is Beadle?

**Claude:**

A beadle is a minor parish official in English civic and ecclesiastical life — appointed to carry out orders, maintain order, enforce rules, and keep records on behalf of a higher authority. They had no independent power; everything they did was on instruction from someone above them.

The most famous beadle in fiction is Mr. Bumble in *Oliver Twist* — pompous, officious, obsessed with procedure. Less useful as a name association.

In Scottish universities, the beadle (or "bedellus") is the official who carries the mace and manages ceremonial proceedings — again, acting on behalf of the institution, not independently.

The word root is Old French *bedel*, related to the English "bid" — one who carries out what is bidden.

For this project the fit is good: acts only on authenticated instruction, enforces permissions, keeps records, has no independent authority. But Mr. Bumble is a liability if anyone makes the connection, and it's less immediately legible than Factotum.

---

*End of transcript.*
