# N-069: the SQL formatter lays out routine bodies

Session: `c3cb07e8-6862-47d9-a796-22790439d497`

## Ask

"Next list item N-069": ced's in-process SQL formatter (sqlfmt.go) did not
wrap long lines and did not look inside `$$` bodies. Asked for scope (sql
bodies only / sql + PL/pgSQL / wrapping / decline); the user picked
**sql + PL/pgSQL bodies**. Wrapping was not picked and is split out as
N-142. Then this wrap (`/sw`).

## The design

**Which dollar strings are bodies** (`routineBodyLang`, sqlbody.go): a pure
function of the token stream. The dollar must be AS's in `CREATE [OR
REPLACE] FUNCTION|PROCEDURE`, or follow `DO` / `DO LANGUAGE x`. The
statement (bounded by `;`, a psql meta-command or COPY data) is scanned for
`LANGUAGE <word|'string'>` OUTSIDE parentheses (a parameter can be named
`language`), before or after the body; DO defaults to plpgsql. Only `sql`
and `plpgsql` qualify. Everything else (C, plperl, plv8, a `$q$` passed
to EXECUTE, `SELECT $$x$$`, a SQL-standard `RETURN $$x$$`) stays data.

**Same contract, one level down.** A body is lexed with the same lexer
(PL/pgSQL uses the core scanner, so boundaries agree), laid out by a
fresh `sqlLayout` (`newSQLLayout(…, base, pl)` + `run()`; `layoutSQL` is
now the (0, false) wrapper), re-lexed and compared. `sameSQLTokens`
RECURSES: a dollar token whose text differs is accepted only when
`routineBodyLang(a, i)` says it is a body AND `sameRoutineBody` (same tag,
bodies token-equal by `sameSQLTokens`). Body-ness is read from the SOURCE
stream alone, so a layout bug cannot vouch for itself, and data dollar
strings are still compared byte-for-byte. A body is best effort: one that
won't lex, holds a `\` meta line / COPY data, has a CR (mixed-ending file),
won't pair parens, or whose PL/pgSQL blocks don't close is kept VERBATIM;
the file around it is still formatted, never refused for it.

**Shape.** Body on the line after the opening delimiter, closer on its own
line, both at the STATEMENT's depth (`l.stack[0].base`, column 0 at top
level), the pg_dump / pg_format shape. The writer now pays the indent on
its very first token (`put` on `w.empty`) and `boundary` breaks to the
statement frame's base, so a layout can start at a depth.

**PL/pgSQL = the SQL layout + a block stack** (`blocks []plBlock`, kinds
declare / begin / exception / if / case / loop). `plCode` runs first in
`code()` and only on the statement frame (never inside parens or an
expression CASE). A block word counts only at a statement's START:
DECLARE, BEGIN (lines up with its DECLARE), EXCEPTION (begin → exception:
WHEN d+1, body d+2), IF / ELSIF / ELSE, statement CASE (arms d+1, body d+2;
its header `CASE x` ends at the first WHEN, which is re-dispatched as a
statement start), WHEN, LOOP, FOR / FOREACH / WHILE, END (pops, the rest
of `END IF;` follows on its line), `<<label>>` (glued, own line). A header
is a statement that ends at THEN / LOOP (`plHead`), which stays on the
header's last line unless the author broke before it or the header held a
laid-out query. `resetStmt` takes `plDepth()` as the statement base;
`plWrite` re-bases the frame at the block word's depth so continuation
lines indent from where the header stands. Malformed (END with nothing
open, `;` inside a header, a `#variable_conflict` option) sets `plBad` →
verbatim.

**Embedded queries.** RETURN QUERY, FOR … IN, OPEN / cursor FOR switch the
statement to clause layout one level in (`plQueryStarts`, `fr.base++`).
`INTO` became a clause after SELECT / RETURNING (`INTO STRICT` keeps
STRICT on its line); INSERT / MERGE INTO never see it (no clause yet).

**Case and spelling.** `l.isKeyword` = SQL keywords + `plIsKeyword`
(ELSIF, LOOP, RAISE, PERFORM, …; QUERY after RETURN, RAISE levels after
RAISE, GET before DIAGNOSTICS/STACKED, TYPE/ROWTYPE after %). Labels are
identifiers (`plIsLabel`: after `<<`, EXIT / CONTINUE / END / END LOOP at
a statement's end), found when the first test re-cased a label `outer`
(a join word). Body keywords join the file's case vote (`sqlCaseVotes`,
recursive), so a lower-case body under an upper-case header comes out
lower-case. `%TYPE` / `%ROWTYPE` glued in `needSpace` (only after an
operand, so `%` can't merge into an operator), top level too. `END CASE`
no longer opens an expression-CASE frame.

## What changed

- `internal/format/sqlbody.go` (new): body detection, split, format,
  comparison, PL/pgSQL block layout, PL/pgSQL keywords + labels.
- `internal/format/sqlfmt.go`: file comment (new "Routine bodies"
  section), recursive `sameSQLTokens`, `sqlCaseVotes`, layout fields +
  `newSQLLayout` / `run`, statement base, `plCode` hook, query switch,
  `spell`, INTO clause, `END CASE`, `%TYPE` glue.
- README (PostgreSQL formatter paragraph), CLAUDE.md (Data formats →
  "Verbatim:" line), next-list.

## Tests

`sqlbody_test.go` (new): full PL/pgSQL function (DECLARE, IF/ELSIF/ELSE,
INTO STRICT, RETURNING INTO, EXCEPTION handlers, `%TYPE`); loops, labels,
statement CASE, nested DECLARE block, RETURN QUERY in a DO; author breaks
(THEN on its own line, blank lines, comments); SQL body; verbatim bodies
(plperl, data string, unpaired END, missing END, `#` option, empty) and
an EXECUTE string untouched inside a laid-out body; case vote; CRLF;
`routineBodyLang`, `splitDollar`, `sameRoutineBody`, `plIsKeyword`,
`plIsLabel`, `sqlIsTypeSuffix`, `sqlStmtStop`, `formatRoutineBody`.
Every layout test is an idempotence test (`mustFormatSQL`).
`sqlfmt_test.go`: the DDL test now expects its body laid out;
`TestSameSQLTokens` covers a re-laid body (same) and a data dollar
string's inner whitespace (different). `go test ./...` passes;
`-race` on the format package passes.

Not done: a real-file check against a project's migrations (only
hand-written fixtures so far).

## Next

Closed: N-069 (routine-body half). Declined: None. Raised: N-142.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
