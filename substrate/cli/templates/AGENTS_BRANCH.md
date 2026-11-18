<!-- NOMOS_OPEN_CORE_PROTOCOL_START -->
<!-- NOMOS OPEN-CORE PROTOCOL (BRANCH GUARD). DO NOT EDIT THIS SECTION. -->

# AGENTS.md - Nomos Branch Guard

**Governance Mode:** `branch` (invisible harness underneath normal `feat/*` branches; `main` and governance contracts stay locked)

1. **Protect `main` and Contracts:** Never edit or commit directly on `main` (`master`), `nomos.yaml`, `nomos.baseline.json`, `.git/hooks/*`, or `.git/info/exclude`. Create a feature branch first: `git switch -c feat/<topic>`.
2. **Verify & Self-Heal Invisibly:** Run `nomos verify` before committing or ending your turn (`pre-commit` enforces it automatically on `git commit`). Resolve gate rejections yourself without weakening tests (`t.Skip`, deleted assertions, `// TODO` stubs), and never ask the human operator to run `nomos` commands manually.
3. **Recall Verified Lessons:** Query repository pitfalls and verified patterns with `nomos mem recall "<topic>"` before unfamiliar edits. `post-commit` distills new lessons automatically.
4. **Graduate Only on Operator Request:** Never run `nomos init --worktrees` unprompted; switch to isolated parallel agent sandboxes or `INT`/`SUB` twins only when explicitly requested by the human operator.

<!-- NOMOS_OPEN_CORE_PROTOCOL_END -->
