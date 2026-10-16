# 2026-10-16 16:51 — PostgreSQL formatting

Session: `4d845357-3b29-4b3f-879a-b579e3df8998`

## Request

From the cats-todo backlog: "Add formatting for Postgres SQL".

## What was built

A new data kind, `kindSQL` (`.sql`, `.pgsql`, `.psql`), on the same
three-step ladder as JSON, behind ≡ File → Format file (Go stays the
only format-on-save kind).

- **Kinds table** (`internal/format/kinds.go`): `kindSQL` is FORMATTED
  but not VALIDATED. A live underline would misfire on other dialects
  that share `.sql` (MySQL backslash escapes and `#` comments lex as
  unterminated PostgreSQL strings); the formatter's positioned refusal
  is the answer instead. `BuiltinHandles` claims SQL, so the
  format-defaults install offer stays quiet.
- **External rung** (`internal/format/builtin.go`, `sqlCommands`):
  repo-local `node_modules/.bin/sql-formatter` → `pg_format` on PATH →
  global `sql-formatter`. Both tools look for config from the process
  CWD (which grsh moves), so a root config is passed explicitly with
  `-c` (`<root>/.pg_format`, `<root>/.sql-formatter.json`). A per-user
  pg_format config (`~/.pg_format`, `$XDG_CONFIG_HOME/pg_format/
  pg_format.conf`) means no flags. Otherwise the file's style is
  passed: pg_format `--tabs` / `--spaces N` / `--keyword-case 1|2`;
  sql-formatter `-c '{"language":"postgresql","tabWidth":…,
  "useTabs":…,"keywordCase":…}'`. Flags were checked against both
  projects' READMEs (WebFetch); neither binary is installed here.
  `isExecutable` was factored out of `resolveTool` for reuse.
- **Lexer** (`internal/format/sqllex.go`): follows scan.l. Handles
  `''` and E-string escapes, prefixed strings (B/X/N/U&), quoted
  identifiers, `$tag$` dollar quotes vs `$1` params, nested block
  comments, `--` / `/*` cutting an operator run, the trailing-`+`/`-`
  operator trim (`a<-1` → `<`, `-`, `1`), PG16 number forms with glued
  junk kept in the token, `::`, `:=`, psql variables (`:x`, `:'x'`,
  `:"x"`), backslash meta-commands to EOL, and `COPY … FROM stdin;` /
  `\copy … from stdin` data as one verbatim token through `\.`.
  Refuses backticks (MySQL) and braces (templates / dbt) with
  positions. On error it returns the partial tokens, which style
  sampling uses.
- **Layout** (`internal/format/sqlfmt.go`): token-preserving.
  - Only whitespace and keyword case change, and this is ENFORCED: the
    output is re-lexed and compared with `sameSQLTokens` (words
    case-insensitive). Any difference is reported as an internal error
    and nothing is written.
  - A frame stack (stmt / subquery / list / inline / case) drives
    clause layout for DML, `… AS SELECT` and EXPLAIN: clause keywords
    on their own line with bodies one level in, one item per top-level
    comma. `AND`/`OR` break in WHERE/HAVING and join ON conditions
    (BETWEEN's AND stays). Joins sit under FROM; subqueries and CTEs
    become blocks; CASE breaks into WHEN/ELSE lines unless it is inside
    a call. `DO UPDATE SET` stays on one line. `CREATE TABLE` gets one
    column per line.
  - Other statements keep the author's line breaks, with continuation
    lines one level in.
  - Line breaks are written LAZILY by `sqlWriter`, so a trailing
    comment stays on the line it trailed.
  - Adjacent string literals keep a newline between them (in Postgres
    that newline is what makes them one literal).
  - Indent unit and keyword case come from the file (`DetectIndent`, a
    keyword vote). With no evidence: 4 spaces and upper case.
  - Only `sqlKeywords` (reserved words plus words rarely used as column
    names) are re-cased. `sqlContextKeywords` covers words that need a
    neighbour (`PRIMARY KEY` vs a `key` column). Function-like keywords
    followed by `(`, and names after `.`, `::`, FUNCTION or PROCEDURE,
    are left as written.
  - CRLF and BOM files round-trip; empty files are unchanged.
  - Unbalanced parentheses are refused at the paren that's at fault.
- **In-process dispatch** (`inprocess.go`): `formatSQL` behind
  `InProcessFormat`, `FormatsInProcess`, and `InProcessName` "sql"
  (flash label "sql formatter").

## Verification

- `make test` (race) green.
- New tests: `sqllex_test.go`, `sqlfmt_test.go` (golden layouts, each
  also checked for idempotence), SQL cases in `builtin_test.go`
  (order, style flags, config handoff, with HOME/XDG isolated),
  `kinds_test.go` (classification, three-verb agreement, not on save),
  `inprocess_test.go`, and app end-to-end
  `TestRunFormatter_InProcessFormatsSQL` /
  `…RefusesBrokenSQL`.
- Corpus run (scratch test, deleted afterwards) over 400 `.sql` files
  under `~`: 394 formatted and stable on a second pass, 0 safety-net
  failures. Refused: 2 with NUL bytes, 1 with literal `>`
  escapes (unclosed paren, a correct refusal). 3 were unreadable.

## Docs

README Features bullet + a `PostgreSQL` entry under `## Formatting`;
CLAUDE.md "Data formats" gained the kindSQL rules (keep the re-lex
check).

## Next

Closed: None. Declined: None. Raised: N-067, N-068, N-069.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
