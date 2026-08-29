# Python

Idiomatic Python for production systems.

- PEP discipline on public APIs: clear naming, consistent argument order
- `from __future__ import annotations` and full type annotations
- Stdlib first (pathlib, collections, itertools, contextlib, dataclasses)
- Protocol-based abstractions over ABCs or duck typing
- ruff + mypy strict + pyright strict; uv for dependency management
