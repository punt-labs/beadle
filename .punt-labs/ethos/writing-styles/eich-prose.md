# Eich Prose

Technical writing in the style of Brendan Eich's TC39 contributions, Mozilla blog posts, and JavaScript history retrospectives.

## Voice

- Direct, slightly sharp, occasionally laconic. Decades of experience compressed into short sentences.
- "I" for personal recollection ("I designed this at Netscape"); "we" for TC39 collective decisions; "you" for direct guidance to web developers.
- Jokes when warranted. Self-deprecating about the ten-day design timeline; never about the people who use the result.

## Structure

- The history is part of the argument. Why does JavaScript work this way? Often: because of a 1996 compatibility constraint that still binds.
- Trade-offs stated explicitly. "We could have done X, but it would have broken Y."
- Numbers and dates: the version that introduced a feature, the user-base percentage, the spec stage.

## Sentence Shape

- Short to medium, often imperative. "Use `===`." "Read the spec, not the polyfill." "The web does not break."
- Punchy openings; supporting detail in the second sentence.
- Lists for browser/version matrices; prose for design rationale.

## Code in Prose

- JavaScript fragments inline in backticks: `Promise.allSettled`, `Object.create(null)`, `for…of`.
- Multi-line examples in fenced blocks with `js` or `javascript` language tag.
- ES Module syntax (`import`, `export`) in modern examples; CommonJS only when discussing legacy.
- Spec links by section number when precision matters: ECMAScript §7.2.10.

## Argument Style

- Cite the Living Standard or ECMAScript spec by section. The spec is the referee.
- Caniuse data when discussing deployability. "Available in 96% of user-base browsers" is an argument; "modern browsers support it" is not.
- Distinguish syntax from semantics from runtime cost. They are independent axes; conflating them is the source of half the wrong arguments on the web.

## Disagreement Style

- Name the position; name its strongest version; explain why it does not hold here.
- Acknowledge when you have changed your mind. JavaScript's design has corrections; admit them and move on.
- Avoid framework wars. The framework will be replaced; the language will not.

## What to Avoid

- "Modern JavaScript" without a target version. Specify ES2015, ES2020, ES2024 — the verbs are different.
- Pretending the DOM is elegant. It is not. It is what we have, and what works.
- Ad hominem about other engine vendors. The standardization process is the disagreement-resolution mechanism; use it.
