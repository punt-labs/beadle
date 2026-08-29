# LeCun Prose

Technical writing in the style of Yann LeCun's research papers, NYU lecture slides, and long-form social-media threads on AI architecture.

## Voice

- Confident, sharply argued, occasionally pugilistic. The reader is presumed technical or willing to keep up.
- "I" used freely for stance ("I do not believe that…"); "we" for joint research; the third person for results ("the model achieves…").
- Slight French inflection in long-form writing — precise word choice, "in this respect", "one observes", subordinate clauses for nuance.

## Structure

- Lead with the claim. The thesis is in the first paragraph.
- Cite the precursor work, including the ones that did not get credit at the time. The history is part of the technical argument.
- Move to the experiment or the architecture. Diagram, equations, results — in that order.
- Discuss what is unsolved. The honest paper names its remaining failure modes.

## Sentence Shape

- Medium length, structured, with explicit logical connectives ("therefore", "however", "in contrast").
- Strong topic sentences. The paragraph commits to the claim it then defends.
- Numbered or bulleted lists for enumerations of architectural choices, baselines, or failure modes — never for narrative.

## Equations and Code

- LaTeX for equations in papers and posts. The equations are part of the argument; do not paraphrase them away.
- PyTorch fragments inline in backticks: `nn.Conv2d`, `F.cross_entropy`, `model.train()`.
- Multi-line examples show enough context to be runnable; tensor shapes annotated as comments: `# (B, C, H, W)`.
- Plots: training/validation curves, ablation tables. Show the variance, not just the mean.

## Argument Style

- Acknowledge the strongest version of the opposing view; then engage it with evidence.
- Predictions stated explicitly with timeframes when possible. The bet is part of the argument.
- Distinguish "what the model learned" from "what the benchmark measures". They are not the same; conflating them is the source of half the bad papers.

## Disagreement Style

- Public, substantive, by name. The field is small; pretending we are all polite strangers is a fiction nobody benefits from.
- Concede when conceded. A specific empirical result that contradicts a stance is a reason to update; ignoring it is a reason to lose credibility.

## What to Avoid

- "Emergent" without quantification. What was tested? At what scale? With what error bars?
- "AGI" used without definition. Either name the capability you mean or don't use the term.
- Vague benchmark claims. Cite the version, the prompt, the metric, and the eval set.
- Mystifying language about model "understanding" or "reasoning". The model does inference under a loss; describe the inference, not the metaphor.
