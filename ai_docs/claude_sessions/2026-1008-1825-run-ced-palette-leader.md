# 2026-10-08 18:25 — run-ced recipes: palette is Esc k (N-041)

Session: `5267531f-a213-432e-b099-b66670bed267`

## Request

From the cats-todo backlog, next-list item **N-041**: the
`.claude/skills/run-ced/SKILL.md` recipes open the command palette with
`{esc}a`, which is now the AI namespace (the palette is `Esc k`), so a
script following the doc types its query into the open buffer. Also check
the "Leaders worth knowing" line.

## What changed

Commit `0ccc864` — "run-ced: palette is Esc k, not Esc a (N-041)".

- `.claude/skills/run-ced/SKILL.md`
  - Both palette recipes use `{esc}k`.
  - "Leaders worth knowing": `k` palette, adds `?` key bindings, and warns
    that `a` opens the AI namespace (the next key is read as an AI verb).
    The other listed leaders (p, t, f, g, L, \`, w, s, q) were checked
    against `internal/app/leader.go` and are correct.
  - Theme-picker recipe filters `theme:` instead of `theme` (see below).
- `.claude/skills/run-ced/capture/main.go` — the `-help-script` text had
  the same mistakes:
  - leader list said `{esc}a palette` → `{esc}k palette`;
  - theme-picker example → `{esc}k` + `theme:`;
  - ≡ menu example sent `{esc}{esc}` in one write → split into
    `400@{esc};120@{esc}` like SKILL.md's recipe.
- `ai_docs/todo/next-list.md` — N-041 moved to Closed.

## Found by running the recipes against the real binary

Built `bin/ced` and the capture driver (into the session scratchpad) and
ran each recipe with `-text`:

- `{esc}k` + `theme` opens the Command palette with 3/79 rows: **Reload
  themes**, Theme: Tokyo Night…, Customize theme…. "Reload themes" ranks
  first, so the theme-picker recipe's `{enter}` reloaded themes rather
  than opening the picker. `theme:` filters to the "Theme: …" row; Enter
  opens the Theme picker (11 rows).
- The capture help's single-write `{esc}{esc}` is the counter-example
  SKILL.md already documents (folds into Alt+Esc, nothing opens). The
  split form opens the ≡ menu (Command palette / File / Edit … rows seen).

`gofmt -l` and `go vet` clean on the capture module. No ced source
changed, so `make test` was not run.

## Next

Closed: N-041. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
