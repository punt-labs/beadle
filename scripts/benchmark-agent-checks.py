#!/usr/bin/env python3
"""Compare the pre-DES-085 adb hook with the current advisory hook."""

import json
import os
from pathlib import Path
import statistics
import subprocess
import time


root = Path(__file__).resolve().parents[1]
os.chdir(root)
env = dict(os.environ, CLAUDE_PROJECT_DIR=str(root), GIT_CONFIG_GLOBAL="/dev/null")
baseline = subprocess.check_output(
    ["git", "show", "origin/main:.claude/agents/adb.md"], text=True
)
old = json.loads(next(line.strip().removeprefix("command: ")
                      for line in baseline.splitlines()
                      if line.strip().startswith("command: ")))
commands = {"old": ["sh", "-c", old],
            "new": ["ethos", "hook", "post-edit", "--agent", "adb"]}
for suffix, path in [("go", "internal/mcp/tools.go"), ("md", "README.md"),
                     ("sh", "install.sh")]:
    payload = json.dumps({"session_id": "benchmark-advisory", "tool_name": "Edit",
                          "cwd": str(root),
                          "tool_input": {"file_path": str(root / path)}})
    for label, command in commands.items():
        samples = []
        for run in range(6):
            start = time.perf_counter()
            result = subprocess.run(command, input=payload, text=True, env=env,
                                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                    check=False)
            elapsed = time.perf_counter() - start
            log = root / ".tmp" / f"benchmark-{suffix}-{label}-{run}.log"
            log.write_text(result.stdout)
            print(f"{suffix} {label} run={run} seconds={elapsed:.3f} "
                  f"exit={result.returncode} log={log}", flush=True)
            if result.returncode:
                raise SystemExit(result.returncode)
            if run >= 1:
                samples.append(elapsed)
        print(f"RESULT {suffix} {label} median={statistics.median(samples):.3f} "
              f"min={min(samples):.3f} max={max(samples):.3f}", flush=True)
