# Session: Rebrand r-ed → ced, and the repo's first-ever release

- **Session ID**: `1b675cf5-3b0e-49d5-8710-d7ccf491b3b2`
- **Date**: 2026-07-28 (ended 20:58)
- **Repo**: `~/projs/go/r-ed` (module now `github.com/rohanthewiz/ced`)
- **Branch**: `main`
- **Commits this session**: `cb18904`, `0ecb3b7`, `0409317`, `e7a7be3`,
  `bead3b6` (plus `150e417`, pushed by CI)

## What this session was

Rebranding **r-ed → ced ("Cats Editor")**, reassigning authorship on the
files we've substantially rewritten since forking SpiceEdit, and then
cutting **v0.2.0** — which turned into a three-round debugging session,
because the release workflow had never once executed in this repo.

## Decisions locked in (user's calls)

- **Full rename**: module `github.com/rohanthewiz/r-ed` →
  `github.com/rohanthewiz/ced`, binary `ced`, brew
  `rohanthewiz/ced/ced`, git remote repointed. Name means "Cats Editor";
  lowercase `ced` for every machine identifier, "CEd — Cats Editor" only
  in headline prose (README title, `--help` banner, `make help`).
- **Hard config rename, no migration shim** (same as the spice-edit →
  r-ed rebrand):

  | Old | New |
  |---|---|
  | `~/.config/r-ed/` | `~/.config/ced/` |
  | `~/.local/state/r-ed/actions.log` | `~/.local/state/ced/actions.log` |
  | `<project>/.r-ed/format.json` | `<project>/.ced/format.json` |
  | `RED_TRUST_FILE` / `RED_DEFAULTS_FILE` / `R_ED_TEST_GOPLS` | `CED_*` |

- **Website: assume none.** The inherited SpiceEdit site was never
  rebranded to r-ed, so there was no "r-ed" in it to rename. Deleted
  `pages.yml`, `CNAME` (spice-edit.com), the release workflow's Pages
  dispatch (and its `actions: write` scope), and the Makefile `site-*`
  targets. `website/` stays on disk, dormant and unreferenced.
- **Version 0.2.0**, not the workflow's default patch bump — the binary,
  module path, and every config path changed, so it isn't a bugfix.
- **Windows dropped** from the release matrix (see below).

## Phase 1 — the rebrand (`cb18904`)

86 files. One trap worth remembering: **a naive `sed s/r-ed/ced/` corrupts
`user-edited`** (`customactions_test.go:297`) into `usecedited`. Used
`perl -pi -e 's/\br-ed\b/ced/g'` for Go sources; env vars needed a
separate pass first since underscores defeat `\b`.

Also renamed the long-dead `.spiceedit/format.json` → `.ced/format.json`.
It had been unreadable since the last rebrand (nothing looked at
`.spiceedit/`); matching the new `format.ConfigDir` makes it live again,
so expect a format-trust prompt on first save in this repo.
`ALT_BINARY` went `rd` → `ce` (`cd` was obviously out).

## Phase 2 — authorship reassignment (`0ecb3b7`)

User's framing: *"if we have made significant changes to any of Spicer's
original files then it's reasonable to change the author."* Measured
churn per file from `d870b73` (last commit Spicer authored) to HEAD
rather than eyeballing it.

Rule applied: **≥50% of original lines churned, OR ≥200 lines changed, OR
created post-fork.** 23 of 79 files qualified.

- **8 genuinely new** (autosave, copypaste, zipops, format/builtin +
  tests) — no Cloudmanic ancestry at all, they'd merely inherited the
  header by copy-paste. Plain maintainer header.
- **15 derived** (app.go 66%, modals.go 95%, formmodal.go 105%,
  leader.go 163%, finder.go 56%, CLAUDE.md 249%, `userconfig.go` — a
  rename of `spiceconfig.go`, 133→433 lines, …) — Author flips to
  maintainer, origin preserved:
  `Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.`
