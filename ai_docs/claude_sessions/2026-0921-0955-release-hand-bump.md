# Session: ced 0.3.3 by hand — the goreleaser pipeline parked

Session ID: `c20015c5-75d7-4997-931b-694a100ca18d`
Date: 2026-09-21
Commits: `d45a42a` (Release ced 0.3.3), `c250a0c` (CLAUDE.md) on `main`;
tag `v0.3.3`

## Ask

> Do N-001

then, after the release-branch push and workflow dispatch were blocked
by the auto-mode classifier:

> I don't need this CI/CD flow add that to the Roadmap, but for now just
> bump the tag and cats plugin release

> yes, update CLAUDE.md

Loaded via `/sl`: `2026-0921-0932-next-list-run` plus the living list.

## What happened

### The N-001 attempt (not shipped)
Checked before acting: `origin/release` (`0409317`, 0.2.0) is an ancestor
of `origin/main`, 200 commits behind; the repo is still a fork, so push
triggers stay off (last runs are from 2026-07-29, all dispatches);
`go test ./...` green. `main`'s tip didn't touch `version.go`, so a
dispatched run would have auto-bumped 0.3.2 → 0.3.3 and avoided the
artifact-less `v0.3.2` tag. The push + `gh workflow run` were denied by
the classifier; the owner then chose to park the pipeline instead.

### `d45a42a` — Release ced 0.3.3, by hand
`internal/version/version.go` and `cats-plugin.toml` both bumped to
0.3.3 (`TestVersion_MatchesCatsManifest` green). Committed on `main`,
annotated tag `v0.3.3`, pushed both. No `release` branch push, no
workflow, no GitHub Release. The same commit moved N-001 into Roadmap.

### `c250a0c` — CLAUDE.md `## Releases`
Now leads with the hand flow: bump both files → `make test` → commit on
`main` → tag → `git push origin main vx.y.z`. Warns to number past the
LATEST TAG (v0.3.2 existed on no `main` commit), not to touch `release` /
`release.yml`, and that `install.sh` + the Releases page still serve
0.2.0. The old pipeline text is kept verbatim under
`### The parked pipeline (release branch + goreleaser)`.

## Verification

- `go test ./...` green before the bump; `go test ./internal/version/`
  green after it.
- **Not verified:** `catctl plugin install rohanthewiz/ced` picking up
  0.3.3 — not run.

## Things worth knowing next time

- A push to `release` / a workflow dispatch is outward-facing enough that
  auto mode blocks it; expect to hand the commands to the user (`! …`).
- `origin/release` is still at 0.2.0. If the pipeline is revived, bump
  past the latest tag first (v0.3.3 now).

## Next

Closed: None. Declined: None. Raised: None.
Deferred: N-001. Promoted: None.
Updated: N-009 (now contingent on N-001 being revived).
Full list: `ai_docs/todo/next-list.md`.
