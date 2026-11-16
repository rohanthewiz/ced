# Struct-tag spell check — misspelled words in Go struct-tag names, cats-todo style

Session: `42da6470-b3dd-420f-a3c4-b774775a33f3`

## Ask

"I would like some kind of spellcheck on json tag names similar to the
spell check feature of the prompt editor in CatsTodo (../cats-todo)."

cats-todo's feature (its `internal/spell` + `spell.go` / `spellpanel.go`):
an embedded SCOWL en_US list (~90k forms, 250 KB gzipped) + `extra.txt`
of software vocabulary, a prose tokenizer that skips code, red underlines,
and a Spelling panel (ranked corrections via weighted OSA, add to a
global / project `dictionary.txt`, toggle off).

**Decisions (asked):**

- **Scope: all Go struct-tag keys** — json, yaml, xml, toml, db, bson,
  form, mapstructure, env… — chosen over json only and over "Go tags +
  keys in .json files".
- **Dictionaries: plain text files, like cats-todo** —
  `~/.config/ced/dictionary.txt` + `<root>/.ced/dictionary.txt` — chosen
  over bytdb (history.bytdb table + config.json) and over global-only.
  `.ced/.gitignore` lists only `history.bytdb*`, so the project list is
  committed beside `format.json`. CLAUDE.md's allowed-files list gained
  `dictionary.txt` in both homes.

## The design

