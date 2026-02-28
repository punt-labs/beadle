# PR/FAQ Hive Meeting Summary

**Document:** `prfaq.tex` (Beadle — Autonomous Agent Daemon)
**Date:** 2026-02-28
**Stage:** `\prfaqstage{hypothesis}` / v1.0
**Mode:** Hive (autonomous consensus, Agent Teams)
**Participants:** Wei (Principal Engineer), Priya (Target Customer), Alex (Skeptical Executive), Dana (Builder-Visionary)

---

## Decisions

| # | Hot Spot | Door | Decision | Resolution | Winning Argument | Dissent |
|---|----------|------|----------|------------|------------------|---------|
| 1 | GPG as authentication primitive | One-way | REVISE | CONSENSUS | Wei/Priya/Alex: age fallback is misleading, GPG abstraction is asserted not demonstrated, SSH signing unconsidered | Dana: disagreed (GPG is right, stop hedging), committed |
| 2 | Email vs CLI as primary interface | Two-way | REVISE | CONSENSUS | All four: headline leads with highest usability risk instead of actual differentiator (trust model) | None — unanimous |
| 3 | Competitive moat lifespan | One-way | REVISE | CONSENSUS | Dana/Alex: moat is structural property (cloud providers cannot replicate local-only execution without contradicting their business model), not a feature lead with a shelf life | None — unanimous |
| 4 | Next validation FAQ contradicts PoC | Two-way | REVISE | CONSENSUS | Wei/Alex: the two FAQs describe different timelines — customer evidence says PoC runs daily with Proton Bridge, next step says "build a prototype" and "add Proton Bridge" | None — unanimous |
| 5 | Proton Bridge as single point of failure | One-way | REVISE | CONSENSUS | Wei/Priya: "CLI-only fallback" is not a mitigation — it eliminates the product's primary value; Proton choice is a trust model decision that needs defending | None — unanimous |
| 6 | TAM — honest niche or methodology showcase? | Two-way | REVISE | CONSENSUS | Wei/Alex: "methodology showcase" is used as both success condition and failure mode in the same document, undermining credibility of both | None — unanimous |
| 7 | 300-500 hours opportunity cost | Two-way | REVISE | CONSENSUS | Wei/Priya: 67% variance range with no per-phase breakdown; opportunity cost mitigation relies on "methodology showcase" already flagged as insufficient | None — unanimous |

---

## Debate Narratives

### 1. GPG as Authentication Primitive (ONE-WAY, CRITICAL)

Wei identified that `beadle init`'s ability to abstract GPG from zero-knowledge users is asserted but never designed — the mitigation reads "invest heavily" which is a budget allocation, not a design. Priya reinforced this: the document claims "No GPG knowledge needed" but provides no evidence this abstraction works. Alex pointed out that SSH signing (which developers already use daily) was never evaluated as an alternative. Dana pushed back, arguing GPG is the only standard with signing + identity + non-repudiation and the document should own the choice harder. The majority won: the document must remove the misleading `age` fallback (age has no signing), add SSH signing to the evaluation, and be honest that GPG abstraction is the product's hardest unsolved UX problem. Dana disagreed and committed.

### 2. Email vs CLI as Primary Interface (TWO-WAY, CRITICAL)

All four personas converged independently on the same diagnosis: the headline leads with "via Email" but the Getting Started section leads with CLI (`beadle run`), and the pre-mortem names email-as-control-plane as the top failure mode. Wei noted the trust model — not email — is the actual differentiator. Priya observed that the headline's positioning overweights email when real usage starts at the terminal. Alex calculated that deferring Proton Bridge integration could save ~2 months. Dana saw the bigger opportunity: "Trusted autonomous agent with cryptographic owner control" is a stronger headline than "agent via email." Unanimous: reframe the headline around the trust model, demote email to one delivery mechanism.

### 3. Competitive Moat Lifespan (ONE-WAY, WARNING)

Dana delivered the winning argument: cloud-hosted AI assistants have an inherent conflict of interest between their business model (data, telemetry, platform lock-in) and the security model a paranoid developer needs (local execution, owner-controlled audit, no third-party visibility). Beadle occupies the space that is structurally off-limits to any commercial cloud product. Alex reinforced this: the moat is structural misalignment with vendor business models, not a feature gap that closes in 12-18 months. Wei agreed: remove the time-bounded prediction and defend the moat structurally. Priya grounded it in customer behavior: if Claude ships daemon mode, it will be cloud-hosted — developers who need local-only execution have no alternative. Unanimous: replace the "12-18 months" concession framing with the structural argument.

### 4. Next Validation FAQ Contradicts PoC (TWO-WAY, WARNING)

All four agreed this is a copy-editing gap, not an architectural issue. The Customer Evidence FAQ describes a working prototype with Proton Bridge running as a daily LaunchAgent for several weeks. The Next Step FAQ says "Build a minimal prototype... Then add Proton Bridge integration" — as if starting from scratch. Wei was direct: a reader will conclude either the author forgot what they built, or the evidence section is fabricated. Alex added the falsification angle: the two FAQs need to agree on what the PoC actually covers. Dana noted the PoC shifts the feasibility risk profile in a way the document fails to communicate. Unanimous: rewrite the Next Step FAQ to start from the actual PoC state.

