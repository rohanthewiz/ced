# Session: Git blame — dated bands style

Session ID: 726905c4-6f50-4160-bb06-7c015bab19d1
Date: 2026-09-29

## 1. The prompt

From the cats-todo backlog, with a screenshot of an IDE blame gutter
(date + surname on every line, a tinted background band per commit):

> Add a Git blame option similar to this

## 2. What already existed

ced already had blame (`gitblame.go`, Esc-A, ≡ Git "Show blame" / "Blame
this line…"): blames the BUFFER via `git blame --porcelain --contents -`,
compact column `hash · given-name · age`, drawn once per run of lines
from one commit, muted, click → reveal commit in the git log. So the
request became a second *style*, not a new feature.

## 3. Design

- **Two styles**, config key `"blamestyle"`: `bands` (new, default) and
  `compact` (the existing look, untouched). ≡ Git row "Blame style: …"
  (label names the style it switches TO), no leader (table is full).
  Switching restyles instantly and never turns the layer on — flashes
  "(esc A shows it)" when hidden.
- **Bands**: every line labelled ` <M/D/YY> <author>`, date slot padded
  to the file's widest date so authors align. Run suppression dropped for
  this style: the color marks the boundary, so blank rows would only hide
  information.
- **Both geometries measured once per result** — `newFileBlame(lines)`
  computes `width` (compact), `bandWidth`, `bandDateW`, `bands` over the
  whole file, so a switch is a repaint, never a git fork, and nothing
  shifts under scroll.
- **Hues**: fixed six-hue wheel (orange, blue, magenta, green, violet,
  teal) rather than theme keys — only distinctness matters, and theme
  syntax colors can collide. Index = fnv32a(hash) % 6, so a commit keeps
  its color across re-blames; a run landing on its predecessor's hue
  bumps to the next (adjacent runs never share a band). Blended over
  theme BG (24% dark / 32% light).
- **Readability**: `blameBandFG` keeps theme `Text` when it reaches
  4.5:1 on the band, else steps it toward white/black until it does.
  Needed for Solarized dark and light, whose own Text/BG contrast is
  ~4.7 / ~4.1.
- **Uncommitted lines**: no band, `—` in the date slot, author "you" in
  GitAdded green. Parser also blanks `Date` for them (git stamps
  `--contents` lines with the ask time).

## 4. Editor primitive

`editor.LineAnnotation` gained `BG tcell.Color` (zero = ColorDefault =
no band). `Tab.Render` fills the whole annotation column with the band on
EVERY row of the line (wrapped continuations too), before text; text on a
band uses the band as background (the caret-line wash doesn't cut
through a run). Mark cell stays outside the band.

## 5. Files

- `internal/editor/decoration.go` — `LineAnnotation.BG`.
- `internal/editor/tab.go` — band fill + text background.
- `internal/userconfig/userconfig.go` — `BlameStyle` type/consts,
  `Config.BlameStyle` (default bands), load validation,
  `SaveBlameStyle`, header doc.
- `internal/app/gitblame.go` — header doc (diagram of both styles),
  `blameLine.Date`, `newFileBlame`, `blameDate`, band slot/width/text,
  `blameBandHues`, `assignBlameBands`, `blameBandColor`, `blameBandFG`,
  WCAG helpers, `blameBandAnnotations`, `setBlameStyle`,
  `menuToggleBlameStyle`, `blameStyleToggleLabel`.
- `internal/app/app.go` — `blameBands` field, config apply, ≡ Git row.
- `CLAUDE.md` — menu pins (149 actions / 166 rows / height 172 /
  dividers [2,5,169]) and a Blame bullet under Git.

## 6. Verification

- Tests: editor band fill across wrap rows; userconfig load/save
  round-trip; parser dates; date format; aligned band text; adjacent-run
  hue bump (forced collision); hue stability across re-blame;
  every-line bands annotations; uncommitted no band; visibility of every
  band in every builtin theme (off BG, off wheel neighbour, text ≥ 4.5:1);
  `blameBandFG` leaves Tokyo Night's Text alone; style toggle persists +
  restyles without git + doesn't enable the layer. Menu pin tests
  updated (Git section now 23 rows).
- `make test` (race) green.
- Real binary via run-ced: gitblame.go and app.go with Esc-A — date +
  author on every line, four distinct bands for four commits
  (green/magenta/orange/blue in Tokyo Night; pastel equivalents in
  Solarized Light), uncommitted row `— you` with no band.

## Next

Closed: None. Declined: None. Raised: N-033, N-034.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
