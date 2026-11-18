# /work-next-list — final wrap: the run's ledger, and N-150 measured

Session: `bf5fffcd-9ebf-4213-a664-3cfa9fde36c5`

## What this doc is

The closing wrap of one `/work-next-list` run over
`ai_docs/todo/next-list.md`, which went out in seven batches, each with
its own session doc:

| Doc | Items |
| --- | --- |
| `2026-1117-2243-n147-n122-n123-n126-detached-children-lsp-writer` | N-147, N-122, N-123, N-126 |
| `2026-1117-2301-n124-n101-n138-post-result-tsc-lsp-fuzz` | N-124, N-101 (→ Validate), N-138 |
| `2026-1117-2313-n129-n128-syntax-classes-async-lex` | N-129, N-128 (raised N-149, N-150) |
| `2026-1117-2326-n139-n151-tmux-harness-clipboard` | N-139, N-151 (raised and fixed) |
| `2026-1117-2344-n121-n130-data-safety-render-window` | N-121, N-130 |
| `2026-1117-2352-n132-n148-n134-n135-find-fastpath-help-menu` | N-132, N-148, N-134, N-135 |
| `2026-1118-0003-n136-n140-n144-undo-trim-git-panels-recovered` | N-136, N-140, N-144 |

This last step only measured N-150 so its decision has numbers: a
throwaway test (not committed) timed `HighlightClasses` on synthetic Go —
512KB 436 ms / 180MB allocated, 1MB 863 ms / 358MB, 2MB 1.7 s / 715MB,
4MB 3.4 s / 1.4GB — linear, about 0.85 ms and 350KB of garbage per KB,
paid on every settle. Recorded in the item; the cap stays 512KB until
the user decides.

## Not done, and why

- **Needs the user** (from the opening triage): N-095, N-142, N-143,
  N-146 (contingent: "if it bites"); N-103 (swap pyright and
  basedpyright?), N-133 (root `ced file` at the repository?); N-137
  (enable Actions in the fork's Actions tab); N-013, N-067, N-096
  (installs: the rust-analyzer component, pg_format, an ACP agent); the
  hand checks in Validate. Since this run, N-101 is a Validate item too
  (a real TypeScript 7 check — installing it was refused here).
- **N-149** "fix if it shows" — contingent, left.
- **N-150** — the user's call, now with measurements.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None. Updated: N-150 (measured).
Full list: `ai_docs/todo/next-list.md`.
