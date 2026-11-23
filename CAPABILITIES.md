---
authors:
  - Mark Gantlett
  - Nomos Open-Core Substrate
classification: public
date: "2026-11-22"
plane: intent
ssot_repository: nomos
status: active
title: "Page 0: Nomos Open-Core Complete Capabilities & Subsystem Map"
type: spec
---

# Page 0: Nomos Open-Core Complete Capabilities & Subsystem Map

**Canonical SSoT Reference (`Apache 2.0`, `CGO_ENABLED=0`, `0` LLM Judge Tokens)** · [**Developer Docs Portal (`sophialabs.ca/docs/specs/capabilities`)**](https://sophialabs.ca/docs/specs/capabilities) · [**Layer 0 Charter (`intent/intent.md`)**](intent/intent.md)

---

## 1. Purpose & End-to-End Subsystem Topology

While [`README.md`](README.md) focuses on the 30-second Day-0 onboarding path (`nomos xray .` -> `nomos init`), this **Page 0 Capabilities & Subsystem Map** serves as the single exhaustive engineering catalog for developers and visiting AI agents. Every single-repository governance capability below ships locally inside the `~20 MB` static Go `nomos` binary (`66` compiled capability descriptors: `8` architectural engines, `16` CLI root commands, `5` ambient lifecycle hook interceptors, `16` embedded Just-In-Time playbooks, and `7` Laws x `3` Defense Rings = `21` Invariant Cells = `42` deterministic quality gate probes).

```mermaid
flowchart TD
    subgraph IntentPlane["Intent Plane (Human + Spec SSoT)"]
        L0["Layer 0: North Star Charter<br/>(intent/intent.md)"]
        L1["Layer 1: Executable Physics<br/>(nomos.yaml + nomos.schema.json + nomos.baseline.json)"]
        L2["Layer 2: 8 Domain Specs & 16 JIT Directives<br/>(NCTE-100 + Go Types + TC-* Vectors)"]
        L0 --> L1 --> L2
    end

    subgraph AgentBoundary["Indirect Control Airlock (Tier 1 Coding Agents)"]
        Boot["SessionStart / Handshake / MCP<br/>(nomos handshake · nomos mcp · AGENTS.md)"]
        Pre["PreToolUse Airlock (~4ms)<br/>(CapBAC Lease · CapabilityFlowGraph IFC · Egress/Cred/MCP Guard)"]
        Post["PostToolUse Radar (~15-24ms)<br/>(Wazero Tree-sitter · HotScopeReflex · Loop & Budget Pacing)"]
        Stop["Stop Anti-Phantom Gate<br/>(Blocks unverified 'tests pass' claims)"]
        Boot --> Pre --> Post --> Stop
    end

    subgraph ExecutionCore["Substrate Execution & Verification Core"]
        WT["Workspace Isolation<br/>(Branch Guard feat/* OR Hermetic worktrees/ + SafeGitCommand)"]
        MTPO["Software Physics & MTPO<br/>(FileMass · Viscosity · Export/Import CCD · Serializability Seq)"]
        DoD["42-Gate DoD Pipeline & Mutation Oracle<br/>(7 Laws x 3 Rings = 21 Cells · --mutate-diff · SARIF · PRProof)"]
        WT --> MTPO --> DoD
    end

    subgraph Persistence["Transactional Persistence & Learning Loop"]
        Ledger["SQLite Living Ledger<br/>(TaskRecord FSM · Causal DAG · $ref Batch)"]
        Notes["2PC Git Notes Receipts<br/>(refs/notes/nomos/releases · SHA-256 + SpecDigests)"]
        Mneme["Nomos Mneme (mneme.db)<br/>(64-dim Subword Vectors · Pitfall/Pattern Distillation · Axiom Consolidation)"]
        Session["Session & Sentinel PRM<br/>(7-Stage Trajectory · SentinelScorecard · RLVR/DPO/SFT JSONL Export)"]
    end

    L1 --> Pre
    L2 --> Boot
    L2 --> DoD
    Stop --> WT
    Ledger <--> WT
    DoD -->|"Pass 42/42"| Notes
    DoD -->|"Fail -> GateTrapEntry"| Mneme
    Notes -->|"Receipt-Gated Mint"| Mneme
    Mneme -->|"Scoped Recall & HotScopeReflex"| Boot
    Mneme -->|"Sensitize 80% -> 65%"| Post
    Notes --> Session
    Mneme --> Session
```

---

## 2. Index of the 8 Layer 2 Domain Specifications

Every compiled package in `substrate/` maps 1-to-1 onto one of the **8 Canonical Layer 2 Domain Specifications**:

| Domain # | Specification | Substrate Packages | Core Responsibility & Connections |
| :---: | :--- | :--- | :--- |
| **01** | [`go_ssot_and_json_schema_spec.md`](intent/architecture/specs/go_ssot_and_json_schema_spec.md) | `core/config`, `core/types` | Compiles `EcosystemConfig` as the single Go SSoT, synthesizes Draft 2020-12 [`nomos.schema.json`](nomos.schema.json), and formats HATEOAS `NomosError` fix recipes. |
| **02** | [`nomos_hermetic_worktree_spec.md`](intent/architecture/specs/nomos_hermetic_worktree_spec.md) | `core/workspace`, `core/capbac`, `cli/hookcmd` | Governs Branch Guard (`feat/*`), isolated `worktrees/<repo>-<KEY>` sandboxes, `SafeGitCommand`, `CapBACLease` step budgets, `CapabilityFlowGraph` IFC taint tracking, and PreToolUse/Stop guards. |
| **03** | [`task_lifecycle_cli_spec.md`](intent/architecture/specs/task_lifecycle_cli_spec.md) | `modules/task`, `core/storage`, `cli/*` | Implements the pure-Go SQLite WAL Living Ledger (`IDLE -> PLAN -> EDIT -> REVIEW -> SYNC -> LEARN`), `$ref` JSON batch DAGs, atomic 2PC trunk fast-forwards, Git Notes receipts, `nomos mcp`, `nomos issue`, and `nomos update`. |
| **04** | [`nomos_intent_traceability_spec.md`](intent/architecture/specs/nomos_intent_traceability_spec.md) | `modules/verify/*`, `intentlint`, `wiredrift`, `prproof` | Enforces the `7x3=21` Invariant Cell / `42`-gate DoD matrix, `@intent` & `@verifies TC-*` 11-language obligation bindings, bijective struct parity, `architecture_boundaries`, `--mutate-diff`, and `PRProofReceipt` sealing. |
| **05** | [`tree_sitter_ast_gate_spec.md`](intent/architecture/specs/tree_sitter_ast_gate_spec.md) | `modules/verify/treesitter`, `xray`, `ratchet` | Runs zero-CGO 11-language AST analysis (Go, TS/JS, Svelte, Python, Rust, Ruby, C/C++, Kotlin, plus pure-Go `wazero` WASM for Java and C#), `nomos xray` scoring, Type-2 clone hashing, and `nomos.baseline.json` ratcheting. |
| **06** | [`mneme_memory_spec.md`](intent/architecture/specs/mneme_memory_spec.md) | `modules/mem` | Provides pure-Go SQLite episodic memory (`mneme.db`) with 64-dim SHA-256 subword feature hashing, Ebbinghaus decay, receipt-gated `pitfall`/`pattern` distillation, `ConsolidateConventionAxioms`, and `HotScopeReflex`. |
| **07** | [`nomos_controlled_technical_english_spec.md`](intent/architecture/specs/nomos_controlled_technical_english_spec.md) | `modules/ncte`, `modules/handshake` | Enforces `NCTE-100` unambiguous prose rules (`IntentSteelmanGate`, `nomos directive --lint`), the 36-term canonical action lexicon, and 16 embedded phase-routed JIT playbooks (`nomos handshake`). |
| **08** | [`software_physics_engine_spec.md`](intent/architecture/specs/software_physics_engine_spec.md) | `cli`, `modules/mtpo`, `modules/session`, `modules/telemetry` | Computes AST File Mass $M(f)$, Shannon/Rabin-Karp viscosity $\mu(f)$, Continuous Collision Detection (`GREEN`/`YELLOW`/`RED`), `MTPO` serializability, `SentinelScorecard` PRM scoring, and RLVR/DPO/SFT `nomos session export`. |

---

## 3. Complete 16-Command Canonical CLI Surface & Subcommands

Nomos exposes a **2-tier CLI surface** with zero command aliases (`ZeroAliasGate`, Law 6):
- `nomos --help` displays the **9 everyday developer commands** (`init`, `status`, `task`, `verify`, `xray`, `mem`, `issue`, `update`, `version`) plus shell `completion`.
- `nomos --help-agent` reveals the **7 internal agent lifecycle & RPC commands** (`capabilities`, `directive`, `handshake`, `hook`, `mcp`, `physics`, `session`).
- **Extensible Plugin Boundary:** Invoking any unknown root command `nomos <name>` resolves `nomos-plugin-<name>` on `PATH` with structured `NomosError` guidance, keeping Open-Core self-contained while allowing external extensions to mount cleanly.

### 3.1 Setup, Diagnostics & System Commands

| Command | Flags & Options | Purpose & Subsystem Connections |
| :--- | :--- | :--- |
| **`nomos xray [dir]`** | `--markdown`, `--json`, `--sarif`, `--top <N>` (default `10`) | Zero-init, zero-write `git ls-files` scan across 11 languages (`~1.5s-4.3s` even on `3.60M` LOC). Computes `AI Hangover Score` (`0-100`, `Grade A-F`), Type-2 AST clones, `>400 LOC` monoliths, complex/high-arity functions, hollow stubs, skipped tests, swallowed errors, secret/path leaks, CWE sinks, and ready-to-lock `architecture_boundaries`. |
| **`nomos init`** | `--local`, `--ci`, `--worktrees`, `--strict`, `--baseline`, `--agents <claude,cursor,opencode,gemini,agy,all>`, `-d <dir>` | Scaffolds [`nomos.yaml`](nomos.yaml) (`isolation_mode: "branch"` by default, or `"worktree"` with `--worktrees`), snapshots Day-0 brownfield bounds into `nomos.baseline.json` (`strict_mode: false` unless `--strict`), registers stealth trials in `.git/info/exclude` (`--local`), generates offline GitHub Actions SARIF + GitLab CI pipelines (`--ci`), scaffolds compact `AGENTS.md` / `CLAUDE.md` blocks, and installs native agent + Git hooks. |
| **`nomos status`** | `-A, --all-projects`, `--json` | Unified operational dashboard reporting active lifecycle phase, branch/worktree state, `CapBACLease` step consumption, SQLite health, stale `SpecDigests` obligations, HATEOAS `Next Actions`, and unblocked DAG wavefront. |
| **`nomos issue [title]`** | `-b, --body`, `-o, --output`, `--dry-run`, `--clear`, `-w, --web`, `-y, --yes` | Reads the local substrate anomaly ring buffer, scrubs all workstation paths, private branch names, and credentials, and writes an auditable Markdown report (`.tmp/nomos-diagnostic.md`). |
| **`nomos update`** | `--check`, `--sync-workspace`, `--version <tag>`, `-d <dir>`, `--json` | Verifies pure-Go SHA-256 `checksums.txt` digests, enforces package-manager ownership guards (Homebrew, AUR, Nix) to prevent clobbering managed installs, atomically swaps standalone binaries, and synchronizes downstream hooks, `AGENTS.md`, and `nomos.schema.json` (`--sync-workspace`). |
| **`nomos version`** | `--json` | Emits compiled Open-Core SemVer version, OS/arch target, and Go toolchain provenance. |
| **`nomos completion`** | `bash \| zsh \| fish \| powershell` | Generates native shell tab-completion scripts. |

### 3.2 Verification, Software Physics & Quality Attestation Commands

| Command | Flags & Options | Purpose & Subsystem Connections |
| :--- | :--- | :--- |
| **`nomos verify [path]`** | `-A, --all`, `--strict`, `--fast-file <file>`, `--impact`, `--pr`, `--pr-output <file>`, `--mutate-diff`, `--base-ref <ref>`, `--range <rev..rev>`, `--sarif`, `--baseline`, `-w, --worktree`, `--json` | Evaluates all `42` DoD gates in a bounded 6-goroutine pool (`~361ms` diff verify on `3.60M` LOC; `~24ms` `--fast-file` single-file radar). Supports 1-hop AST reverse-dependency blast radius (`--impact`), zero-token shadow AST micro-mutation probing across Go, TS/JS, and Python (`--mutate-diff`), SHA-256 sealed PR proof tables (`--pr`), pre-push commit-range audits (`--range`), and OASIS SARIF 2.1.0 (`--sarif`). |
| **`nomos verify proof [path]`** | `--json` | Runs the **Three Mathematical Gate Integrity Vectors** across local or external codebases: (1) Synthetic AST Defect Mutation Sensitivity (`MutationScore == 100%`), (2) Clean Reference Metamorphic Invariance (`FalsePositiveRate == 0%`), and (3) AST Anti-Vacuity Audit across all 42 `Evaluate()` methods. |
| **`nomos physics`** | `--json` | Computes real-time AST File Mass $M(f)$, Shannon/Rabin-Karp viscosity, `WorktreeHull` exported/imported symbol envelopes, and Continuous Collision Detection (`GREEN`, `YELLOW`, `RED`) across concurrent active worktrees. |

### 3.3 Living Ledger Task & 2PC Lifecycle Commands (`nomos task` - All 15 Subcommands)

| Subcommand | Key Flags | Lifecycle Role & State Transition |
| :--- | :--- | :--- |
| **`task chore <title>`** | `-p, --plane <intent\|substrate>`, `--paths`, `--obligations`, `--project`, `--json` | **1-Turn Fast Inception:** Allocates a `burden: 1` task, provisions `worktrees/<repo>-<KEY>`, mints an `EDIT`-phase `CapBACLease`, and assigns a monotonic `MTPO` sequence in one turn (`IDLE -> EDIT`). |
| **`task pair <title>`** | `--scope`, `--ac`, `--verify`, `--paths`, `--obligations`, `--json` | Atomically allocates twinned `INT-<N>` (specification) and `SUB-<N>` (implementation) tasks wired in the causal DAG. |
| **`task batch`** | `-F, --file <path\|->`, `--project`, `--json` | Compiles a multi-node Agent-to-Agent `TaskBatchManifest` JSON subgraph in one atomic SQLite transaction, resolving symbolic `$ref` and `twin_of` keys and rejecting DAG cycles. |
| **`task create <title>`** | `-p, --plane`, `--scope`, `--ac`, `--verify`, `-F`, `-b, --burden`, `-r, --priority`, `--depends-on`, `--paths`, `--obligations`, `-k, --key`, `--json` | Persists a durable 3-section Living Ledger task card (`Scope & Context`, `Acceptance Criteria`, `Verification`) with automatic cognitive burden sizing (`1-10`). |
| **`task dag`** | `--plane`, `--limit`, `-A`, `--json` | Computes unblocked `WavefrontCandidate` tasks whose upstream dependencies are `DONE` and flags `OverlapsActive` scope collisions. |
| **`task start <KEY>`** | `--edit`, `--json` | Transitions `IDLE -> PLAN` (or `IDLE -> EDIT` with `--edit`), locks trunk, creates `worktrees/<repo>-<KEY>`, and mints a step-budgeted `CapBACLease`. |
| **`task edit <KEY>`** | `--json` | Transitions `PLAN -> EDIT`, unlocking worktree source mutations. |
| **`task commit [KEY]`** | `-m, --message`, `-F, --file`, `--rationale`, `--mechanics`, `-a, --all`, `-d` | Stages changes and creates a **Golden Conventional Commit** (`[<KEY>] <type>(<scope>): <summary>` <= 72 runes) hydrated with rationale prose and RFC 822 `Task:` / `Twin:` Git trailers. |
| **`task review <KEY>`** | `--json` | Transitions `EDIT -> REVIEW`, freezing code mutations and running all 42 DoD gates. |
| **`task sync <KEY>`** | `-m, --message` OR `-F, --file` (mutually exclusive), `--json` | **Atomic Two-Phase Commit (`2PC`) Release:** Runs 42/42 DoD gates + `MTPO` serializability check, unlocks trunk, fast-forwards `main`, seals `ReleaseNotePayload` (`SHA256Receipt`, `HookTrace`, `SpecDigests`) into `refs/notes/nomos/releases`, distills Mneme `pitfall`/`pattern` records, revokes lease, and prunes the worktree (`REVIEW -> IDLE`). |
| **`task abort <KEY>`** | `-h` | Revokes active `CapBACLease`, removes the transient worktree and unmerged branch, and resets workspace phase to `IDLE`. |
| **`task list`** | `--pending`, `--status`, `--plane`, `-A`, `--limit`, `--json` | Lists and filters Living Ledger task records. |
| **`task show <KEY>`** | `--json` | Renders the full Living Ledger task card, twin lineage, and `TC-*` obligation status. |
| **`task project`** | `<project-id>`, `--json` | Assigns or reconciles repository `project` identifiers across Living Ledger tasks. |
| **`task prune`** | `--dry-run`, `--json` | Sweeps orphaned git worktrees, merged `feature/*` branches, and stale `.tmp/walkthrough-*` files. |

### 3.4 Agent Lifecycle Hooks, Memory, Sessions, Directives & MCP Commands

| Command | Subcommands & Key Flags | Purpose & Subsystem Connections |
| :--- | :--- | :--- |
| **`nomos hook`** | `install`, `session-start`, `pre-tool`, `post-tool`, `stop`, `post-commit` · Flags: `--hermetic-dir`, `--container-sandbox`, `--loop-strikes <2..10>`, `--budget-sec`, `--budget-warn-pct`, `--budget-crit-pct`, `--advisory-profile <standard\|carbon>`, `--advisory-style <standard\|terse>` | Zero-jq/zero-bash compiled Go stdin JSON interceptors across **Claude Code, Cursor, OpenCode, Gemini CLI, and Antigravity**. Enforces `~4ms` PreToolUse boundary/credential/egress/MCP/container guards, `PostToolUse` in-memory AST & `HotScopeReflex` radar + loop/budget circuit breakers, `Stop` anti-phantom verification, and `post-commit` Branch Guard Mneme distillation. |
| **`nomos handshake`** | `--json` | Boot-time orientation RPC: repairs `.git/info/exclude` & Git hooks, validates `CapBACLease` tokens, recalls top 2 repo `axiom`s + 3 `pitfall`s + 3 `pattern`s, and emits phase-specific `Next Actions`. |
| **`nomos capabilities [id]`** | `--type <hook\|directive\|engine\|gate\|cli\|all>`, `--json` | Introspects all `66` active substrate capabilities (`5` hooks, `16` directives, `8` engines, `21` cells / `42` probes, `16` CLI commands), persists SQLite invocation telemetry (`CapabilityMetrics`), and returns full law/ring/threshold/remedy metadata for any `probe_id`. |
| **`nomos directive [name]`** | `--lint <file.md>`, `--lexicon`, `--domain <lifecycle\|verification\|isolation\|memory\|steering>`, `--term <word>`, `--json` | Reads the `16` embedded phase-routed JIT playbooks, queries the `36`-term `NCTE-100` Succinct Action Lexicon, or audits Markdown specs against `NCTE-100` sentence/modal/passive-voice bounds (`--lint`). |
| **`nomos mem`** | `save <content>` (`--kind`, `--topic`, `-p`, `--json`) · `recall <query>` (`-A, --cross-repo`, `--kind`, `--topic`, `-p`, `-n`, `--json`) · `list` (`--status`, `-A`, `-p`, `-n`, `--json`) · `dismiss [id...]` (`--stale`, `-p`, `--json`) | Receipt-gated pure-Go SQLite episodic memory (`mneme.db`). Enforces the Statutory Write Firewall (`pitfall`/`pattern`/`release`/`review` minted strictly by verified commits; CLI `save` restricted to advisory `reflection`/`axiom`/`decision`), `<50ms` hybrid subword + lexical recall, and `--stale` dead-scope sweeps. |
| **`nomos session`** | `list` (`-b`, `--json`) · `show [id]` (`-b`, `-r`, `--json`) · `name [title]` (`-i`, `-b`, `--json`) · `visual` (`-b`, `-r`, `--json`) · `export` (`-n`, `-b`, `-r`, `--json`) | Multi-provider conversation discovery and process grading across AGY, Claude Code, OpenCode, Cursor, and Gemini CLI. Infers 7-stage causal trajectories (`BOOT -> RELEASE`), computes `DeliberationMetrics` and **`SentinelScorecard`** (`0-100` PRM score), renders Mermaid topologies (`visual`), and exports joined **RLVR / DPO / SFT JSONL datasets** (`export`). |
| **`nomos mcp`** | `--help` | Zero-dependency read-only `stdio` JSON-RPC 2.0 Model Context Protocol server (`7` tools, `3` `nomos://` resources, `3` prompts; negotiates MCP `2026-07-28` down to `2024-11-05`). |

---

## 4. Complete 7x3 = 21 Invariant Cell & 42 DoD Quality Gate Matrix

Every `nomos verify` run evaluates **42 deterministic sensor probes** organized across **7 Statutory Laws** and **3 Orthogonal Defense Rings** (`syntactic`, `dynamic`, `cryptographic`):

| Law | Cell (`Ring`) | Canonical `ProbeID`s (`42` Total) | Exact Verification Invariant |
| :---: | :--- | :--- | :--- |
| **Law 1**<br/>*Dual-Core Inversion* | `cell_1_1` (`syntactic`) | `complexity`, `max_file_loc`, `cognitive_debt`, `boundary`, `ui_anti_mask` | Enforces per-function $V(G)$ (`<=15` brace / `<=10` indent) & `<=60` LOC, `<=400` file LOC, `CDS <= 45` & Type-2 clone bounds, `prohibited_packages` / `prohibited_commands` / `architecture_boundaries`, and banishes defensive UI fallback masks (`membrane`). |
| | `cell_1_2` (`dynamic`) | `phase_discipline` | Verifies workspace mutations adhere strictly to `IDLE -> PLAN -> EDIT -> REVIEW -> SYNC`. |
| | `cell_1_3` (`cryptographic`) | `capbac_lease` | Verifies active worktree mutations hold a cryptographically valid, unexhausted `CapBACLease`. |
| **Law 2**<br/>*Non-Verbal Proof* | `cell_2_1` (`syntactic`) | `tdd`, `anti_shim`, `docstrings`, `dead_code`, `uasset_dependency` | Enforces paired unit tests (`strict`/`ratchet`/`diff` + `.tmp/tests`), blocks `t.Skip`/`@Disabled`/`#[ignore]`/deleted assertions/trivial stubs (`ERR_TRIVIAL_PASS_SHIM`), enforces `>=10%` docstring density, prunes dead code, and validates binary asset references. |
| | `cell_2_2` (`dynamic`) | `go_toolchain`, `coverage`, `visual_regression` | Executes isolated polyglot build/test/lint/vuln runners (`GOWORK=off` + `corpus_command`), enforces `>=55%` statement coverage + greenfield relational boundary-mutant spot-checks, and checks perceptual UI baselines. |
| | `cell_2_3` (`cryptographic`) | `review_receipt`, `gate_integrity_proof` | Validates non-empty walkthrough/review artifacts and attests `100%` mutant kill sensitivity, `0%` FPR, and non-vacuity across gates. |
| **Law 3**<br/>*Hermetic Worktree* | `cell_3_1` (`syntactic`) | `staged_boundaries` | Rejects staged files escaping active task `scope_paths` or worktree roots. |
| | `cell_3_2` (`dynamic`) | `hermetic_worktree`, `git_hygiene`, `ecosystem_drift` | Blocks direct trunk mutations, rejects stray scratch files outside `.tmp/` & raw `exec.Command("git")` bypasses, and blocks unapproved lockfile mutations (`lock_dependencies`). |
| | `cell_3_3` (`cryptographic`) | `release_receipt` | Verifies trunk release commits carry SHA-256 `ReleaseNotePayload` attestations in `refs/notes/nomos/releases`. |
| **Law 4**<br/>*Single Source of Truth* | `cell_4_1` (`syntactic`) | `broken_wire`, `intent_traceability`, `intent_steelman` | Audits Markdown/relative links, verifies `@intent` AST anchors + `@verifies TC-*` 11-language test bindings + normative registry surjection + stale `SpecDigests`, and validates `NCTE-100` spec structure. |
| | `cell_4_2` (`dynamic`) | `contract_parity` | Enforces bijective spec-to-AST struct/type signature parity (`strict_types`), [`nomos.schema.json`](nomos.schema.json) reflection parity, and backend-to-frontend DTO parity (`membrane.fail_on_contract_drift`). |
| | `cell_4_3` (`cryptographic`) | `intent_parity` | Verifies SHA-256 byte parity between workspace governance templates/directives/hooks and compiled `//go:embed` assets. |
| **Law 5**<br/>*Zero Hardcoded Paths* | `cell_5_1` (`syntactic`) | `forbidden_literals`, `ip_leak`, `magic_strings`, `magic_string_duplication` | Blocks machine-specific host paths, private IP/hostname leaks, unextracted magic literals, and `>10x` repeated string literals per package. |
| | `cell_5_2` (`dynamic`) | `infra_ingress` | Verifies every environment variable read in code is declared in `.env.example` (`fail_on_orphan_env`) and audits ingress manifests. |
| | `cell_5_3` (`cryptographic`) | `topology_parity` | Audits multi-seat service port declarations (`port_scan`) to prevent workstation daemon collisions. |
| **Law 6**<br/>*Canonical Singularity* | `cell_6_1` (`syntactic`) | `commit_schema` | Enforces Golden Conventional Commits (`[<KEY>] <type>(<scope>): <summary>` + rationale prose + `Task: <KEY>` trailer + `intent_twin_mode`). |
| | `cell_6_2` (`dynamic`) | `zero_alias`, `zero_filesystem_alias` | AST-scans Cobra commands to forbid CLI aliases/wrappers/deprecated invocations and rejects repository symlinks (`ERR_SYMLINK_FORBIDDEN`). |
| | `cell_6_3` (`cryptographic`) | `canonical_singularity` | Enforces strict `^(INT\|SUB)-[0-9]+` task key singularity and unique spec/gate IDs. |
| **Law 7**<br/>*Fail-Closed Pre-Flight* | `cell_7_1` (`syntactic`) | `secret`, `config_immutability`, `plane_separation` | Scans entropy/patterns for leaked secrets/keys, blocks `SUB-*` or `feat/*` edits to `nomos.yaml`/linter configs, and blocks cross-plane `INT` vs `SUB` file mutations (`ERR_CROSS_PLANE_MUTATION`). |
| | `cell_7_2` (`dynamic`) | `unchecked_errors` | Rejects discarded error returns (`_ = fn()`, bare `catch {}`, `except: pass`) across Go and polyglot ASTs. |
| | `cell_7_3` (`cryptographic`) | `threshold_integrity` | Compares worktree `nomos.yaml` against trunk to reject any numeric ceiling relaxation or `tdd_mode`/`coverage_mode`/`strict_mode` downgrade. |

---

## 5. Declarative `nomos.yaml` Schema Reference ([`nomos.schema.json`](nomos.schema.json))

### 5.1 Isolation, Security Airlock & Egress Policies (`governance.boundary`)
- **`isolation_mode`:** `"branch"` (default on `nomos init`; locks `main`, verifies `feat/*` commits) or `"worktree"` (enabled via `nomos init --worktrees`; requires isolated `worktrees/<repo>-<KEY>` + `CapBACLease`).
- **`egress_mode` & Domain Lists:** `"standard"` (default; blocks `169.254.169.254` cloud metadata SSRF), `"allowlist"` (`allowed_egress_domains`), or `"offline"` (zero outbound HTTP/HTTPS), plus `blocked_egress_domains`.
- **`credential_read_mode` & `blocked_read_paths`:** `"host_deny"` (default; blocks `~/.ssh`, `~/.aws`, `~/.kube`, `~/.gnupg`, `~/.config/gcloud`, `.netrc`, `.npmrc`, `.pypirc`), `"strict"`, or `"taint_only"` (records `TaintEdge` in `CapabilityFlowGraph` to block downstream exfiltration sinks).
- **Zero-Trust MCP & Package Locks:** `allowed_mcp_servers`, `blocked_mcp_servers`, `lock_dependencies` (blocks unapproved `package.json`/`go.mod`/`Cargo.toml` drift), `prohibited_packages`, and `prohibited_commands`.

### 5.2 Ratchets, Thresholds & 3-Pillar Wire-Drift Contracts (`governance.quality_gates`)
- **Progressive Modes:** `strict_mode` (`true` greenfield / `false` brownfield `nomos.baseline.json` ratchet), `ratchet_mode` (`"standard"` hotspot burndown vs `"upstream"` surgical external OSS PR mode preventing collateral file extractions), `verify_scope` (`"diff"` default vs `"repo"`), `intent_twin_mode` (`"off"` | `"feat"` | `"strict"`), `tdd_mode` (`"off"` | `"ratchet"` | `"diff"` | `"strict"` + `tdd_exclude`), `coverage_mode` (`"off"` | `"artifact"` | `"live"`), `polyglot_toolchains` (`true`), and `corpus_command`.
- **Statutory `thresholds`:** `max_file_loc: 400`, `max_func_loc: 60`, `max_cyclomatic_complexity: 15`, `max_cognitive_debt_score: 45`, `max_func_params: 8`, `max_package_files: 40`, `min_clone_lines: 18`, `max_magic_string_duplication: 10`, `min_comment_density_pct: 10`, `min_test_coverage_pct: 55`.
- **`architecture_boundaries`:** Directional import rules (`name`, `from` glob, `forbid_imports`, `allow_only`) enforced in `<5ms` across Go and polyglot imports (including TypeScript/Svelte `$lib/`, `@/`, `~/` aliases).
- **3-Pillar `contracts` Engine:**
  1. **`contracts.contracts`:** `spec_roots`, `schema_files`, `backend_roots`, `strict_types` (bijective polyglot spec-to-AST struct parity), `verify_obligations` (`@verifies TC-*` across 11 languages), and `fail_on_stale_obligations` (SHA-256 `SpecDigests` taint check).
  2. **`contracts.membrane`:** `backend_dto_roots`, `frontend_roots`, `fail_on_contract_drift`, and `banish_defensive_masks` (`ui_anti_mask`).
  3. **`contracts.infra`:** `env_declarations` (`.env.example`), `fail_on_orphan_env`, `ingress_configs`, and `port_scan`.

---

## 6. Autonomic Physics, `HotScopeReflex`, `MTPO` & MCP Endpoints

### 6.1 Closed-Loop Mneme Math & Adaptive `HotScopeReflex`
- **Hybrid Recall Score (`nomos mem recall`):**
  $$\text{Score} = (0.75 \cdot \text{CosineSimilarity} + 0.25 \cdot \text{LexicalOverlap}) \cdot e^{-\lambda \cdot \text{age}_{\text{days}}} \cdot \text{Reinforcement} \cdot \text{LocalityBoost}$$
  Uses deterministic 64-dim domain-separated SHA-256 subword + character 4-gram unit vectors (`CGO_ENABLED=0`) with optional `LOCAL_EMBEDDING_URL` (`127.0.0.1:8081/v1/embeddings`) neural vectors, $1.25\times$ repository locality boost, and `ConsolidateConventionAxioms` multi-scope promotion.
- **Adaptive `HotScopeReflex`:** When a directory accumulates $\ge 3$ AST pitfalls (`access_count < 5`), `PostToolUse` automatically tightens complexity/LOC early-warning thresholds in that directory from `80%` to `65%` and enables inline arity, unchecked-error, and anti-shim micro-probes until `5` consecutive clean tasks cool the scope back to `80%`.

### 6.2 Software Physics & `MTPO` Serializability
- **File Mass & Viscosity:** $M(f) = 0.1\cdot\text{LOC} + 2.0\cdot v(G) + 0.05\cdot|\text{AST}| + 1.5\cdot(\text{FanIn}+\text{FanOut})$, paired with Shannon token entropy $H(f)$ and 10-token Rabin-Karp rolling-hash viscosity $\mu(f)$.
- **Monotonic Trajectory Pre-Order (`modules/mtpo`):** Assigns ascending `Seq` numbers on worktree start, tracks AST read/write symbol sets on every edit (`<25ms`), emits write-time `[Nomos MTPO Advisory]` warnings on overlapping symbols, and runs `VerifySyncSerializability` during `nomos task sync` to block stale fast-forwards when an earlier sibling trajectory mutated a read symbol.

### 6.3 Local `stdio` MCP (`nomos mcp`) vs. Remote Streamable HTTP MCP (`sophialabs.ca/mcp`)

| MCP Surface | Transport & Auth | Exposed Tools, Resources & Prompts |
| :--- | :--- | :--- |
| **Local Workspace MCP (`nomos mcp`)** | `stdio` JSON-RPC 2.0 (`0` network egress; supports `2026-07-28`, `2025-11-25`, `2025-06-18`, `2025-03-26`, `2024-11-05`) | **7 Tools:** `nomos_handshake`, `nomos_status`, `nomos_task_dag`, `nomos_mem_recall`, `nomos_verify_impact`, `nomos_xray`, `nomos_capabilities`<br/>**3 Resources:** `nomos://status`, `nomos://capabilities`, `nomos://directives`<br/>**3 Prompts:** `xray-audit`, `verify-impact`, `recall-pitfalls` |
| **Remote Portal MCP ([`server.json`](server.json))** | Streamable HTTP `POST https://sophialabs.ca/mcp` (`0` auth, stateless) | **7 Tools:** `xray_github_repo`, `inspect_dod_gates`, `diagnose_nomos_error`, `scaffold_nomos_config`, `recommend_adoption_plan`, `compare_governance_approaches`, `search_nomos_corpus`<br/>**6 Resources:** `nomos://capabilities/catalog` (this Page 0 SSoT), `nomos://corpus/overview`, `nomos://gates/catalog`, `nomos://cli/reference`, `nomos://compare/matrix`, `nomos://skills/bootstrap` + templates `nomos://specs/{slug}`, `nomos://handbook/{slug}`, `nomos://dispatches/{slug}` |

---

## 7. All 9 Distribution, CI & Agent Registry Channels

| Channel | Canonical Install / Invocation | SSoT Manifest |
| :--- | :--- | :--- |
| **1. POSIX Installer (Linux / macOS / WSL2)** | `curl -fsSL https://sophialabs.ca/install \| bash` | `install.sh` / `uninstall.sh` |
| **2. Homebrew Tap (macOS & Linux)** | `brew install --cask mgantlett/tap/nomos` | `.goreleaser.yaml` |
| **3. Node / `npx` Zero-Install Launcher** | `npx --yes @mgantlett/nomos xray .` | [`packaging/npm/package.json`](packaging/npm/package.json) |
| **4. Python / `uvx` Zero-Install Launcher** | `uvx nomos-cli xray .` (or `pipx run nomos-cli xray .`) | [`packaging/pypi/pyproject.toml`](packaging/pypi/pyproject.toml) |
| **5. Arch Linux / Omarchy AUR** | `makepkg -si` on verified `PKGBUILD` | [`packaging/aur/PKGBUILD`](packaging/aur/PKGBUILD) |
| **6. Go 1.26+ Source Build** | `GOTOOLCHAIN=auto CGO_ENABLED=0 go install github.com/mgantlett/nomos/substrate/cmd/nomos@latest` | `substrate/go.mod` |
| **7. GitHub Actions PR Gate & SARIF** | `uses: mgantlett/nomos@v4.30.0` (`base-ref`, `mutate-diff`, `receipt-path`, `sarif-path`) | [`action.yml`](action.yml) |
| **8. Cross-Agent `SKILL.md` Bootstrapper** | `npx skills add mgantlett/nomos` | [`skills/nomos/SKILL.md`](skills/nomos/SKILL.md) |
| **9. Claude Plugin & Official MCP Registry** | `/plugin marketplace add mgantlett/nomos && /plugin install nomos` · `claude mcp add --transport http nomos https://sophialabs.ca/mcp` | [`.claude-plugin/plugin.json`](.claude-plugin/plugin.json) · [`server.json`](server.json) · [`glama.json`](glama.json) |

---

## 8. Embedded JIT Directives Catalog (`16` Phase-Routed Playbooks)

Inspect any directive offline via `nomos directive <name>`:
- **Orientation & Agency (`IDLE`..`LEARN`):** `00_index_and_taxonomy.md`, `00_tier1_agency.md`, `02_agentic_workflow_discipline.md`, `handshake_01_topology_and_workstations.md`
- **Planning & NCTE-100 (`PLAN`):** `plan_01_sizing_and_dag.md`, `plan_02_architectural_synthesis.md`, `plan_03_nomos_controlled_technical_english.md`
- **Editing & SSoT Contracts (`EDIT`):** `edit_01_deprecated_terms.md`, `edit_02_transient_migrations.md`, `edit_03_refactoring_disciplines.md`, `edit_04_traceability_anchors.md`, `edit_05_schema_synthesis.md`, `edit_06_ssot_alignment.md`
- **Verification & 2PC Sync (`REVIEW`, `SYNC`):** `review_01_dod_gates.md`, `review_02_adversarial_audit.md`, `sync_01_atomic_2pc_and_receipts.md`
