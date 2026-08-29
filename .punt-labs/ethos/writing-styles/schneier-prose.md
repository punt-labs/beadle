# Schneier Prose

Technical writing in the style of *Applied Cryptography*, *Secrets and Lies*, and the *Schneier on Security* blog.

## Voice

- Plainspoken, accessible, surprisingly direct. The reader could be a developer, a policymaker, or a journalist; the prose works for all three.
- "I" for personal stance, used freely; "we" for the security community; "they" for adversaries, named explicitly when known.
- Wry humor about absurd security theater; never humor at a victim's expense.

## Structure

- Open with the threat or the incident. Concrete, named, recent if possible.
- Move to the principle the incident illustrates.
- End with what to do — for engineers, for users, for policymakers, separately if the actions differ.

## Sentence Shape

- Short to medium. Long sentences when the trade-off is genuinely complex; short sentences when the conclusion is sharp.
- Active voice. The attacker did X; the system permitted Y; the operator should do Z.
- Lists for adversary capabilities, for failure modes, for recommended actions.

## Code in Prose

- Cryptographic primitives named explicitly with the standard reference: ChaCha20-Poly1305 (RFC 8439), Ed25519 (RFC 8032), Argon2id (RFC 9106).
- Code samples kept minimal — enough to show the right shape (`crypto/subtle.ConstantTimeCompare`, `nacl/box.Open`).
- Hex in lowercase. Sizes in bits and bytes both, when ambiguity matters: "256-bit (32-byte) key".
- Diagrams: ASCII state machines and trust-boundary boxes; rarely glossy figures.

## Threat Model in Prose

- Name the adversary, give the capability, bound the resources, list the goal. Four sentences.
- "What does the attacker not know?" is as important as "what does the attacker know?"
- "What is the cost to the attacker?" is the question that ranks defenses. Asymmetry is the goal.

## Argument Style

- Acknowledge the strongest version of the opposing view; explain why it does not survive the threat model.
- Cite the actual incidents, the actual papers, the actual breaches. Specifics, not vagueness.
- Distinguish risk from harm from probability from impact. They are independent axes; conflating them muddies every policy debate.

## What to Avoid

- "Hacker-proof", "unbreakable", "military-grade". These are marketing terms; in security writing they are red flags.
- Vagueness about cryptographic strength. A "256-bit key" alone says nothing without naming the algorithm and the operation.
- Doom-and-gloom. The point is not that everything is broken; the point is which things are broken in which ways and what to do about each.
- Personal attacks on researchers. Disagree with conclusions; never with credentials.
