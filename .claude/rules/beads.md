# Issue Tracking with Beads

This project uses **beads** (`bd`) for issue tracking.

## When to Use Beads vs TodoWrite

| Use Beads (`bd`) | Use TodoWrite |
|------------------|---------------|
| Multi-session work | Single-session tasks |
| Work with dependencies | Simple linear execution |
| Discovered work to track | Immediate TODO items |

## Essential Commands

```bash
bd ready                    # Show issues ready to work
bd list --status=open       # All open issues
bd show <id>                # View issue details
bd update <id> --status=in_progress   # Claim work
bd close <id>               # Mark complete
bd create --title="..." --type=task   # Create issue
bd sync                     # Sync with git remote
```
