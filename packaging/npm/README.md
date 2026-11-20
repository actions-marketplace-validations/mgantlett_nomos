# `@mgantlett/nomos`

Zero-dependency Node.js (`npx`) launcher for [**Nomos**](https://github.com/mgantlett/nomos) (`Apache-2.0`), the compiled static Go execution substrate between AI coding agents and Git.

## Quick Usage (Zero Global Install Required)

```bash
# Run a read-only 5-second AI Hangover Score & AST hotspot audit:
npx --yes @mgantlett/nomos xray .

# Initialize non-breaking Branch Guard & Day-0 brownfield baseline ratchet:
npx --yes @mgantlett/nomos init --local

# Launch the local read-only stdio MCP server:
npx --yes @mgantlett/nomos mcp
```

Or connect directly to the zero-install remote Streamable HTTP MCP surface:

```bash
claude mcp add --transport http nomos https://sophialabs.ca/mcp
```

## Integrity Verification

When `nomos` is not already present on `PATH`, `@mgantlett/nomos` downloads the official GitHub Release tarball (`https://github.com/mgantlett/nomos/releases`) for your OS/CPU (`linux`/`darwin`/`windows`, `x86_64`/`arm64`) and verifies its SHA-256 digest against the release `checksums.txt` manifest before execution.
