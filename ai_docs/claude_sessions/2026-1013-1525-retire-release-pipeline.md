# 2026-10-13 15:25 — Release pipeline retired (N-001 declined, N-009 moot)

Session: `9912fa41-2f6c-47a8-8567-394eeb33ccad` (run from the cats repo, as
cats next-list item N-019)

## Request

cats N-019: ced's `release.yml` still described the `release` branch with
automatic version bumps, a route unused since 0.2.0. "Retire it or go back
to it." Found while checking: 15 tags since v0.2.0, and not one GitHub
Release. The Releases page's latest is still v0.2.0 (2026-07-29), so
`install.sh` (which fetches the latest release) installed a binary
two and a half months old.

The owner chose to **retire it entirely**: no workflow, no goreleaser, no
install script, no binaries. They also said "Don't go back to
release.yml".

## What changed

- Deleted `.github/workflows/release.yml`, `.goreleaser.yml` and
  `install.sh`.
- README:
  - The install script and manual binary sections are replaced by one
    line: there are no pre-built binaries, so without cats, build from
    source.
  - The feature bullet now says "builds for" rather than
    "cross-compiled".
  - The layout tree has lost the goreleaser row.
  - **Releases** describes the hand flow: bump `version.go` and
    `cats-plugin.toml`, `make test`, commit `Release ced x.y.z`, then an
    annotated tag, and push `main` plus the tag. It notes that the Releases
    page stops at v0.2.0.
- CLAUDE.md:
  - Module/repo: there's no install script or release binaries, and the
    pipeline was retired on 2026-10-13. Don't bring it back.
  - The CI note no longer points at the removed Releases notes.
  - The terminal note's "don't add windows to goreleaser" now points at
    `cats-plugin.toml`'s `platforms`.
  - The Releases section has lost the parked-pipeline notes.
- AGENTS.md: the release-packaging line (it still named `Formula/ced.rb`
  and `install.sh`) and the `[skip ci]` release-automation advice now
  describe the hand flow.
- Comments only: `cats-plugin.toml` (version and platforms),
  `internal/version/version.go`, `version_test.go` (both tests stay, and
  their reasons now name the hand bump and the tags) and `test.yml`'s
  header.
- `test.yml` stays. As before, it doesn't fire on this fork until Actions
  is enabled.

## Left alone

- The v0.2.0 GitHub Release, kept by the owner's choice. The `release`
  branch (at `0409317 Release ced 0.2.0`, fully contained in main) was
  deleted, remote and local, at the owner's say.
- `.claude/commands/summary-of-downloads.md`. It still reports the old
  releases' downloads correctly.
- The Makefile's `alt` target, which is cats N-010's remaining question.

## Checks

`go vet ./internal/version`. `go test ./internal/version ./` passes
(`TestVersion_IsSemver`, `TestVersion_MatchesCatsManifest`). A grep for
goreleaser, `install.sh` and `release.yml` finds only history (session
docs, the next-list) and the new "retired" notes.

## Next

Closed: N-009. Declined: N-001. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None. Full list:
`ai_docs/todo/next-list.md`.
