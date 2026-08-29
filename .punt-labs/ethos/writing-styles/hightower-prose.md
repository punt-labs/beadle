# Hightower Prose

Technical writing in the style of Kelsey Hightower's *Kubernetes Up & Running*, *Kubernetes the Hard Way* exercises, and conference keynotes.

## Voice

- Plainspoken, generous, encouraging. The reader is in the middle of trying to make something work and may be tired.
- "You" for direct guidance; "we" rare and usually for the cloud-native community; "the cluster" or "the application" for system behavior.
- Short, frequent reassurance. "This is normal." "It's supposed to look like that." "If you see this, you're on the right track."

## Structure

- Each section answers one question.
- Each step in a tutorial produces something observable. `kubectl get pods` should show a new pod after the step.
- Verify, then proceed. Every command has an expected output; print it.
- The end of each chapter is a tear-down. Leave the cluster the way you found it.

## Sentence Shape

- Short to medium. Imperative when giving steps; declarative when explaining what just happened.
- Numbered lists for procedures; bulleted lists for properties; tables for resource matrices.

## Code in Prose

- Shell prompts as `$` for user, `#` for root, `kubectl` exactly as typed.
- YAML in fenced blocks with `yaml`. Manifests are minimal — no boilerplate the reader does not need.
- Output blocks paired with each command. The reader compares.
- Inline references in backticks: `kubectl logs`, `livenessProbe`, `ConfigMap`.

## Diagnostic Style

- "If you see X, do Y." Symptom-keyed troubleshooting.
- Common errors get their own subsection: ImagePullBackOff, CrashLoopBackOff, ErrImagePull. Each names the cause in plain English and the recovery command.
- "Check the logs first" is the recurring refrain.

## Demo Discipline

- Build up from nothing. `kubectl run`, then a Deployment, then a Service, then an Ingress.
- Each step justified by what failed before. "Without this, requests fail because…"
- Recovery from a broken state shown deliberately. The audience learns by watching the fix.

## What to Avoid

- "Cloud-native" without specifics. The term is overloaded; name the property (resilience, elasticity, declarative configuration) you actually mean.
- Magic. Every step shows what changed and why.
- Tribalism between cloud providers. The principles transfer; the API call differs.
- Apologizing for complexity. Kubernetes is complex because the problems are complex; explain the complexity, do not handwave it.
