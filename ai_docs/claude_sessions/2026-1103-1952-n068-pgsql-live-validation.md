# N-068: live PostgreSQL validation for .pgsql / .psql

Session: `353563fd-ae70-4022-9ee3-83dd847004ef`

## Ask

1. From the cats-todo backlog (pasted): next-list item **N-068**. SQL
   files got no live syntax underline because `kindSQL` was formatted
   but never validated: `.sql` is shared with other dialects, and
   MySQL's `\'` escapes and `#` comments lex as unterminated PostgreSQL
   strings. Option given: validate only the unambiguous `.pgsql` /
   `.psql` extensions with the existing lexer and paren matcher, and
   keep the three-verb table in agreement.
2. `/sw`

## Design

- **One kind, not two.** `.sql`, `.pgsql` and `.psql` stay `kindSQL`, so
  the formatter, the external-tool ladder (`BuiltinCommandsFor`) and
  the install offer (`BuiltinHandles`) can't drift apart. A second kind
  (`kindPgSQL`) would have to be remembered at all five `case kindSQL`
  switches. The dialect is asked only by the one verb it changes:
  `sqlDialectKnown(path)` (kinds.go) answers true for `.pgsql` / `.psql`
  (case-folded). Extension knowledge stays in kinds.go.
- **The check is the formatter's own first half.** `validateSQL`
  (new `internal/format/sqlvalidate.go`) runs `lexSQL` and then
  `matchSQLParens`, nothing more. The live underline and Format's
  refusal therefore give the same message at the same position.
  Matching paren pairs is skipped when lexing fails: the token list is
  cut off at the failure, so an "unclosed (" there would be an invented
  second problem (the one-problem rule).
- **No BOM/CRLF handling,** unlike `formatSQL`: the validator reads the
  editor BUFFER, which is always LF-only with no BOM (fileio.go), so
  adjusting for them would shift columns.
- **Positions already point at the cause:** the lexer reports the
  opening quote, comment, `$tag$` or `(`, so unlike JSON/CSV no mark
  needs moving. Columns are counted in runes via `offsetToLineCol`.
- **It is a lexer, not a parser.** `SELEC 1` passes. What it catches is
  the mistake that is hardest to see by eye: a quote or comment that
  never closes and swallows the rest of the file.

## What changed

- `internal/format/kinds.go`: `kindSQL` comment rewritten (formatted
  everywhere, validated only for .pgsql/.psql, and why it stays one
  kind); new `sqlDialectKnown`.
- `internal/format/validate.go`: `Validates` / `Validate` handle
  `kindSQL` through `sqlDialectKnown`, and both ask it, so they agree
  even for a caller that skips the cheap predicate. New exported
  `NotValidatedReason(path)`: a reason string for `.sql` and `""` for
  every other file.
- `internal/format/sqlvalidate.go` (new): `validateSQL`.
- `internal/app/validate.go`: when ≡ "Validate file" has nothing that
  can check a `.sql` file, it now flashes the dialect reason, which
  names `.pgsql` and Format file. The generic message now reads "JSON,
  CSV and PostgreSQL (.pgsql / .psql)". Header comment updated.
- Docs: README (the PostgreSQL formatting bullet gains the as-you-type
  sentence and why `.sql` is excluded; the tab-menu and ≡ reference
  "Validate file" descriptions), CLAUDE.md Data formats rule (it said
  "never validated live").

## Tests

- `sqlvalidate_test.go` (new): valid PostgreSQL stays quiet (doubled
  quotes, `E''`, a `$$` body with quotes and parens, nested comments,
  psql vars and meta-commands, a COPY data block); empty file; each
  mistake marked at its cause (string, comment, `$fn$`, unclosed `(` at
  `;`, unmatched `)`, backtick, `{{` template); rune columns; a lex
  error hides the paren check; **validator ⇔ formatter agreement** on
  message and 1-based position.
- `kinds_test.go`: the test that checks the three verbs agree now
  covers .pgsql/.psql (same formatting rungs and tool ladder length as
  .sql; validator and formatter agree on broken input); new
  `TestSQLDialectKnown_OnlyUnambiguousExtensions`.
- `validate_test.go` (format): .sql in the "files it does not own"
  list; .sql/.pgsql/.psql in `TestValidates_MatchesValidate`; new
  `TestNotValidatedReason_ExplainsOnlySQL`.
- `validate_test.go` (app): `TestValidate_PgSQLIsMarkedButSQLIsNot`
  (the same MySQL-style text is marked in .pgsql/.psql and quiet in
  .sql); `TestValidateFile_SQLExplainsTheDialectCall`.
- `go test ./...` and `make test` (race) pass; vet clean.

## Real-binary check (run-ced)

A scratch project held `fn.pgsql` and `dump.sql` with the same text
(`select (a, b from t;` / `select 'it\'s' from t;`). In `fn.pgsql`,
line 3 showed `◇` plus the caret note `✗ unterminated string`. Lexing
failed first, so the unclosed paren on line 2 was correctly not
reported. `dump.sql` showed nothing.

## Also committed

`next-list.md` already had the user's own edits from before this
session (N-005 and N-043 moved to Roadmap, N-006 declined into
Non-goals). They ride in this commit unchanged.

## Next

Closed: N-068. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
