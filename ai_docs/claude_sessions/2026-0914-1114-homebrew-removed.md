# Homebrew removed — the cats plugin is ced's only official install

Session: `session_017JuGQ823Vnw61jMoV3avZV`
(https://claude.ai/code/session_017JuGQ823Vnw61jMoV3avZV)

Date: 2026-09-14

> ## ⚠️ BREAKING DISTRIBUTION CHANGE
>
> **ced no longer ships a Homebrew formula.** As of 2026-09-14:
>
> - `Formula/ced.rb` is **deleted** — the in-repo tap is gone.
> - `.goreleaser.yml`'s **`brews:` block is removed** — a release run
>   publishes GitHub Release archives and `checksums.txt`, and nothing else.
> - The README's Homebrew install / upgrade / uninstall instructions are
>   **gone**.
>
> **The official install is the cats plugin:**
>
> ```sh
> catctl plugin install rohanthewiz/ced
> catctl plugin update rohanthewiz.ced
> ```
>
> The plugin builds from source and links `~/.cats/bin/ced`, which leads
> PATH in every cats pane (and in any shell that evals
> `catctl shellinit zsh`).
>
> **Anyone who installed with `brew tap rohanthewiz/ced …`** gets no further
> upgrades — the formula their tap points at no longer exists. Clean up with
>
> ```sh
> brew uninstall rohanthewiz/ced/ced
> brew untap rohanthewiz/ced
> ```
>
> and install through cats. Without cats, `install.sh` still works: it
> downloads the GitHub Release archives, which are unaffected.
>
> **Don't bring Homebrew back** without revisiting CLAUDE.md's "What NOT to
> add" entry — a second install channel is a second `ced` on PATH drifting
> behind the plugin build.

## What was asked

This closed out a longer session that started in the cats repo ("Is
`~/.cats/bin` even in use?") and ended here:

> What about the ced plugin can we do similarly for it too?

> The official install of CEd is through Cats as a plugin, not homebrew

> fix the make install mismatch in ced's README

> remove the Homebrew installation instructions from ced's README and any ref n the Makefile

> Yes take Homebrew out of the release pipeline and be sure to make a session doc in ced making the change prominent

## Why

Plugin actions exec `./bin/ced` inside the plugin root, so they always ran
the fresh plugin build. Everything that looked `ced` up on PATH — a shell,
an agent, and cats's `editor.command` spawn for clicked paths — found a
different, hand-installed copy instead. On the machine in question that was
`~/bin/ced` 0.2.0 against a plugin build of 0.3.0. A Homebrew formula is one
more way to end up with that second copy, and since the cats plugin is the
official install, it had no reason to stay.

## Everything that changed in ced today, in order

| Commit | What |
|---|---|
| `9103b61` | `cats-plugin.toml`: `bin = ["./bin/ced"]` — the host links `~/.cats/bin/ced`. Reverses `f179657`'s deliberate "NO [bin] ENTRY" (which feared shadowing a Homebrew ced). |
| `95da6ea` | Manifest comment reworded (plugin build is *the* `ced`, not a trade-off); README Install leads with "Inside cats (as a plugin) — the official install"; old plugin section claiming the plugin never touches PATH removed. |
| `dd361f2` | README "From source" and CLAUDE.md: `make install` copies to `/usr/local/bin` (may need sudo), not `go install` to `$GOPATH/bin`. |
| `84e60d3` | README: Homebrew install / Updating / Uninstalling subsections removed; install-script section retitled "Linux / macOS". Makefile `alt` comment points at `~/.cats/bin/ced` instead of a brew-installed ced. |
| this commit | **Homebrew out of the release pipeline** — details below. |

## The pipeline change (this commit)

- **`Formula/ced.rb`** — deleted (`git rm`). Last written by CI as
  `150e417 Update ced brew formula to v0.2.0 [skip ci]`.
- **`.goreleaser.yml`**
  - `brews:` block and its "Users install via brew tap…" comment removed.
  - Header rewritten. It also carried two stale claims, fixed on the way:
    it said releases run on "every push to main" (they run on pushes to
    `release`), and that the formula updated "when the homebrew tap secret
    is set" (there never was such a secret; it used `GITHUB_TOKEN`).
  - `builds`, `archives`, `checksum`, `changelog` untouched.
  - `goreleaser check` (v2, via `go run …@latest`): **1 configuration
    file validated**.
- **`.github/workflows/release.yml`** — no step changed. Header step 3 no
  longer mentions a formula push; the GoReleaser step's `GITHUB_TOKEN`
  comment now says the token creates the Release and uploads archives. The
  token itself stays — GoReleaser needs it for the GitHub Release.
- **`README.md`**
  - Repo layout tree: `Formula/` row removed; `.goreleaser.yml` described as
    "Cross-compile + GitHub Release archives".
  - **Releases** section rewritten. It was stale beyond Homebrew too: it
    said every push to `main` releases. Now: push to `release`, auto-bump
    commits back to `release`, GoReleaser attaches archives + checksums, no
    Homebrew formula, merge `release` back into `main` afterwards.
- **`CLAUDE.md`**
  - *Module / repo*: "Brew tap: this same repo, `Formula/`" replaced with
    the official-install line and a bold **No Homebrew formula** note.
  - *Release steps*: step 4 now says GoReleaser attaches archives and
    `checksums.txt` and **nothing else** — no commit after the version bump.
  - `[skip ci]` warning: "both auto-commits" → the version-bump commit is
    the only one left; any future commit-back step needs the same marker.
  - *What NOT to add*: "A separate `homebrew-tap` repo" (which defended the
    in-repo formula) replaced with **no Homebrew formula or tap, anywhere**,
    and why.

## Deliberately left alone

- `internal/app/app.go` / `app_test.go` — "brew upgrade" is an example of a
  `$FILE`-free custom-action command, not distribution.
- `internal/icons/icons.go` — "macOS users with Homebrew fontconfig" is
  about font discovery.
- `.claude/commands/summary-of-downloads.md` — says release download counts
  include Homebrew installs. True of every release up to v0.3.0; new
  releases won't have any. Worth a tweak if the note starts to mislead.
- `install.sh` and the GitHub Release archives — still the non-cats path.

## Release-branch state (for whoever cuts the next release)

- `origin/release` is at `0409317 Release ced 0.2.0`, **172 commits behind
  `main`**. Tags `v0.2.0` and `v0.3.0` exist.
- These commits went to `main`, which does not trigger `release.yml`, so
  nothing was published by this change. The next push to `release` is the
  first run without the `brews:` step.

## Verification

- `goreleaser check` — config valid.
- `release.yml` parses as YAML (Ruby `YAML.load_file`).
- `grep -i 'brew\|formula'` over `.goreleaser.yml`, `.github/`, `Makefile`
  and `README.md` — no distribution references left.
- Not verified: an actual release run (needs a push to `release`).
