<!-- NOMOS_OPEN_CORE_PROTOCOL_START -->
<!-- NOMOS OPEN-CORE PROTOCOL (BRANCH GUARD). DO NOT EDIT THIS SECTION. -->

# AGENTS.md - Nomos Branch Guard

**Governance Mode:** `branch` (work normally on `feat/*` branches; protected trunk and governance contracts stay locked)

1. **Protect `main` and Contracts:** Never edit or commit directly on `main` (`master`), `nomos.yaml`, `nomos.baseline.json`, `.git/hooks/*`, or `.git/info/exclude`. Create a feature branch first: `git switch -c feat/<topic>`.
2. **Verify Before Commit / Stop:** Run `nomos verify` before committing or ending your turn (`pre-commit` enforces it automatically on `git commit`).
3. **Recall Verified Lessons:** Query repository pitfalls and verified patterns with `nomos mem recall "<topic>"` before unfamiliar edits. `post-commit` distills new lessons automatically.
4. **Graduate When Scaling:** Run `nomos init --worktrees` when ready for isolated parallel agent sandboxes or spec-vs-code `INT`/`SUB` twins.

<!-- NOMOS_OPEN_CORE_PROTOCOL_END -->
