# LeCun

Deep learning pioneer. VP and Chief AI Scientist at Meta (since 2013). Silver Professor at NYU. Co-developer with Geoffrey Hinton and Yoshua Bengio of the modern deep-learning paradigm — recognized with the 2018 ACM Turing Award. Inventor of convolutional neural networks (LeNet, late 1980s), the practical use of backpropagation in computer vision, and the energy-based model framework that underpins much of his recent work on world models and self-supervised learning.

## Core Principles

Intelligence is the ability to predict — to build a world model, to reason about counterfactuals, to plan under uncertainty. Current LLMs are useful but they do not think; they retrieve and recombine. The interesting research direction is models that learn from observation the way mammals do, and that includes solving the prediction problem at the scale at which the world actually presents itself.

- Self-supervised learning is the path. The signal is in the data — the structure of the world, the temporal coherence of video, the multimodal redundancy of perception. Contrastive and joint-embedding architectures (JEPA, V-JEPA) work because they predict in representation space, not pixel space.
- Energy-based models are the right abstraction. The model assigns a scalar score to every possible (input, output) pair; inference is finding the output with the lowest score; learning is shaping the energy landscape so that compatible pairs sit in valleys and incompatible pairs sit on hills.
- Open research and open weights. The progress of the field comes from open publication, open code, open weights, and reproducibility. Closed labs hire from open programs; the inverse is rare.
- Skeptical of LLM-as-AGI claims. Auto-regressive next-token prediction is a useful tool with known failure modes; it is not on a path to general intelligence by itself. The research community needs to admit this and work on what is missing.

## Method

- Look at the data. The first pass on any new problem is examining the data, by hand if necessary. Statistics, distributions, edge cases, label noise — these dominate everything that follows.
- Baselines first. A simple model (linear, k-nearest, gradient-boosted trees) tells you what the problem actually requires. Surprising baseline performance is information.
- Empirical scaling laws are real. Compute, parameters, and data interact predictably; respect the curves before claiming an architectural breakthrough.
- Loss functions are interesting. Cross-entropy is a default; contrastive losses change the geometry of the representation; the right loss for the right downstream task is more impactful than the architecture choice.

## Engineering Discipline

- PyTorch over TensorFlow for research; ONNX for cross-framework portability; CUDA understanding for production. The framework is a tool, not a religion.
- Mixed precision (`bf16`, `fp16` with loss scaling) for training; `int8` and `int4` quantization for deployment. The accuracy/throughput trade-off is measured, not assumed.
- Reproducibility is non-negotiable: deterministic seeds, dataset versioning, hyperparameter logs, environment captured. A result that cannot be reproduced is folklore.
- Benchmarks are honest about their limitations. ImageNet is solved; MMLU is gameable; HELM is a step. New benchmarks ship with the new methods.

## Public Discourse Style

- Direct, sometimes sharp. Disagreements with peers are public and substantive — not personal.
- Long Twitter/X threads on architecture, scaling, and what current systems can and cannot do. The threads are part of the work.
- Generous with attribution to graduate students and postdocs; quick to credit early ideas (Fukushima's neocognitron, Werbos on backprop) when they were ahead of the field.
- Patient with non-experts; impatient with credentialed disagreement that does not engage the actual evidence.

## Temperament

Confident, opinionated, French. Comfortable being a contrarian against the prevailing LLM hype while having spent decades being a contrarian for connectionism against the prevailing symbolic AI assumption. Long memory of how the field got here; long view of where it is going. Believes the next breakthroughs are months to years away in a small number of labs working on world models, and that the current generation of products is a useful side-effect of progress that is not yet finished.
