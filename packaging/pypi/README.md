# `nomos-cli`

Zero-dependency Python (`uvx` / `pipx` / `pip`) launcher for [**Nomos**](https://github.com/mgantlett/nomos) (`Apache-2.0`), the compiled static Go AST and Git governance substrate between AI coding agents and production repositories.

## Quick Usage (`uvx` - Zero Install Required)

```bash
# Run a read-only sub-5s AI Hangover Score & AST hotspot audit:
uvx nomos-cli xray .

# Initialize non-breaking Branch Guard & Day-0 brownfield baseline ratchet:
uvx nomos-cli init --local

# Launch the local read-only stdio MCP server:
uvx nomos-cli mcp

# Inspect all 66 compiled capabilities, 8 engines, and 42 gate ProbeIDs (--json):
uvx nomos-cli capabilities
```

Or install `nomos` onto your Python environment `PATH`:

```bash
uv tool install nomos-cli
# or: pip install nomos-cli
```

Or attach directly to the zero-install remote Streamable HTTP MCP endpoint:

```bash
claude mcp add --transport http nomos https://sophialabs.ca/mcp
```

## Complete Capabilities & Subsystem Map (Page 0 SSoT)

- **Developer Docs (Interactive Diagram & TOC):** [https://sophialabs.ca/docs/specs/capabilities](https://sophialabs.ca/docs/specs/capabilities)
- **AI Agent Raw Markdown (`text/markdown`):** [https://sophialabs.ca/docs/specs/capabilities.md](https://sophialabs.ca/docs/specs/capabilities.md)
- **GitHub Canonical Source:** [`CAPABILITIES.md`](https://github.com/mgantlett/nomos/blob/main/CAPABILITIES.md)
- **MCP Resource URI (Local `stdio` & Remote HTTP):** `nomos://capabilities/catalog` (`nomos://capabilities`)

## SHA-256 Integrity Verification

When `nomos` is not already present on `PATH`, `nomos-cli` downloads the official GitHub Release archive (`https://github.com/mgantlett/nomos/releases`) for your OS/CPU (`Linux`/`Darwin`/`Windows`, `x86_64`/`arm64`) using Python's standard library and verifies its SHA-256 digest against the release `checksums.txt` manifest before extracting or executing the binary.

<!-- mcp-name: io.github.mgantlett/nomos -->
