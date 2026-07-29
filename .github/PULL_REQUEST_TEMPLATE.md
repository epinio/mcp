<!--
Thanks for contributing to the Epinio MCP server. Fill in the sections below.
Keep it proportional: a small change gets a short PR. PRs are documentation.
-->

## What changed

<!-- Plain-language summary of what was added, changed, or removed. Group
related changes; don't just list files. -->

## Why

<!-- The reason for the change. Link the issue if there is one. -->

Closes #

## What to test

<!-- Explicit steps a reviewer can follow to verify this works. "It works" is
not enough. If tests were descoped for this change, say so here. -->

## Possible regressions

<!-- What could this break, and which adjacent areas should be checked? If none,
write "none identified" and why. -->

## Type

<!-- Keep the one that applies: -->

`feature` / `fix` / `chore` / `breaking-change`

## Checklist

- [ ] `make check` passes (fmt-check, vet, lint, test)
- [ ] New or changed behavior has tests, or the test impact is noted above
- [ ] Tool names, inputs, or outputs changed? The [MCP server reference](https://docs.epinio.io/reference/mcp) is updated
- [ ] New or changed capability sits in the right tier: **core** (Epinio API only, runs as the caller) vs **elevated** (`EPINIO_MCP_ELEVATED`, reaches directly into Kubernetes and needs the standard-elevated RBAC)
- [ ] Touches auth, credentials, or the RBAC/appchart surface? Flagged for security review
- [ ] User-facing behavior changed? A matching PR is opened in [`epinio/docs`](https://github.com/epinio/docs)
- [ ] No secrets, tokens, or real cluster credentials committed