### 5. Proton Bridge as Single Point of Failure (ONE-WAY, WARNING)

Wei was blunt: "CLI-only fallback" means the product loses its primary feature when Bridge crashes — that is describing the failure, not mitigating it. Priya brought a concrete scenario: a developer who uses Fastmail has to get a new Proton account, a paid plan, and a separate desktop app just to try Beadle. Dana identified the narrative gap: the document says "standard IMAP/SMTP" in the Dependencies FAQ but "Proton-only" in the Won't Do list — the abstraction exists but is artificially capped, and the document never explains why. Alex asked the strategic question: is this a product that requires Proton, or a product that uses email and starts with Proton? Unanimous: defend Proton as a trust model choice (E2E encryption aligns with the threat model), acknowledge the IMAP/SMTP abstraction, and be honest that multi-provider is a scope decision not an architectural constraint.

### 6. TAM — Honest Niche or Methodology Showcase? (TWO-WAY, SUGGESTION)

Wei mapped the structural problem: "methodology showcase" appears in four contexts doing three different jobs — it is simultaneously the fallback position (TAM FAQ), the opportunity cost mitigation (P&L FAQ), the named failure mode (pre-mortem), and the viability risk mitigation (risk assessment). Using the same phrase as both the success condition and the safety net undermines both. Dana identified the precise defect: one sentence in the TAM FAQ ("If that niche is too small, the product still serves its secondary purpose as a methodology showcase") frames the showcase as a consolation prize, contradicting the Revenue FAQ where it is listed as a co-equal strategic value. Priya observed that from a customer's perspective, if the author's answer to "what if nobody uses it" is "that's fine," the pitch is undermined. Unanimous: remove the conditional hedge from the TAM FAQ, keep the showcase in the Revenue FAQ as honest disclosure, and keep it in the pre-mortem as the failure mode name.

### 7. 300-500 Hours Opportunity Cost (TWO-WAY, SUGGESTION)

Wei and Priya both noted the 67% variance (300 to 500 hours) has no derivable basis — the timeline gives month-ranges but no per-phase hour estimates. A reader cannot pressure-test the number. Alex elevated the severity: the opportunity cost mitigation ("methodology showcase") was already flagged in Hot Spot #6, leaving the viability risk effectively unmitigated. Dana brought the builder's perspective: the existing PoC already covers significant Phase 0 scope (GPG signing, single command execution, Proton Bridge delivery), so the estimate may be high if Phase 0 extends the PoC rather than building from scratch. Unanimous: add per-phase hour estimates, account for PoC acceleration, and replace the "methodology showcase" mitigation with a concrete kill switch condition.

---

## Revision Queue

These directives are written to work as `/prfaq:feedback` input:

1. **GPG ownership:** Remove the `age` fallback from the Tech Risks FAQ — age has no signing primitive. Add SSH signing (used by GitHub, GitLab) as an evaluated alternative with an honest reason it was rejected. Rewrite the GPG abstraction mitigation from "invest heavily in init and sign" to a specific design description of what `beadle init` must do. Update the risk assessment to reflect that GPG abstraction is the hardest unsolved UX problem, not just a "mitigation target."

2. **Headline reframe:** Change the headline from "via Email" to leading with the trust model (cryptographic owner control, signed audit trail). Demote email to one delivery mechanism in the lede. Ensure the headline, lede, Getting Started, and pre-mortem tell a consistent story about what Beadle's primary value is.

3. **Competitive moat restructure:** Replace the "12-18 months" concession in the Competitive FAQ with the structural argument: cloud providers cannot replicate local-only, owner-controlled execution without contradicting their business model. Reframe the competitor table gaps using trust model language (not capability gaps). Remove the time-bounded prediction.

4. **Next Step FAQ rewrite:** Rewrite faq:next-step to start from the actual PoC state (GPG signing, single command execution, Proton Bridge delivery all validated). The next step is: generalize to multi-command pipelines with `beadle init`, then test with 5-10 external developers. Do not describe building things that already exist.

5. **Proton Bridge defense:** In the Tech Risks FAQ, explain *why* Proton specifically (E2E encryption aligns with the trust model's threat model). Acknowledge that Beadle uses standard IMAP/SMTP and multi-provider support is a scope decision, not an architectural constraint. Replace "CLI-only fallback" with an honest operational mitigation (restart policy, healthcheck interval, alerting). Move "Multi-provider email support" from Won't Do to Should Do or add a note that it is a Phase 0 scope constraint.

6. **TAM FAQ tightening:** Remove "If that niche is too small, the product still serves its secondary purpose as a methodology showcase" from the TAM FAQ. The TAM section's job is to make the case for the niche, not hedge against its failure. Keep "methodology showcase" in the Revenue FAQ (honest strategic disclosure) and the pre-mortem (named failure mode). Do not use the same phrase as both the success condition and the safety net.

7. **P&L hour breakdown:** Add per-phase hour estimates to the timeline or P&L FAQ so the 300-500 range has a derivable basis. Account for PoC work that accelerates Phase 0. Replace the "methodology showcase" opportunity cost mitigation with a concrete kill switch: specific metrics at specific time points that trigger archival mode.