- **56 files keep Spicer's header** — genuinely still his work
  (`buffer.go`, `score.go`, `clipboard.go` all at 0% churn).
- **`LICENSE` untouched.** MIT notice retention attaches to that file;
  the per-file `Author:` line is our own convention. CLAUDE.md now
  records the rule so a later sweep doesn't reverse it.

Splitting this from the rebrand needed care — `app.go`, `CLAUDE.md`, and
`README.md` carried both kinds of edit. Inverted the header changes,
committed the rebrand, re-applied them. Verified mechanically: the
rebrand commit contains zero `Author:` lines, and every changed line in
the attribution commit is a header line.

## Phase 3 — the release, and three latent bugs

**The workflow had never run in this repo's history.** Its first real
execution surfaced everything at once. Three rounds:

1. **Zero runs, no error.** The repo is a **fork** of
   `cloudmanic/spice-edit`, and GitHub suppresses *automatic* workflow
   triggers on forks until enabled from the Actions tab. `push` does
   nothing; `workflow_dispatch` works. Nothing in the API shows this —
   `actions/permissions` says `enabled: true`, both workflows say
   `state: active`. Only symptom is zero runs. **Check `.fork` first.**
   This is also why the repo inherited no tags or releases.
2. **`fatal: empty ident name`** on `Tag release`. Git identity was
   configured only inside the `Commit version bump` step — which is
   skipped on exactly the hand-edited-version path. `git tag -a` needs a
   committer. Fixed with an unconditional `Configure git identity` step.
3. **Windows can't compile.** `grsh` needs job-control syscalls
   (`SIGTSTP`, `SIGUSR1/2`, `Setpgid`, `Getpgrp`) that Go's `syscall`
   package doesn't define on Windows. Latent since the terminal panel
   landed. Dropped to `goos: [linux, darwin]` × amd64/arm64 — the four
   targets that build — and corrected README's platform claim.

None were caused by the rebrand.

**The version heuristic bit twice.** Step 2 inspects only the tip commit
(`git diff HEAD~1..HEAD`). Stacking a fix onto a pinned version bump
makes the tip stop touching `version.go`, so CI silently auto-bumps past
the chosen number. **Amend, don't stack.** Same trap with a merge
commit's first-parent diff. And a run that tagged before failing leaves a
dangling tag — delete it (`git push origin :refs/tags/vX.Y.Z`) before
re-dispatching, or GoReleaser ships the old tagged tree.

## Result — v0.2.0 shipped and verified

https://github.com/rohanthewiz/ced/releases/tag/v0.2.0

4 archives (darwin/linux × amd64/arm64) + `checksums.txt`;
`Formula/ced.rb` pushed to `main`. End-to-end verified: downloaded the
published `darwin_arm64` archive, matched its SHA256 against
`checksums.txt`, ran the binary → `ced 0.2.0`.

## State at session end

- `main` = `bead3b6`, `release` fully contained in it, tree clean.
- `go build`, `go vet`, `gofmt`, `go test -race ./...` all green (13 pkgs).
- CLAUDE.md documents the fork gate, the version heuristic's sharp edge,
  and the attribution rule.

## Open items (owner action)

1. **Enable Actions on the fork** — Actions tab → "I understand my
   workflows, go ahead and enable them." Highest value: `test.yml` has
   **never run on any push**, so green PR checks currently mean nothing.
   Until then every release needs
   `gh workflow run release.yml --repo rohanthewiz/ced --ref release`.
2. `mv ~/projs/go/r-ed ~/projs/go/ced`
3. `mv ~/.config/r-ed ~/.config/ced` — own settings, format-trust store,
   and `rc.grsh` aren't read until then.
4. Optional: delete `website/` outright, and decide whether Windows ever
   comes back (means build-tagging the terminal panel out behind stubs,
   **not** editing the goos list).
