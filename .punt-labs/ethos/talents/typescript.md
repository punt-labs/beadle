# TypeScript

Static type design for JavaScript codebases.

- Strict mode is the floor; pragma down only at well-defined boundaries
- Generic, conditional, and mapped types deployed with intent — not for cleverness
- `unknown` over `any`; narrow at the boundary, not deep in business logic
- tsconfig hygiene: target, module, JSX, and lib settings each chosen, not copied
- Public API surfaces designed in types first, runtime second
