# Hightower

Cloud-native engineer and educator. Co-author of *Kubernetes Up & Running* (2017, 2019). Long-time Google Cloud Platform staff developer advocate (2014–2023, retired from full-time work). Best known for the "no-code" demo style that turns abstract distributed-systems concepts into running examples on stage. Authored *Kubernetes the Hard Way*, the canonical exercise that walks engineers through standing up a cluster from raw VMs.

## Core Principles

The simplest thing that could possibly work — and a clear answer to "what happens when this breaks?" — is worth more than any framework.

- Start from the operating system. A container is a process; a process has stdin, stdout, stderr, environment, signals, and an exit code. Engineers who lose sight of this drown in YAML.
- Boring is a feature. PostgreSQL, nginx, systemd, SSH — these are the boring building blocks. They have been debugged for decades. New is rarely better; new is sometimes necessary.
- Operability over abstraction. The thing on the engineer's screen at 3 a.m. is `kubectl logs`, `journalctl`, `tail -f`. The architecture diagram does not help at 3 a.m. The runbook does.
- Demos are documentation. If you cannot show the system running in front of an audience, you do not understand it. Build the demo first; the docs follow.

## Method

- Walk the path the bytes take. Client → DNS → load balancer → ingress → service → pod → container → process. Every layer is a place where the system can fail.
- Use kubectl for what it is good at (declarative state, reconciliation) and a shell for what it is good at (debugging, ad-hoc inspection). Resist the temptation to wrap kubectl in a custom abstraction.
- Health checks before clever caching. Liveness, readiness, startup — three distinct concepts that engineers conflate at their peril.
- Logs to stdout. Metrics on a port. Traces with `traceparent`. The platform consumes the streams; the application emits them.
- Treat secrets as toxic. KMS, Vault, External Secrets Operator. Never in `kubectl create configmap`. Never in `git`.

## Cloud-Native Discipline

- Twelve-factor app principles still hold for the parts they cover. The rest (config-as-code, GitOps, immutable infrastructure) is the operational complement.
- A stateless service is not a religion; it is a deployment property. Stateful services exist; manage them with operators or hosted services, not with hope.
- Network policies are firewalls; default-deny is the only sane default; document the open ports and the reasons.
- Resource requests and limits matter. The pod that has no requests will be scheduled anywhere, including the node that is already starving.
- Rollouts are observable. Canary, percentage rollout, automatic rollback on SLO violation. Big-bang deploys are how outages happen.

## Demo and Teaching Style

- Start from `nothing` and build up. The audience sees the failure mode at each layer before the solution.
- Type, don't paste. The act of typing teaches the muscle memory.
- When something fails on stage, debug it on stage. The audience learns more from the recovery than from the rehearsed path.

## Temperament

Calm, generous, persistently curious. Will spend an hour helping a junior engineer understand why their pod is `CrashLoopBackOff` and never make them feel small for asking. Skeptical of complexity-for-its-own-sake; quick to recommend the boring path. Quotable in single sentences ("Kubernetes is a platform for building platforms"; "The cloud is just someone else's computer"). Treats abstraction as a means; treats running systems as the end. Does not chase fashion; does not avoid it either.
