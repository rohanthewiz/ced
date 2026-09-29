# Session: Overflow markers on compare, problems, chat, terminal

Session ID: 89fd7a38-d901-44ec-b839-af7132393784
Date: 2026-09-29

## 1. The prompt

From the cats-todo backlog — next-list item N-018:

> Overflow markers on the surfaces that have none — compare panel, problems
> panel, chat, terminal. The click comes along for free via `scrollAt`.

## 2. What already existed

`overflow.go` enumerated markers (`overflowMarkers`, the ONE source for
draw, hit-test and popup) for the editor, both git panels, the git log,
the Find-all list and the tree. `scrollAt` (app.go) already routed the
wheel to all four target panels by rect, and `overflowMarkerClick` hands
its delta to `scrollAt` — so the click really did need no code.

## 3. Geometry per panel

All four panels paint their body one column in from each side (text from
px+1 / px+2, truncated short of px+pw-1), and `toolRect` already excludes
the dock seam, so `px+pw-1` is a blank margin on every one — the marker
covers nothing (the git diff pane's situation).

| Panel | Viewport | Total | Unit |
|---|---|---|---|
| Compare | rows py+1 … py+ph-1 (`compareClampScroll`'s ph-1) | `len(compare.lines)` | line |
| Problems | `problemsVisibleRows()` under the header | `len(problems.view)` (filtered) | problem |
| Chat | ph-1 − composer rows − attachment rows | `chatContentRows()` | row |
| Terminal | ph-2 (header + input row) | `termContentRows()` | line |

- Chat's band is recomputed raw rather than read from `chatVisibleRows`,
  which floors at 1 for the scroller; a panel whose composer ate the band
  gets no markers. Unit is "row" because the transcript is wrapped (the
  markdown preview's rule).
- Terminal counts "line": the strip truncates, it doesn't wrap.

## 4. Coloring decisions

- **Problems is colored** by the hidden rows' severities
  (`problemsOffscreen`, reusing `countDiag` + `lspOffscreenKind`), so a
  red marker there means what a red marker on the editor means. Justified
  because rows sort by PATH, not rank — "an error further down" is news.
  Contrast Find-all, where offFind would only repeat the title. Only
  off-screen rows count; an on-screen error doesn't tint the marker. The
  helper walks the view instead of subtracting, so a stale scroll floors
  at zero.
- **Terminal stays plain** even over stderr: stderr is progress chatter as
  often as failure; red would cry wolf on every `go test -v`.
- Compare and chat: plain counts.

## 5. Tests (overflow_test.go)

- `panelMarkerPair` helper: no ▴ unscrolled, ▾ with the hidden count /
  unit / page, then scrolled to the end the pair swaps.
- `TestOverflowMarkers_ComparePanel`, `_ProblemsPanel` (incl. filtered
  view that fits → none), `_ProblemsColoredBySeverity` (on-screen error
  ignored, hidden warning wins; scrolled past it the ▴ carries the error;
  tip reads "5 problems below" + severity line), `_ChatTranscript`
  (count taken from `chatContentRows` — chatRows adds copy-button action
  rows, so 20 message lines derive 23 rows; band never reaches the
  composer), `_TerminalScrollback` (plain over stderr, not on the input
  row, and actually PAINTED after `a.draw()`).
- `TestOverflowClick_PagesThePanels`: real `handleMouse` press on each ▾
  pages by visible-1 and is claimed (no focus change, no file opened).
- None skip in the default fixture. `make test` green.

## 6. Verified in the real binary

`run-ced` capture: terminal opened (Esc-`), `ls internal/app` → ▴ at the
scrollback's top-right; three wheel-ups → both ▴ and ▾ drawn.

## 7. Docs

README "Overflow markers": surface list and unit list extended, Problems'
severity coloring noted. overflow.go header lists the new surfaces.

## 8. Loose end

`gofmt -l internal/app` flags `hovermodal.go` — pre-existing, untouched
this session.

## Next

Closed: N-018. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