**internal/spell** (new; adapted copy of cats-todo's package, which is
`internal/` and can't be imported):

- `spell.go` — `Dictionary` with TWO tiers: `words` (SCOWL + extra.txt +
  ident.txt + user lists → `Known`, case-lenient as cats-todo) and
  `trusted` (ident.txt + user lists, lowercase keys). `Load` DEGRADES:
  an unreadable user file costs only its words, returned with
  `errors.Join` beside a working dictionary (cats-todo refuses). `New`
  for tests. The prose `Check` was not copied.
- `suggest.go` — cats-todo's weighted OSA + ranking unchanged; the scan
  split out as `candidates` (uncapped, ranked) so the identifier side can
  filter before capping.
- `userdict.go` — `Add` (also TRUSTS the word) + `AppendWord` (header,
  case-insensitive dedupe, unfinished-last-line newline).
- `structtag.go` — `TagNames(line []rune)`: pairs backticks on one line;
  a span counts only if ALL of it parses as `key:"value"` pairs (the
  `reflect.StructTag.Lookup` scan) — the guard against a multi-line raw
  string misaligning the pairing. Name = value up to the first comma,
  only when identifier-shaped (ASCII letters/digits/`_-.`, ≥1 letter):
  `-`, `,omitempty`, gorm's `column:x;type:…`, `gte=0`, prose, patterns,
  escapes, non-ASCII are never read. Tag-shaped text in comments IS read.
  Line scanner, not go/scanner: per-frame over visible lines.
- `ident.go` — `identPieces`: letter runs cut at camel boundaries
  (`userName`, `HTTP|Server`, plural acronym `IDs`/`URLsByHost` kept
  whole); skipped: ≤2 letters, acronyms, a PIECE touching a digit or
  non-ASCII (`lenghtUtf8` still flags `lenght`); SCREAMING names
  (no lowercase) check pieces ≥5 letters (`DATABSE_URL`). Unknown
  pieces pass as COMPOUNDS (word-break DP, ≤4 parts): each part a list
  word ≥4 letters or a trusted word of any length; triple letters never
  pass. Rationale found by probing the list: `pas`, `con`, `sep`, `rec`,
  `com`, `ant` are all SCOWL words, so a 3-letter rule accepts
  `pasword` / `existant`. `SuggestPiece`: `identRank` over
  `candidates` — drops capitalized entries (proper nouns, acronyms),
  non-letters, <3 letters; keeps cost order but within a cost puts
  dropped-letter supersequences first (`usr`→user, `frst`→first: the
  prefix tie-break had put `US, USA, USB…` and `frat` first); then
  `halfRepairs` for run-togethers (`frstname`→firstname); every
  candidate must itself pass; `Recase` to the piece's shape.
- `ident.txt` (new data): tag options (omitempty, inline, chardata…),
  connectors (at, by, id, ids, is, key…), field abbreviations (addr,
  cfg, ctx, dsn, src, dst, pwd, req, resp, uid, sku…). All TRUSTED.
- `en_US.txt.gz`, `LICENSE-SCOWL.txt`, `extra.txt` copied verbatim.

**app/spellcheck.go** (new): `spellSource` DecorationSource —
underline + `DiagWarning` ink, NO gutter mark, registered in `wireTab`
git < spell < validate < plugin < LSP; NOT fed to `diagsFor` (would fill
Problems, answer the gutter tooltip by line, block the hunk tip).
Per frame, visible lines, byte-search for a backtick first, nothing
cached. The piece with the primary caret at its END is not painted
(typing); clicks INTO a word keep it. Lazy async load: `spellAfterEvent`
(check on + active `.go` tab) → goroutine (`defer crash.Recover()`) →
`spellDictEvent`, seq-checked, 30s lost-answer relaunch, inline when no
screen; a nil dictionary latches `dead` (re-picking the ≡ row clears
it). `spellAfterSave` reloads when a dictionary file is saved — beside
`themeAfterSave` in `saveTabAt` AND in `autoSaveTab` (auto-save is how a
hand edit usually lands; added after review). Seams: `spellDictPathFn`,
`spellConfigPathFn`, `spellLoadFn`.

**app/spellfix.go** (new): `openPicker` titled `Spelling · <word>`
(+ "— closest 8" / "— no close words"); rows = corrected full NAMES,
`Add "<lower>" to my dictionary`, `… to project dictionary`,
`Stop spell-checking struct tags`. A pick re-reads the captured columns
(refuses + flashes if moved) then `Tab.ApplyMultiEdit` (one undo step),
caret after the word. Add = `AppendWord` then `dict.Add`. Held load
error flashed when the picker opens.

**Doors:** editor right-click `Fix spelling of "<word>"…` prepended with
a split, only on a flagged word (typing exemption ignored); ≡ Code
`Fix spelling at caret…` (after Previous problem; word at caret else
nearest on the line; every refusal flashed); ≡ View toggle
`Spell-check struct tags` / `Stop spell-checking struct tags` after the
diag-note row (below the fold). Config key `"spellcheck"` (default on;
off in a directly built App).

## What changed

- New: `internal/spell/` (5 .go + tests, 4 data files),
  `internal/app/spellcheck.go`, `spellfix.go` + tests.
- `internal/userconfig/userconfig.go`: `Config.SpellCheck`,
  `fileFormat.SpellCheck`, parse, `SaveSpellCheck`, `DictionaryPath`.
- `internal/app/app.go`: `App.spell`, config load, event case,
  after-event hook, wireTab source, saveTabAt hook, two ≡ rows.
  `autosave.go`: hook. `contextmenu.go`: the prepended row.
- `app_test.go`: newTestApp pins the three seams (loader = empty stub);
  menu pins 193→195 actions, 210→212 rows, 252→254 height, dividers
  `[2,5,251]`, WithCustomActions 257, both ModalRect heights 254.
  `contextmenu_test.go`: 5th fixture state (flagged tag word).
- README: `### Spelling in struct tags`, ≡ View / ≡ Code rows (Code's
  "needs a language server" note now excepts the row), Editor
  right-click row, config row. CLAUDE.md: map entries, a design rule
  under Ambient highlights, allowed-files list, menu pins, and the stale
  Git fold count fixed (35/43 → 36/44).

## Tests

- `internal/spell`: splitter table (camel/Pascal/snake/kebab/dotted,
  acronym+word, SCREAMING, digit-adjacent piece, `pasword` / `existant`
  / `messsage` rejected, connectors), quiet table (conventions,
  acronyms, digits, run-togethers, header names), offsets, camel
  boundaries, compound two-tier rule + part cap, `Recase`,
  `SuggestPiece` (case, letters-only, half repairs, every candidate
  passes), `TagNames` (all keys, non-names, strictness guard, rune
  columns), Load tiers + degradation; cats-todo's Suggest / Add /
  AppendWord tests carried over.
- `internal/app`: spans + style + no gutter mark; quiet off/.txt/no
  dict; typing exemption; real draw (underline + DiagWarning) and LSP
  error wins the cell; hit-near; lazy async load (no load for .txt /
  off, one in-flight launch, answer installs); stale seq dropped; held
  error + dead latch + ≡ retry; save and auto-save reload; toggle
  persists via the seam; loadUserConfig default on; picker rows/title
  with the real list; fix in one undo step; stale-range refusal; both
  Add rows write + clear; off row; ≡ row's four flashed refusals;
  right-click row only on a flagged word.
- `internal/userconfig`: `TestSpellCheckKey`, `TestDictionaryPath_…`.
- `make test` (race) green, run alone. One run that overlapped a
  second race suite timed out `TestOpenFinder_PopulatesResults`
  (2s deadline); it passed 3× alone and in the clean full rerun.
- Real binary (run-ced): a fixture struct marked exactly
  `usr`+`Nmae`, `emial`, `adress`, `DATABSE` (not `id`, `user_name`,
  `createdat`, `apiKey`, `API_KEY`, `sha256Sum`, the gorm value);
  right-click → `Fix spelling of "emial"…` leads the menu; picker
  `email, emails, mail…` + add rows; picking `email` fixed the tag and
  cleared the mark; Add "usr" wrote `.ced/dictionary.txt` with header and
  `Nmae` stayed flagged. The capture emulator records colour, not the
  underline attribute.
- Contrast probe after the work: on every built-in, warning and string
  hues differ, but on gruvbox-dark (#b8bb26 vs #fabd2f) and super-warm
  (#b6c25e vs #ffcf6b) they sit close in hue and luminance (1.2–1.3:1),
  so the underline carries the mark there — N-145.

Not committed in-session until this wrap.

## Next

Closed: None. Declined: None. Raised: N-145, N-146, N-147, N-148.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
