# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Introspect nine previously-unsupported object types (tablespaces, foreign data
  wrappers, foreign servers, user mappings, publications, event triggers,
  collations, casts, extended statistics), so `dump`/`verify`/`plan --live`
  cover them. Reconstructed DDL is canonicalised through pg_query to match the
  compiler's output. Extension-owned and system objects are filtered out.
- Introspect seven further object types — operators, operator families, operator
  classes (with their full `OPERATOR`/`FUNCTION` member list), text-search
  parsers, templates, dictionaries, and configurations — completing catalog
  coverage for the opaque tier. Previously `plan --live` proposed a spurious
  `CREATE` for each of these because introspection returned nothing. Operator
  class/family members are scoped by their internal `pg_depend` link to the
  class, so family-level members added later by `ALTER` are not mis-attributed;
  operator families auto-created for a class are skipped.
- `dump` now writes database-scoped schemaless objects to `<db>/objects.dpg`
  (previously misfiled under the cluster roles file) and can emit these object
  types as DPG declarations.
- Indexes now support `WITH (...)` storage parameters and `NULLS NOT DISTINCT`
  end to end: parsed in hand-written source, introspected from the live
  catalog (via `pg_get_indexdef`, so it works on any supported PG version
  without a version-gated catalog column), emitted in `CREATE INDEX` SQL, and
  rendered back by `dump`. `WITH` was previously parsed into the IR (`idx.With`)
  but silently dropped everywhere downstream; `NULLS NOT DISTINCT` wasn't
  represented at all.
- `INDEX name (...)` (Mode B, RFC §4.8's singular-keyword form) now parses for
  hand-written indexes. Previously the parser routed both `INDEX` and
  `INDICES` to the same brace-requiring block parser, so the singular form was
  a hard parse error (`expected '{', got '('`), not just silently unsupported.
  Mode A (`INDICES { ... }`) is unchanged and the two may be freely mixed, as
  the RFC requires.
- CI now enforces a 75% `internal/diff` coverage floor (`.github/workflows/ci-dpg.yml`),
  guarding against a regression back toward that package's pre-coverage-push
  baseline (67.1%) rather than tracking a specific target. The last four
  0%-covered functions left over from the earlier targeted coverage pass
  (`op.Pos`, `ptrStr`, `int64PtrEq`, `compositeAttrsChanged` — all trivial
  helpers with no prior gap in correctness) now have unit tests, bringing the
  package to 80.8%.

### Fixed

- **Introspection silently dropped `OUT` columns and lost `INOUT`/`VARIADIC`
  mode keywords for any function using them** — `introspectFunctions` built
  `Args` purely from `oidvectortypes(proargtypes)`, which — like
  `pg_get_function_identity_arguments` — only ever reports `IN`/`INOUT`/
  `VARIADIC` argument *types*, with no mode or name information at all, and
  `OUT` arguments are invisible to it entirely. A plain `OUT`-only
  function's `OUT` columns were completely missing from introspected
  `Args` (the same severity as the `RETURNS TABLE` bug above); an `INOUT`
  or `VARIADIC` function's argument type was captured but its mode
  keyword was silently lost, so `dump`/diff-generated SQL would recreate
  it as a plain `IN` argument — a different, non-equivalent declaration
  for `VARIADIC` in particular, since it changes callable arity. Fixed by
  widening the `RETURNS TABLE` fix's existing raw-catalog-array query
  (`unnest` over `proargmodes`/`proargnames`/`proallargtypes`) from
  TABLE-mode-only to any function where `proargmodes IS NOT NULL` —
  PostgreSQL's own signal that at least one argument uses a non-default
  mode — covering `OUT`/`INOUT`/`VARIADIC`/`TABLE` all at once. The
  overwhelming common case (plain `IN`-only functions, where `proargmodes`
  is `NULL`) is untouched, using the original, unmodified path. Also fixed
  two query bugs surfaced by this widening specifically: the query lacked
  the schema exclusion every other introspection query has, so it also
  matched PostgreSQL's own catalog functions (many of which use `OUT`/
  `VARIADIC` with unnamed arguments), crashing on a null scan; and an
  unnamed argument is a real, valid construct for a *user* function too
  (`proargnames` comes back entirely `NULL`, not per-position empty
  strings), handled with a nullable scan.
  Scope note: a genuinely separate, pre-existing bug was found (not fixed)
  while building this fix's test fixture — a function declared with no
  explicit `RETURNS` clause at all (valid PostgreSQL when the signature
  has `OUT`/`INOUT` parameters, since the return type is then implied)
  produces a bare `RETURNS  LANGUAGE ...` (empty type) from
  `buildFunctionSQL`/`dump`, a real syntax error on `apply`. Unrelated to
  this fix's Args-mode-capture scope; worked around in the new test with
  an explicit `RETURNS` clause, flagged for a future fix.
- **A `FUNCTION` with no explicit `RETURNS` clause produced invalid SQL and
  a permanent drift loop** — PostgreSQL allows omitting `RETURNS` entirely
  when the signature has at least one `OUT`/`INOUT` parameter (the return
  type is then implied: a single `OUT`/`INOUT` parameter's own type, or
  `record` when there's more than one — confirmed live against
  postgres:17). `pg_query` performs no semantic analysis, so
  `CreateFunctionStmt.ReturnType` is simply `nil` for this form, and
  `ir.Function.ReturnType` stayed the zero value — `buildFunctionSQL`
  unconditionally rendered `"RETURNS " + ReturnType.String()`, producing a
  bare `RETURNS  LANGUAGE ...` (empty type), a real syntax error on
  `apply`. Fixing only the rendering (e.g. omitting `RETURNS` from the
  generated SQL to match source) would have left a second, subtler bug:
  the live-introspected snapshot's `ReturnType` is always concrete (PG
  computes and stores a real type at `CREATE` time, and `pg_get_functiondef`
  always reconstructs it explicitly), so an empty offline `ReturnType`
  could never match it — every `verify`/`plan --live` would show a
  permanent, self-inconsistent `DROP FUNCTION` + `CREATE FUNCTION` for the
  function forever. Fixed at the source: `ir.Builder` now computes the
  same implied type PostgreSQL itself would (new `impliedReturnType`,
  internal/ir/builder.go) when `RETURNS` is omitted, so both the generated
  `CREATE FUNCTION` SQL and the offline-vs-live diff comparison are
  correct and mutually consistent. A signature with zero `OUT`/`INOUT`
  parameters and no `RETURNS` clause is itself invalid PostgreSQL
  independent of this fix ("function result type must be specified");
  DPG doesn't add its own validation for that case and simply passes it
  through to fail on `apply`, same as before.
- **`FUNCTION`'s `RETURNS TABLE (...)` produced invalid SQL and lost its
  output columns on introspection** — a TABLE-mode argument (`ir.FuncArg`
  with `Mode: "TABLE"`) was already parsed correctly from source, but
  `buildFunctionSQL`/`dump`'s parameter-list rendering wrote it inline as a
  literal `TABLE a integer` mode-prefix in the main parens — not valid
  PostgreSQL syntax (`RETURNS TABLE(...)` columns belong in a separate
  clause) — so any hand-written `RETURNS TABLE (...)` function failed to
  apply with a real parse error. Separately, introspection built a
  function's `Args` purely from `oidvectortypes(proargtypes)`, which — like
  `pg_get_function_identity_arguments` — never reports `OUT`/`TABLE`-mode
  parameters at all, so a live `RETURNS TABLE` function's output columns
  were silently missing from introspected `Args` entirely, not merely
  mis-rendered; `dump`ing such a function produced a broken definition with
  no columns. Fixed by skipping `TABLE`-mode args in the main parameter-list
  rendering (both `dump` and diff-generated SQL) and rendering them inside a
  proper `RETURNS TABLE(col type, ...)` clause instead (new
  `ir.FuncTableColumns`/`ir.FormatTableColumns` helpers, shared by both
  rendering and the new comparison below), and by adding a second, narrowly
  scoped introspection query (via `unnest(proargmodes, proargnames,
  proallargtypes)`, filtered to functions whose `proargmodes` actually
  contains `'t'`) that reconstructs the full, correctly-moded `Args` list
  for TABLE-mode functions specifically — every other function keeps using
  the existing, unmodified introspection path, so this carries no
  regression risk for the common case.
  Also fixes a related diffing gap: a `RETURNS TABLE` function's
  `ReturnType`/`SetOf` are always `record`/`true` regardless of its actual
  column list (that's genuinely how PostgreSQL's own catalog represents
  it), so two functions with different `TABLE` column lists looked
  identical to the SETOF work's return-type comparison. Added a separate
  `SnapFunction.ReturnTable` field (the column list's comparable text) so a
  column-list-only edit — verified live to require the same `DROP FUNCTION`
  + `CREATE FUNCTION` pattern as any other return-type change — is
  correctly detected as drift instead of silently ignored.
  Scope note: plain `OUT`/`INOUT`/`VARIADIC`-only functions (no `TABLE`
  mode) are unaffected by either fix and were already rendering correctly;
  introspection's handling of `VARIADIC`'s keyword and plain `OUT`-only
  functions' `Args` was not investigated as part of this item and may carry
  its own separate gaps, not confirmed either way.
- **`FUNCTION`'s `RETURNS SETOF <type>` was silently dropped everywhere** —
  pg_query's `TypeName.Setof` field was never read anywhere in the codebase
  (`typeNameToRef` ignored it), so a function declared with `RETURNS
  SETOF integer` in DPG source was actually created live as a plain scalar
  function, introspection never reported whether a live function was
  set-returning, and `dump` never reconstructed `SETOF`. This also meant
  `ROWS` (only valid together with `SETOF` — PostgreSQL rejects it on a
  scalar function with "ROWS is not applicable when function does not
  return a set") could never actually be exercised through DPG-compiled
  source, only introspected, a scope limit this project's own ROWS work
  had to document and work around. Fixed by adding `SetOf bool` to
  `ir.TypeRef` (populated generically in `typeNameToRef` from pg_query's
  field — the grammar only ever sets it true when parsing a function's
  return type, so every other `TypeRef` consumer such as columns, casts,
  and function arguments is unaffected), introspecting it from
  `pg_proc.proretset`, and rendering `SETOF` in both `dump` and
  diff-generated SQL. Also fixes a genuinely separate, pre-existing gap
  found while wiring this up: a function's return type was never compared
  in `diffFunction` at all, so ANY return-type-only change (`SETOF` or
  otherwise) silently went undetected. Verified live against PostgreSQL 17
  that `CREATE OR REPLACE FUNCTION` rejects a return-type change outright
  ("cannot change return type of existing function", hinting to
  `DROP FUNCTION` first) — a genuine return-type change (including toggling
  `SETOF`) now diffs to a `DROP FUNCTION` + `CREATE FUNCTION` pair, the same
  DROP-required pattern already used for a materialized view's query
  change, rather than the invalid in-place `CREATE OR REPLACE`.
  Scope note: `RETURNS TABLE (...)` and plain `OUT`-parameter-based
  set-returning functions are a distinct PostgreSQL mechanism (parameter-list
  based, not `TypeName`-based) with a separate, pre-existing rendering bug
  in the function parameter list — not addressed here, and not made worse by
  this fix (confirmed live: an ordinary composite-`OUT`-parameter, non-`SETOF`
  function is unaffected, since PostgreSQL's own grammar never sets `Setof`
  true for that case).
- **`FUNCTION`'s `PARALLEL`/`COST`/`ROWS` attributes were parsed nowhere,
  introspected nowhere, and never diffed or rendered** — `ir.FuncAttrs`
  already had `Parallel`/`Cost`/`Rows` fields, but nothing populated them,
  so DPG silently dropped these clauses from hand-written source, never
  detected drift when they changed live, and `dump` never reconstructed
  them. Verified live against PostgreSQL 17 that `procost` defaults to `1`
  for `internal`/`c`-language functions and `100` otherwise, `prorows`
  defaults to `0` for a scalar function and `1000` for a set-returning one,
  and `proparallel` defaults to `'u'` (UNSAFE) — introspection now suppresses
  each attribute when it merely matches that computed default, the same
  suppress-when-default treatment already used for column `STORAGE`, so an
  ordinary function doesn't grow a noisy `COST 100` on every dump. Fixed by
  parsing `COST`/`ROWS`/`PARALLEL` in `extractFuncAttrs`, introspecting
  `proparallel`/`procost`/`prorows`/`proretset` with the live-default
  comparison above, diffing them (an explicit desired value differing from
  the snapshot's now correctly triggers `CREATE OR REPLACE FUNCTION`, while
  an *unspecified* desired value never counts as drift even though the
  snapshot always carries PostgreSQL's own concrete catalog value), and
  rendering `PARALLEL`/`COST`/`ROWS` in both `dump` and diff-generated SQL
  in PostgreSQL's documented clause order. `PROCEDURE` is intentionally
  unaffected — PostgreSQL doesn't support these attributes on procedures.
  Scope note: `ROWS` is only valid on a set-returning function
  (`RETURNS SETOF ...`/`RETURNS TABLE (...)`), and DPG's IR has no `SETOF`
  representation yet (a separate, pre-existing gap, not addressed here) —
  `ROWS` is fully wired and unit-tested end to end regardless, and its
  introspection-side suppression is verified live against a `SETOF`
  function created via raw SQL.
  **Correction (caught by an independent verification pass, same day):** the
  initial fix compared `PARALLEL` with a bare inequality on the assumption
  that, unlike `COST`/`ROWS`, it's always a concrete value on both sides with
  no "unspecified" ambiguity — true for the desired side (the IR builder
  always defaults unmentioned `PARALLEL` to `"UNSAFE"`) but not for the
  snapshot side: any `snapshot.json` written before this field existed has no
  `parallel` JSON key at all, so unmarshalling leaves it at the Go zero value
  `""`. Reproduced live: diffing an ordinary function (source never mentions
  `PARALLEL`) against exactly such a pre-upgrade snapshot produced a spurious
  `CREATE OR REPLACE FUNCTION` — for every function in every existing
  project, on the very first `plan`/`apply` after upgrading, directly
  affecting the offline `plan`/`apply` path this project's design treats as
  the primary workflow. Fixed with a new `parallelChanged` helper that treats
  an empty snapshot value as "unknown, don't diff yet" rather than a genuine
  `UNSAFE`, self-healing after the first apply records a real value (the same
  transitional-pain class already documented for the Operator `QualifiedName`
  change). Confirmed the bug reproduces before this fix and disappears after,
  via a new regression test.
- **An inline, unnamed `EXCLUDE` constraint produced a self-inconsistent
  `DROP CONSTRAINT` + `ADD` pair on every single `verify`/`plan --live`
  run**, the same class of bug already fixed for unnamed `PRIMARY
  KEY`/`UNIQUE`/`FOREIGN KEY`/`CHECK`. Verified live that `EXCLUDE`'s
  default name follows the exact same pattern as `UNIQUE`
  (`table_col1_col2_excl`, every element's column name joined by `_`) and
  goes through PostgreSQL's `ChooseRelationName` — the same schema-wide,
  `pg_class`-scanning collision path `PRIMARY KEY`/`UNIQUE` use, not the
  narrower constraint-name-only path `FOREIGN KEY`/`CHECK` use (confirmed
  live: a plain table coincidentally named like an `EXCLUDE`'s predicted
  name forces PostgreSQL to fall back to a `1`-suffixed name, exactly the
  existing `PRIMARY KEY`/`UNIQUE` relation-name-collision case). Fixed by
  extending the existing PK/UNIQUE/FK/CHECK auto-naming reconciliation to
  cover `EXCLUDE` too, deriving name2 from every element's column name in
  source order.
  Scope: a plain column element contributes its own name; a bare,
  top-level function-call element (e.g. `EXCLUDE ((lower(a)) WITH =)`)
  contributes its function's name (schema-qualification stripped, matching
  `get_func_name`'s own behavior) — confirmed live across single-arg,
  multi-arg, nested, and schema-qualified calls that PostgreSQL's real
  algorithm uses ONLY the bare function name for such an element, with no
  dependence on its arguments at all, so no catalog/OID lookup is actually
  needed to predict it. Two elements that independently derive the same
  name (e.g. two `lower(...)` calls on different columns) get PostgreSQL's
  own per-element disambiguating suffix (`lower`/`lower1`, confirmed
  live), which this reconstructs too.
  **Correction (caught by an independent verification pass, same day):**
  the original version of this fix excluded ALL expression-based elements
  outright, reasoning that PostgreSQL's real naming algorithm
  (`ChooseIndexExpressionName`) needs a resolved function OID
  (`get_func_name(FuncExpr.funcid)`) unavailable to an offline, syntax-only
  parser. That premise doesn't hold for the common case: `get_func_name`
  only ever returns the bare, already-known function name — never
  schema-qualified, and identical across every overload of that name — so
  resolving the OID is needed to disambiguate WHICH overload was called,
  not to know what string PostgreSQL uses for the constraint name. Once
  verified live that PostgreSQL's real algorithm doesn't even look at a
  function call's arguments for naming purposes (confirmed extensively:
  `lower(a)` → `lower`, `date_trunc('day', ts)` → `date_trunc`,
  `upper(lower(a))` → `upper` — never anything derived from the
  argument(s)), a bare top-level function-call element turned out to be
  entirely predictable from the raw parse tree alone.
  **Second correction (caught by an independent verification pass, same
  day):** the claim above that a type cast (e.g. `(a::text)`) can't be
  predicted without catalog access was ALSO too broad — repeating the
  exact same category of mistake the first correction had just corrected
  for function calls. PostgreSQL's real per-element naming rule
  (`FigureColnameInternal` in `parser/parse_target.c`, invoked via
  `FigureIndexColname` from `parse_utilcmd.c`'s `transformIndexStmt` —
  confirmed via source that this runs BEFORE expression analysis, on the
  raw parse tree, genuinely no catalog/OID access involved) handles a type
  cast by recursing into its own argument first: if that argument is
  itself a column or function call (a "strong" name), the cast is
  transparent and the argument's name wins (confirmed live: `a::text` →
  `a`, `lower(a)::text` → `lower`, even through two nested casts). Only
  when the argument gives nothing usable does the cast fall back to its
  own written target type name (confirmed live: `(a + b)::text` → `text`,
  matching the cast's type, not the discarded operator). `NULLIF(a, b)` is
  a further special case, always predicting the literal `nullif`. What
  remains genuinely unpredictable is narrower than either prior version
  claimed: only a bare, uncast operator/arithmetic/concatenation
  expression with no column, function call, or cast anywhere in it (e.g.
  `(a + b)` with no `::` around it) — PostgreSQL's own algorithm produces
  no usable name for that shape either (confirmed live), so it's a
  genuine limitation of PostgreSQL's own naming rule, not a gap this
  tool is choosing not to close.
  **Extension (same day, following up on an independent verification
  pass's live-tested suggestion):** the same `FigureColnameInternal` port
  was extended to four more node shapes confirmed live to be handled by
  the identical mechanism: `COALESCE(...)` (always predicts the literal
  `coalesce`, the same "act like a function" treatment PostgreSQL gives it
  — never inspecting its arguments), `CASE ... END` (consults only the
  `ELSE` clause, never a `WHEN` branch, falling back to the literal `case`
  when the `ELSE` clause is absent or itself weak), an array subscript
  (e.g. `a[1]`, which has no naming contribution of its own and recurses
  through to the base column), and a `COLLATE` clause (transparent
  pass-through to its argument, same as it is for `TypeCast`).
- **`EXCLUDE` constraints could not actually be declared in DPG source at
  all.** `buildConstraint`'s `CONSTR_EXCLUSION` case discarded the entire
  body — access method, per-element operators, `WHERE` clause — down to a
  hardcoded `"EXCLUDE"` placeholder, which is invalid SQL on its own
  (`ALTER TABLE t ADD CONSTRAINT c EXCLUDE;` is a syntax error), so any table
  with an inline `EXCLUDE` constraint failed to apply. Introspection already
  captured a real `EXCLUDE` definition correctly via `pg_get_constraintdef`
  (used for `verify`/`plan --live`) — the gap was source-side parsing only.
  Fixed by extracting the full body: `ir.Constraint` gained a structured
  `Exclude *ExcludeSpec` (access method, an ordered element list — each a
  column or expression with optional `COLLATE`/operator class/sort
  direction/`NULLS` placement, mirroring the same `IndexElem` grammar
  `CREATE INDEX` already uses — plus its `WITH` operator, with `pg_catalog.`
  stripped from a schema-qualified built-in operator the same way built-in
  type names already are), and a real WHERE predicate. `Expr` is rendered
  from this struct in PostgreSQL's own syntax and flows through the same
  generic constraint-emission and `dump`-rendering paths every other
  constraint type already uses — no changes needed there. Verified live
  that PostgreSQL fills in `USING btree` as the access method even when
  `USING` is omitted from source (confirmed via `pg_get_constraintdef`
  against a real server, not assumed), so an unqualified `EXCLUDE` renders
  and round-trips correctly too, not just the explicit-`USING` form.
  Also fixed a related, newly-reachable gap in the same change: the
  column-rename translator (`translateConstraintExpr`, used so a plain
  `RENAMED FROM` doesn't look like a drop+re-add of every constraint
  touching that column) had no `EXCLUDE` case at all — harmless before,
  since there was no real column reference in the placeholder to translate,
  but a genuine gap now that `EXCLUDE` has one. Fixed by routing `EXCLUDE`
  through the same whole-string rename translation `CHECK` already uses
  (safe here since, unlike `FOREIGN KEY`, `EXCLUDE` has no second,
  different-table column group to accidentally touch).
  Scope: `WITH (storage_parameter)` and `USING INDEX TABLESPACE` are not
  captured, matching `PRIMARY KEY`/`UNIQUE`'s existing inline-constraint
  scope (neither of those support it today either — a pre-existing,
  consistent limitation, not a new one introduced here); index opclass
  options (`opclass (param = value)`) aren't modelled, a narrower and rarer
  omission. `EXCLUDE`'s own auto-generated-name reconciliation (matching
  the fix already shipped for unnamed `PRIMARY KEY`/`UNIQUE`/`FOREIGN
  KEY`/`CHECK`) remains a separate, not-yet-implemented feature — this
  change removes what was blocking it (there's now a real body to name),
  but doesn't implement the naming itself.
  **Correction (caught by an independent verification pass, same day):**
  the `EXCLUDE`/`CHECK` rename-translation fix above was incomplete.
  `replaceQuotedIdents` only substituted the fully quoted `"old"` form, but
  `nodeToText`/`pg_query.Deparse` — which renders both a `CHECK` expression
  and `EXCLUDE`'s `WHERE` clause/expression-based elements — only quotes an
  identifier that actually needs it (confirmed live: `SELECT room > 0`
  deparses to `room > 0`, unquoted), unlike `PRIMARY KEY`/`UNIQUE`/`FOREIGN
  KEY`'s always-quoted, hand-built column lists. In practice this meant a
  renamed column referenced only in a `CHECK`'s expression or an `EXCLUDE`'s
  `WHERE` clause (the ordinary case — `CHECK (amount > 0)`, not
  `CHECK ("amount" > 0)`) was silently NOT translated, producing the exact
  self-inconsistent manual-warning-then-re-add sequence the original fix
  exists to prevent. This bug was **pre-existing for `CHECK`** (reproduced
  independently against a `CHECK` constraint with no `EXCLUDE` involved at
  all) — not introduced by this session's `EXCLUDE` work, just newly
  exercised by it and caught in the same review pass. The shipped
  regression test's fixture had hand-written a quoted `WHERE ("room" > 0)`,
  a shape the real pipeline never actually produces, so it passed without
  exercising the bug. Fixed by having `replaceQuotedIdents` also substitute
  a bare, word-boundary-matched occurrence of the old name (case-sensitive,
  so a lowercase column name can never collide with an uppercase-rendered
  keyword like `AND`/`NULL`/`ASC` — confirmed live that `Deparse` always
  renders keywords uppercase while a declared-unquoted column name is
  always lowercase). New tests use realistic unquoted expression text and
  cover a rename touching only a `WHERE` clause and only a bare `CHECK`
  expression, for both constraint types.
  **Second correction (caught by an independent verification pass, same
  day):** the bare-word substitution above was itself incomplete — it had
  no awareness of SQL string-literal boundaries. A single-quoted string
  literal is delimited by `'`, a non-word character, so the unqualified
  `\bold\b` regex treated a literal VALUE that happened to equal a renamed
  column's old name (e.g. `CHECK (status <> 'room')`, with an unrelated
  column also named `room` being renamed elsewhere on the same table)
  exactly like a real identifier reference — mangling the literal into
  `'chamber'`, which made an **unchanged** constraint's snapshot-side text
  stop matching its (correctly unchanged) desired-side text, producing a
  spurious destructive drop+recreate for a constraint that never actually
  changed. Reproduced end-to-end through the real diff pipeline before
  fixing. Fixed by having `replaceQuotedIdents` skip both substitutions
  (quoted and bare-word) inside any single-quoted string literal span, via
  a new `splitSQLStringLiterals` helper that also correctly handles SQL's
  doubled-quote escape (`''` inside a literal is a literal quote character,
  not the end of the string — confirmed this is `pg_query.Deparse`'s own
  escaping convention too, e.g. rendering `it's` as `'it''s'`, so a naive
  split on every apostrophe would have ended a literal early on that case).
  New tests cover the literal-collision scenario directly, plus the
  `splitSQLStringLiterals` helper in isolation (plain literal, escaped
  quote, multiple literals, no literal at all).
  invalid `DROP OPERATOR` statement, a PostgreSQL syntax error.**
  PostgreSQL requires a mandatory `(lefttype, righttype)` operand clause on
  `DROP OPERATOR`, which `ir.Operator` never captured — `dropObject` emitted
  a bare `DROP OPERATOR IF EXISTS name;`, so genuinely removing an operator
  from a DPG project always failed to apply. The same missing information
  meant `diffOpaqueIR` deliberately excluded "operator" from the structured
  drop+recreate path other opaque kinds got, falling back to a manual
  `-- WARNING: ... manual DROP + recreate required` comment on a body edit
  instead. Separately, operators are identified by `(schema, name, lefttype,
  righttype)` — the same symbol can be overloaded across operand types (e.g.
  `+` for `integer` vs `numeric`) — but `ir.Operator.QualifiedName` keyed
  only on `(schema, name)`, so a second overload of an existing symbol
  silently overwrote the first in the flat, name-keyed snapshot and diff
  maps, the same collision class already fixed for `OperatorClass`/
  `OperatorFamily`. Fixed by extracting `LEFTARG`/`RIGHTARG` in both
  directions — from source (`buildDefineStmt`'s `DefElem` walk, the same
  pattern `Cast` already uses) and from the live catalog
  (`introspectOperators`, already scanning `oprleft`/`oprright` but
  previously discarding them) — and widening `QualifiedName` to
  `schema.name(lefttype, righttype)`, PostgreSQL's own `DROP OPERATOR`
  operand-list syntax verbatim (with the literal `NONE` for the side a
  unary/prefix operator omits), so the same rendering doubles as both the
  identity suffix and the `DROP OPERATOR` argument list. `dropObject`'s
  operator case also had a second, previously-unexercised bug caught while
  fixing the first: it quoted the operator symbol itself
  (`qualIdent("public", "===")` → `"public"."==="`), which is also a syntax
  error — an operator symbol is a lexical token, not a quotable identifier.
  Fixed via a new `qualOperatorIdent` helper that quotes only the schema,
  mirroring introspection's existing `operatorRef`.
  Upgrade note: `QualifiedName`'s format change means any operator already
  in a project's snapshot is keyed differently after upgrading — the first
  `plan`/`apply` will show it as removed-and-re-added once (self-heals, same
  pattern as the prior `OperatorClass`/`OperatorFamily` and index-format
  snapshot changes).
- **`dump` could not reconstruct a `FUNCTION`'s body at all, and dropped
  every `PROCEDURE` from generated source entirely.** A dumped function
  rendered only `-- function ... (body omitted; use source files for full
  definition)` — a comment, not a working declaration — and `PROCEDURE` had
  no case at all in `dump`'s object-rendering switch, so a database
  containing procedures produced source silently missing them, with no
  error or warning. Root cause: introspection selected `pg_proc.prosrc`
  only to compute a change-detection hash (`BodyHash`), then discarded the
  raw text; it was never stored anywhere `dump` could read it from, even
  though `ir.Function`/`ir.Procedure` already carry an `Attrs.Body` field
  (already populated correctly when compiling from `.dpg` source — this was
  purely an introspection-side gap). Fixed by also storing `prosrc` into
  `Attrs.Body`, and giving `dump` real rendering logic for both object
  types: full signature, `RETURNS`/`LANGUAGE`/volatility/`STRICT`/`SECURITY
  DEFINER` (as applicable), the actual `AS $$...$$` body, and a trailing
  block for `COMMENT`/`GRANT` (also previously never rendered for either
  type, despite being genuinely compared by `diffFunction`/`diffProcedure`).
  `PARALLEL`/`COST`/`ROWS` remain unrendered, since introspection doesn't
  capture them either — a narrower, separate residual gap, not addressed
  here. `AGGREGATE` is unaffected by this fix and remains a deliberate,
  separate limitation: reconstructing a real `CREATE AGGREGATE` from
  catalog fields (`sfunc`/`stype`/`finalfunc`/etc.) isn't attempted at all,
  and `apply`/`plan` already error explicitly if an aggregate's body was
  never captured, rather than silently applying an incomplete definition.
- **An inline, unnamed `PRIMARY KEY`/`UNIQUE`/`FOREIGN KEY` (e.g.
  `id BIGINT PRIMARY KEY`, with no explicit `CONSTRAINT` name) produced a
  self-inconsistent `DROP CONSTRAINT` + `ADD` pair on every single
  `verify`/`plan --live` run, never converging.** PostgreSQL's
  `pg_constraint.conname` is never empty — it auto-generates a name (e.g.
  `orders_pkey`) even when the user writes none — so live introspection
  always sees a real name, while the desired source (still unnamed) has
  none; the differ's constraint-matching key treated these as unrelated,
  reading the live one as removed and the desired one as new. Fixed by
  reconstructing PostgreSQL's own auto-naming algorithm
  (`ChooseConstraintName`/`ChooseRelationName`/`makeObjectName`, ported from
  PostgreSQL's own C source rather than from memory, including its exact
  truncation-on-collision behavior for names exceeding 63 bytes and its
  schema-wide collision-avoidance rule) and matching an unnamed desired
  constraint against the reconstructed name in addition to the existing
  structural-signature fallback (which already correctly handled the
  offline `plan`/`apply` path, where a persisted snapshot preserves the
  original unnamed declaration verbatim). Covers `PRIMARY KEY`, `UNIQUE`,
  `FOREIGN KEY`, and `CHECK` (see the `CHECK`-specific entry below for how
  its single-column naming rule was reconstructed). `EXCLUDE` constraints
  remain excluded: they aren't round-tripped by DPG at all yet (a
  pre-existing, separate, larger gap — the body isn't parsed from source in
  any capacity, so there's no real desired-side definition to name in the
  first place). The
  collision-avoidance check is schema-wide in the same way PostgreSQL's own
  is: for `PRIMARY KEY`/`UNIQUE` (both index-backed) it considers every
  relation name in the schema — tables, views, sequences, indexes — not
  just other constraint names, matching `ChooseRelationName`'s real
  `pg_class` scan (a plain table happening to be named e.g. `orders_pkey`
  forces PostgreSQL to fall back to `orders_pkey1` for an unnamed `PRIMARY
  KEY` on table `orders`, even with no colliding constraint name);
  `FOREIGN KEY` only needs the narrower constraint-name check, since it
  isn't backed by an index.
- **An inline, unnamed `CHECK` constraint (e.g. `CHECK (amount > 0)`, or
  `a INTEGER CHECK (a > 0)`) had the same self-inconsistent `DROP CONSTRAINT`
  + `ADD` problem as the unnamed `PRIMARY KEY`/`UNIQUE`/`FOREIGN KEY` fix
  above, but was left uncovered by it at the time: PostgreSQL's default name
  for a `CHECK` constraint (`ChooseConstraintName`, called from
  `AddRelationNewConstraints` in `heap.c`) depends on whether its expression
  references exactly one distinct column — `tab_col_check` if so,
  `tab_check` otherwise — which neither the IR builder nor the introspector
  previously extracted. Fixed by walking the constraint's parsed expression
  tree (via protobuf reflection over every populated message field, rather
  than hand-listing each expression node type — `A_Expr`, `FuncCall`,
  `CASE`, `BoolExpr`, etc. — individually) to collect the distinct columns
  it references, mirroring PostgreSQL's own `pull_var_clause`-based
  approach. This is expression-based, not syntactic-position-based: a
  column-level `CHECK` that references another column (e.g.
  `a INTEGER CHECK (a > b)`) correctly predicts the same `tab_check`
  (no-name2) form PostgreSQL itself would generate, rather than assuming the
  column it's attached to. `CHECK`'s collision-avoidance check is
  constraint-names-only (like `FOREIGN KEY`), not schema-wide over relation
  names — `CHECK` isn't index-backed, so its real naming path never scans
  `pg_class`. `EXCLUDE` remains excluded, unchanged from the entry above.
- **`diffIndexes` now compares a same-named index's actual definition instead
  of only checking whether the name exists.** Previously an index present in
  both desired and snapshot was never compared at all — editing its method,
  uniqueness, columns, sort order/`NULLS` placement, `WHERE`, `INCLUDE`,
  `WITH`, or `NULLS NOT DISTINCT` while keeping the name was a silent no-op on
  `plan`/`apply`. `SnapIndex` gained `Include`/`With`/`NullsNotDistinct` fields
  (previously only `Name`/`Unique`/`Method`/`Columns`/`Where` were stored) and
  `Columns` now also carries sort-order/`NULLS` suffixes; any difference now
  emits a `DROP INDEX` + `CREATE INDEX` pair, the same statement already used
  when an index is removed outright. `translateIndexCols` (used to suppress a
  redundant `DROP INDEX` when `DROP COLUMN` already cascades to it) was
  updated to strip these new suffixes before matching column names — the
  `Columns` format change would otherwise have broken that cascade check for
  any sorted index.
  Fixing this surfaced two additional spurious-drift bugs that only appear
  once an index's definition is actually compared against a live catalog:
  `pg_get_indexdef` always quotes `WITH` storage-parameter values (e.g.
  `fillfactor='70'`) while hand-written source may not, and it adds an
  explicit `::typename` cast to string literals inside expression index
  columns (e.g. `to_tsvector('english'::regconfig, e)`) that source doesn't
  have. Both are now normalized before comparison (quote-stripping in
  `snapshot.ToSnapIndex`; the existing `stripStringLiteralCasts` — already
  used for column defaults — applied to `WHERE` and expression columns during
  introspection), so an unchanged index no longer shows drift against a real
  database.
- `GRANT name (...)`/`REVOCATION name (...)` (Mode B, RFC §4.8's singular-
  keyword form) now parse in all three places they're accepted — table level,
  column level, and inside a `DEFAULT PRIVILEGES { }` block. Previously the
  parser routed `GRANT`/`GRANTS` (and `REVOCATION`/`REVOCATIONS`) to the same
  brace-requiring block parser at each of those three sites, so the singular
  form was a hard parse error, identical to the `INDEX`/`INDICES` conflation
  fixed earlier in this release. Mode A (`GRANTS { ... }`/`REVOCATIONS { ... }`)
  is unchanged and the two may be freely mixed, as the RFC requires.
- Explicit `REVOCATION`/`REVOCATIONS` (RFC §11.3) on a table, column, or view
  now actually emits `REVOKE`, in either syntax mode. Found while live-testing
  the fix above: it was parsed into the AST/IR correctly but
  `internal/diff/differ.go` never read `ir.Table.Revocations`,
  `ir.Column.Revocations`, or `ir.View.Revocations` anywhere — only
  `DEFAULT PRIVILEGES` revocations were diffed/emitted, so declaring one on a
  table/column/view was a complete silent no-op. Mirrors the existing
  `DEFAULT PRIVILEGES` revocation semantics: a revocation is tracked in the
  snapshot once applied, so a later unchanged run doesn't re-issue `REVOKE`
  (kept for tidy migration output, not because re-running it would error —
  `REVOKE` on an already-absent privilege is a harmless no-op in PostgreSQL),
  and removing a `REVOCATION` declaration re-`GRANT`s the privilege to
  restore it. `CASCADE` is supported at the table/view level and column
  level.
- `POLICY name ...`/`TRIGGER name ...`/`PARTITION name ...` (Mode B, RFC
  §4.8's singular-keyword form) now parse. Unlike `INDEX`/`GRANT`/
  `REVOCATION`, these three weren't even in the block dispatch switch, so the
  singular keyword was "unknown block directive", not just a brace mismatch.
  `POLICY`/`TRIGGER` reuse the existing per-entry parser their plural block
  already called internally; `PARTITION` needed one extracted
  (`parseOnePartitionBound`), since `PartitionDef` wraps a list unlike the
  other four collection types. Fixing `PARTITION` surfaced two further bugs,
  both fixed alongside it:
  - **`CREATE TABLE ... PARTITION OF ... FOR VALUES ...` had a duplicated
    `FOR VALUES`, a syntax error.** Both the parser (raw text captured after
    the partition name) and introspection (`pg_get_expr` on `relpartbound`)
    already include the `FOR VALUES ...` (or `DEFAULT`) clause in a
    partition's stored bounds text, but the SQL-emission code prepended a
    second, hardcoded `FOR VALUES` on top. Pre-existing, present for both
    Mode A and Mode B, in both the initial-create and update-diff paths —
    just never live-tested against a real database before now.
  - **A `PARTITIONS { }` block silently discarded any partitions a preceding
    Mode-B `PARTITION` entry had already added**, because `PartitionDef`
    wraps a list and the `PARTITIONS` dispatch case assigned outright
    (`ast.Partitions = ...`) instead of merging into an existing value. Only
    manifests when `PARTITION` precedes `PARTITIONS` on the same table; the
    reverse order happened to work by accident.
  A known limitation found live-testing this fix — introspecting a
  partitioned parent table reported spurious column/trigger drift — is now
  fixed; see below.
- **Partitioned-table introspection drift, fully fixed** (four separate
  bugs, all found live-testing a partitioned table with columns, constraints,
  indexes, and triggers together — the combination no prior test exercised):
  - `introspectColumns`/`introspectConstraints`/`introspectIndexes` all
    filtered `relkind = 'r'` (ordinary table only), silently excluding
    `relkind = 'p'` (a partitioned parent). A partitioned table's own
    columns, constraints, and indexes were never introspected at all, so
    `verify`/`plan --live` proposed re-adding all of them on every run.
  - `introspectPartitions`'s child-partition query matched on
    `relispartition` alone, which is **also true for an auto-created child
    INDEX partition** (when an index exists directly on the partitioned
    parent) — those have no partition bound, so `pg_get_expr` returns `NULL`
    and scanning it into a non-nullable string crashed introspection
    outright for any partitioned table that also had an index on it. Fixed
    by additionally requiring `relkind IN ('r', 'p')` (an actual table
    partition), excluding index partitions (`relkind 'i'`/`'I'`).
  - `diffTriggers` compared a trigger's `EXECUTE FUNCTION` reference as a
    raw string. Introspection always returns it schema-qualified
    (`public.func`); hand-written source commonly leaves it unqualified
    (`func`), relying on the default schema the same way DPG's own objects
    do. Every unqualified trigger function showed as changed on every
    `verify`/`plan --live` — **not specific to partitioned tables**, this
    affects any table with a trigger.
  - The introspected trigger `events` column concatenated a hardcoded
    `" OR "` before `DELETE`/`UPDATE`/`TRUNCATE` regardless of whether an
    earlier event was actually present, leaving a dangling `"OR "` fragment
    for any trigger whose only event isn't `INSERT` (e.g. an UPDATE-only
    trigger introspected as `"OR UPDATE"` instead of `"UPDATE"`, which never
    matched the declared event and always showed as changed). Also
    **not specific to partitioned tables.** Rebuilt using `array_to_string`
    (which skips absent events cleanly) instead of string concatenation.
  `dpg apply` was never affected by any of these — only `verify`/
  `plan --live` on an already-applied table.
- `CONSTRAINTS { }` (Mode A, RFC §4.8's plural-block form) now parses.
  Previously only the singular `CONSTRAINT name ...;` form worked at all —
  unlike the other 7 collection types in RFC §4.8's Dual Definition Modes
  table, `CONSTRAINTS` had no parser whatsoever, not even a buggy one. The
  RFC lists `CONSTRAINTS`/`CONSTRAINT` as a pair but never otherwise
  specifies or exemplifies the plural form anywhere in the document; judged
  a gap worth closing for consistency with the other 7 rather than a
  deliberate omission, since nothing calls out constraints as an intentional
  exception. Reuses the existing `parseConstraint` unchanged — each entry
  in the block gets its own position captured before calling it. May be
  freely mixed with the singular form on the same table.
- A reserved-word or otherwise non-bare `SCHEMA` name (e.g. `SCHEMA "select"
  { }`, or one containing a hyphen or an embedded quote) can now be declared
  at all. The scanner's schema-name reader previously accepted only a bare
  word, so any such name failed to parse; it now also accepts a double-quoted
  identifier (with `""` escaping), reconstructed with its quoting intact when
  fed to the PG parser. `dump` now quotes a schema name via the same
  `quoteIdentIfNeeded` helper already used for roles/extensions, instead of
  always emitting it bare.
- A sequence's `CYCLE`/`NO CYCLE` clause was silently unparseable: PostgreSQL's
  grammar represents it as a `DefElem` whose argument is a `Boolean` node,
  unlike every other sequence option (`INCREMENT BY`, `MINVALUE`, etc.), which
  use an `Integer`/`A_Const` node — the builder routed all sequence options
  through a helper that only handles the latter two, so `CYCLE`/`NO CYCLE`
  always evaluated to "unset" regardless of what was written. A second,
  related bug in `diffSequence` then gated the `CYCLE` comparison on
  `INCREMENT BY` also being explicitly set, so even a correctly-parsed
  `CYCLE` change was missed by `verify`/`plan --live` unless another sequence
  option changed in the same diff — the common case (changing only `CYCLE`)
  was silently ignored. Both are fixed: the builder now reads the `Boolean`
  node directly, and the comparison is gated on `CYCLE` itself being set.
  `ir.Sequence.Cycle` changed from `bool` to `*bool` (nil = unspecified,
  matching every other sequence option) to make the "not set" state
  representable at all.
- Added unit test coverage for several `internal/diff` code paths that had
  none at all (found via a coverage pass: package coverage was 67.1%): fresh
  `CREATE ROLE`/`CREATE EXTENSION`/`CREATE SEQUENCE`, schema `ALTER
  SCHEMA ... OWNER TO`/comment changes, and a table-level `POLICY`/inline
  `GRANT` declared alongside a `CREATE TABLE`. `PROCEDURE` had zero diff-level
  coverage of any kind, unit or live — added create/comment/grant/body-change
  unit tests plus a live round-trip test.
- `dump` now renders a table/schema/sequence's `OWNER` and a column's
  `COMMENT`/`STORAGE`/`COMPRESSION`/`STATISTICS`. For Table/Schema, `OWNER`
  was already genuinely compared by the differ but never emitted into
  dumped source, so drift went undetected forever. Sequence `OWNER` was a
  deeper gap: it was not just unrendered but **completely inert** end to
  end — the snapshot form had no `Owner` field at all, `CREATE SEQUENCE`
  never emitted an `OWNER TO` for a new sequence, and the differ never
  compared it against drift — so a declared sequence owner had zero effect
  on initial apply or on any subsequent `verify`/`plan --live`, even though
  the IR builder parsed it correctly. `SnapSequence` gained an `Owner`
  field, and `createSequence`/`diffSequence` now emit `ALTER SEQUENCE ...
  OWNER TO` the same way Table/Schema already did.
  `STORAGE` needed separate care: PostgreSQL always has a concrete storage
  mode for every column (there's no "unset" state at the catalog level,
  unlike the other attributes), and it's usually just the column's type's
  own default (e.g. every `text`/`varchar`/`jsonb` column defaults to
  `EXTENDED`) — rendering it unconditionally would add a `STORAGE` line to
  nearly every variable-length column in every dumped table. Introspection
  now also captures whether a column's storage matches its type's own
  default (via a live join against `pg_type.typstorage`), and `dump` only
  renders `STORAGE` when it's a genuine override.
- `dump` output now recompiles **and re-applies** for real-world tables:
  identifiers that are reserved keywords or otherwise non-bare (table/column/view/
  sequence/role/index/constraint names) are quoted; indexes render as the accepted
  `INDICES { … }` block (was the unparseable `INDEX name (…)` form) with bare
  column names (the differ quotes them); `COMMENT` values render as SQL string
  literals (was Go `%q` double-quotes); and enums render as `ENUM <name> AS ENUM
  (…)` (was a missing `AS ENUM`).
- `CREATE INDEX` now emits the partial-index `WHERE` predicate and `INCLUDE`
  columns, which were silently dropped — a declared partial/covering index was
  created as a plain index.
- Index column sort order (`ASC`/`DESC`) and `NULLS FIRST`/`LAST` now round-trip:
  the block parser previously stored `col DESC NULLS LAST` as one literal column
  name, so a dumped sorted index failed to apply. Covering (`INCLUDE`) columns are
  now introspected and dumped, so a dumped covering index stays covering.
- The dependency resolver now orders a custom type before a table that uses it
  even when the column type is written unqualified (as introspected columns are),
  so a dumped project whose table precedes its enum applies without
  "type … does not exist".
- `dump` now emits `SCHEMA` and `EXTENSION` declarations, so non-public schemas
  and installed extensions are no longer dropped by `plan --live`.
- `dump` now renders the seven deferred-tier types it learned to introspect
  (operators, operator classes/families, text-search parsers/templates/
  dictionaries/configurations); previously it silently omitted them, so a `dump`
  of a database using any of them was immediately followed by a destructive
  `DROP` for each on the next `plan --live`.
- An operator class and the operator family PostgreSQL auto-creates for it share
  the same qualified name, which collided in the flat, name-keyed snapshot and
  diff maps and silently dropped one of the two objects. Operator class/family
  identity now includes the access method and a class/family discriminator, so
  the two can never overwrite each other (this changes their snapshot keys — see
  upgrade notes).
- Operator-family introspection no longer emits the same-named family that a
  `CREATE OPERATOR CLASS` auto-creates as a standalone object (which would emit a
  redundant `CREATE OPERATOR FAMILY` that conflicts with the auto-creation on
  re-apply), and a class's reconstructed body no longer names that implicit
  family in a `FAMILY` clause. The previous discriminator relied on a `pg_depend`
  deptype that is identical for auto-created and explicitly-attached families; the
  reliable signal is the family's matching name, mirroring PostgreSQL's own rule.
  Known limitation: an *explicit* operator family that happens to share a class's
  name (rare, but legal) is treated as the implicit auto-created one and omitted
  from the dump; operator-family members added via `ALTER OPERATOR FAMILY … ADD`
  are not modeled in either case.
- Introspection now excludes extension-owned functions, procedures, aggregates,
  types, tables, views, and sequences (e.g. `hstore`'s ~60 functions), so they
  are not dumped or proposed for dropping.
- `DROP OPERATOR CLASS`/`DROP OPERATOR FAMILY` now use the object's real index
  access method instead of a hardcoded `USING btree` (legacy snapshots without
  the recorded method fall back to `btree`).
- `DROP USER MAPPING` for a `FOR PUBLIC` mapping no longer emits a zero-length
  identifier.
- `dump` no longer emits `COLLATION`/`STATISTICS` declarations with an empty
  body (which aborted `plan`/`apply` with "body not captured").
- A source-side body edit to an opaque object (tablespace, FDW, foreign server,
  user mapping, publication, subscription, event trigger, collation, operator
  class/family, cast, extended statistics, or any text-search object) now emits
  a real `DROP ... IF EXISTS` followed by the new `CREATE`, reusing the exact
  statement `dropObject` already emits when the object is removed outright.
  Previously `plan`/`apply` only emitted a `-- WARNING: ... manual DROP +
  recreate required` SQL comment — a body edit was silently a no-op unless
  someone read migration output and ran the DROP/CREATE by hand. Operators are
  excluded from this fix and keep the old warning. Known limitation: `DROP
  OPERATOR` requires a mandatory `(lefttype, righttype)` clause that
  `ir.Operator` does not capture (a pre-existing gap, not introduced by this
  fix — it also affects an operator's removal from source, not just its body
  edits); operator names can also be overloaded across operand types, which the
  flat `schema.name` snapshot key cannot disambiguate. Emitting invalid or
  misdirected DDL would be worse than the manual warning, so operator body
  edits keep the placeholder until operand types are modelled end-to-end.
- Reconstructed opaque-object bodies no longer report spurious destructive
  "body changed" drift on `verify`/`plan --live` when hand-written source uses an
  equivalent-but-differently-spelled form; offline `plan`/`apply` still detect
  genuine body edits.

### Changed

- Cluster/database object routing is unified across `dump`, `plan`, and `verify`:
  only roles and tablespaces are cluster-scoped.

### Upgrade notes

- **Drift on first run against an existing database.** Because these object types
  are now introspected, a live-but-undeclared publication, FDW, event trigger,
  collation, etc. becomes a drop candidate on `plan --live` (standard declarative
  semantics — declare the object in source to keep it). This is a behavior change
  only for upgrade-in-place; freshly dumped projects are unaffected.
- **Body-change detection self-heals on next apply.** Snapshots written before
  this release have no stored body hash for these types, so offline detection of
  a body edit stays dormant until the next `apply` re-snapshots from source.
- **Operator class/family snapshot keys changed.** Their snapshot keys now embed
  the access method and a class/family marker (e.g. `public.x USING btree` vs
  `public.x USING btree FAMILY`). Any operator class or family in a pre-existing
  snapshot re-keys on the next run: the old key reads as a removed object and the
  new key as a new one, so `plan` emits a **DESTRUCTIVE** `DROP` + `CREATE` pair
  (requires `--allow-destructive` to apply). For a class or family with no
  dependents this applies cleanly and the next `plan` is a genuine no-op. If
  anything depends on the object — e.g. an index built with a custom operator
  class, the usual reason to have one — the `DROP` **fails outright** with a
  Postgres "other objects depend on it" error (loud failure, not silent
  corruption); drop/rebuild the dependents around the apply, or avoid the churn
  entirely by hand-editing the snapshot key to the new format before running.
- **Index content-comparison self-heals on next apply.** `SnapIndex` gained
  `Include`/`With`/`NullsNotDistinct` fields and `Columns` now carries sort-
  order/`NULLS` suffixes. Snapshots written before this release don't have
  this data for indexes that already use these properties (covering columns,
  storage parameters, `NULLS NOT DISTINCT`, or non-default sort order), so the
  first `plan`/`apply` after upgrading recreates those indexes once (a
  `CAUTION`-level `DROP INDEX` + `CREATE INDEX`, not `DESTRUCTIVE` — same as a
  plain index change) to bring the snapshot up to date; every run after that
  is a genuine no-op.
- **Table/column/view `REVOCATION` now actually runs.** Previously a silent
  no-op, so an existing `REVOCATION`/`REVOCATIONS` declaration may not
  reflect current reality — the target role might already lack the privilege,
  or might still have it because DPG never revoked it. On the first
  `plan`/`apply` after upgrading, `REVOKE` runs for every declared revocation
  not yet recorded in the snapshot. This is safe to run even if the privilege
  was never actually granted — `REVOKE` on an already-absent privilege
  succeeds as a no-op in PostgreSQL, it does not error. The one thing that
  does error is revoking from a role that no longer exists; if you have a
  stale `REVOCATION` naming a dropped role, fix or remove it before applying.

## [idea-v0.5.2-alpha.13] — 2026-05-22

### Added

- Add support for macro spread references, resolution logic, and related tests across LSP and IDEA plugin
- Enforce blank line between schema attributes and first nested object in formatter and add related test
- Add support for DPG code formatting in IDEA plugin, including indentation, blank lines, and spacing adjustments
- Add support for macro declarations, rendering, and schema-level nesting in formatter

### Changed

- Simplify schema node comment collection logic in parser

## [0.5.5-alpha.1] — 2026-05-21

### Added

- Enforce blank line between schema attributes and first nested object in formatter and add related test
- Add support for DPG code formatting in IDEA plugin, including indentation, blank lines, and spacing adjustments
- Add support for macro declarations, rendering, and schema-level nesting in formatter
- Add support for macro references, completions, and spread expressions in IDEA plugin

### Changed

- Simplify schema node comment collection logic in parser
- Migrated idea plugin from LSP-based implementation to plugin-native parsing, highlighting, and tooling

## [lsp-v0.5.5-alpha.1] — 2026-05-21

- No changes.

## [lsp-v0.5.2-alpha.11] — 2026-05-21

### Added

- Enforce blank line between schema attributes and first nested object in formatter and add related test
- Add support for DPG code formatting in IDEA plugin, including indentation, blank lines, and spacing adjustments
- Add support for macro declarations, rendering, and schema-level nesting in formatter
- Add support for macro references, completions, and spread expressions in IDEA plugin

### Changed

- Simplify schema node comment collection logic in parser
- Migrated idea plugin from LSP-based implementation to plugin-native parsing, highlighting, and tooling

## [idea-v0.5.2-alpha.12] — 2026-05-20

### Added

- Add support for macro references, completions, and spread expressions in IDEA plugin

## [idea-v0.5.2-alpha.11] — 2026-05-20

### Changed

- Migrated idea plugin from LSP-based implementation to plugin-native parsing, highlighting, and tooling

## [0.5.4] — 2026-05-17

- No changes.

## [0.5.3] — 2026-05-17

- No changes.

## [0.5.2-alpha.21] — 2026-05-17

- No changes.

## [0.5.2-alpha.20] — 2026-05-17

- No changes.

## [0.5.2-alpha.18] — 2026-05-17

- No changes.

## [0.5.2-alpha.17] — 2026-05-17

- No changes.

## [0.5.2-alpha.16] — 2026-05-17

- No changes.

## [0.5.2-alpha.15] — 2026-05-17

- No changes.

## [0.5.2-alpha.14] — 2026-05-17

- No changes.

## [0.5.2-alpha.13] — 2026-05-17

- No changes.

## [0.5.2-alpha.12] — 2026-05-17

- No changes.

## [0.5.2-alpha.11] — 2026-05-17

- No changes.

## [idea-v0.5.2-alpha.10] — 2026-05-17

- No changes.

## [vscode-v0.5.2-alpha.10] — 2026-05-17

- No changes.

## [nvim-v0.5.2-alpha.10] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions
- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs
- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`
- Add shortcodes, workflows, and versioning improvements for docs and LSP
- Add install-lsp script and integrate versioning enhancements for dpg-lsp
- Enable local-first Gradle distribution for faster offline builds
- Add test data and refactor LSP smoke testing root logic
- Add installer script and documentation for streamlined binary installation
- Add workflow support and bindings for editor integrations
- Add comprehensive DPG support to editors, tests, and tooling
- Introduce RFC DPG-1 specification for Declarative PG
- Add RFC for Declarative PG (DPG) specification
- Add comprehensive documentation for all schema objects and advanced features
- Add RenderAll for multi-migration output and refactor migration planning
- Add `VIRTUAL TYPE` and macro preprocessing support
- Inline single-column constraints and suppress redundant NOT NULL for PostgreSQL
- Normalize NOT NULL handling for PostgreSQL PRIMARY KEY columns
- Add macro preprocessing and support for virtual type declaration
- Improve introspection and schema handling for PostgreSQL
- Add minimal home page layout for website
- Add new commands and editor integration for improved `.dpg` workflow
- Add plugin example for custom linter registration and chaining
- Enhance aggregate and enum diffing with grants and migration support
- Enhance column and partition handling in snapshot diffing
- Enhance snapshot diffing with grant tracking and structural updates
- Introduce base infrastructure for DPG compiler, formatter, and public API

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script
- Update prerelease detection regex in release workflows
- Return error for missing body in `createOpaque` to prevent silent no-op

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency
- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`
- Simplify argument signature generation with ArgsKey and dropObject cleanup
- prefer user-local Hugo binary in Makefile and setup script
- Add editor integration guide and extend `dpg fmt` documentation
- Improve `setup.sh` script and update website configs
- add development guide and setup script
- add CLI documentation generation and embedding in release builds
- Add comprehensive CLI and documentation overhaul
- Update documentation for commands, directory structure, and test workflows
- Filled in gaps and added test containers for integrations tests
- Enforce column existence and reject unknown columns in TABLE builds per RFC §7.2

## [lsp-v0.5.2-alpha.10] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions
- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs
- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script
- Update prerelease detection regex in release workflows

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency
- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`

## [grammar-v0.5.2-alpha.10] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions
- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs
- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`
- Add shortcodes, workflows, and versioning improvements for docs and LSP
- Add install-lsp script and integrate versioning enhancements for dpg-lsp
- Enable local-first Gradle distribution for faster offline builds
- Add test data and refactor LSP smoke testing root logic
- Add installer script and documentation for streamlined binary installation
- Add workflow support and bindings for editor integrations
- Add comprehensive DPG support to editors, tests, and tooling
- Introduce RFC DPG-1 specification for Declarative PG
- Add RFC for Declarative PG (DPG) specification
- Add comprehensive documentation for all schema objects and advanced features
- Add RenderAll for multi-migration output and refactor migration planning
- Add `VIRTUAL TYPE` and macro preprocessing support
- Inline single-column constraints and suppress redundant NOT NULL for PostgreSQL
- Normalize NOT NULL handling for PostgreSQL PRIMARY KEY columns
- Add macro preprocessing and support for virtual type declaration
- Improve introspection and schema handling for PostgreSQL
- Add minimal home page layout for website
- Add new commands and editor integration for improved `.dpg` workflow
- Add plugin example for custom linter registration and chaining
- Enhance aggregate and enum diffing with grants and migration support
- Enhance column and partition handling in snapshot diffing
- Enhance snapshot diffing with grant tracking and structural updates
- Introduce base infrastructure for DPG compiler, formatter, and public API

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script
- Update prerelease detection regex in release workflows
- Return error for missing body in `createOpaque` to prevent silent no-op

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency
- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`
- Simplify argument signature generation with ArgsKey and dropObject cleanup
- prefer user-local Hugo binary in Makefile and setup script
- Add editor integration guide and extend `dpg fmt` documentation
- Improve `setup.sh` script and update website configs
- add development guide and setup script
- add CLI documentation generation and embedding in release builds
- Add comprehensive CLI and documentation overhaul
- Update documentation for commands, directory structure, and test workflows
- Filled in gaps and added test containers for integrations tests
- Enforce column existence and reject unknown columns in TABLE builds per RFC §7.2

## [0.5.2-alpha.10] — 2026-05-17

- No changes.

## [0.5.2-alpha.9] — 2026-05-17

- No changes.

## [0.5.2-alpha.8] — 2026-05-17

- No changes.

## [0.5.2-alpha.7] — 2026-05-17

- No changes.

## [0.5.2-alpha.6] — 2026-05-17

- No changes.

## [0.5.2-alpha.5] — 2026-05-17

- No changes.

## [vscode-v0.5.2] — 2026-05-17

- No changes.

## [idea-v0.5.2-alpha.4] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions
- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs
- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script
- Update prerelease detection regex in release workflows

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency
- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`

## [vscode-v0.5.2-alpha.4] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions
- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs
- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency
- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`

## [0.5.2-alpha.4] — 2026-05-17

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency

## [0.5.2-alpha.3] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions

## [0.5.2-alpha.2] — 2026-05-17

### Added

- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs

### Changed

- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`

## [0.5.2] — 2026-05-16

### Added

- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script

## [0.5.1] — 2026-05-16

### Fixed

- Update prerelease detection regex in release workflows

## [0.5.1-alpha.9-rc.10] — 2026-05-16

- No changes.

## [0.5.1-alpha.9-rc.9] — 2026-05-16

- No changes.

## [0.5.1-alpha.9-rc.8] — 2026-05-16

- No changes.

## [0.5.1-alpha.9-rc.7] — 2026-05-16

- No changes.

## [0.5.1-alpha.9-rc.6] — 2026-05-16

- No changes.

## [0.1.0] — 2026-04-29

Initial release.

### Added

**CLI**
- `dpg plan` — diff source files against the committed snapshot and print the minimal SQL migration; supports `--live` to diff against a live database instead
- `dpg apply` — lint, diff, prompt for approval, execute the SQL migration, and update the committed snapshot; supports `--allow-destructive` and `--yes`
- `dpg verify` — connect to a live database and report any drift from the committed snapshot
- `dpg dump` — introspect a live database and generate initial `.dpg` source files and an initial snapshot
- `dpg diff` — diff two `.dpg` source directories and print the SQL between them (no database required)
- `dpg portability` — report PostgreSQL-specific constructs in use and suggest standard SQL alternatives
- `--cluster` and `--database` flags on all commands for multi-cluster/multi-database projects
- Cluster-level objects (roles) planned, applied, and snapshotted independently from databases

**Compiler**
- Source file scanning, parsing via `pg_query_go`, and IR construction for all supported object types
- Schema context inference from directory layout
- Dependency-ordered compilation with topological sort and `DEFERRABLE` cycle handling for circular foreign keys

**Object support**
- Tables: columns (including `IDENTITY`, `GENERATED`, computed), inline single-column constraints, table-level constraints, indexes, RLS, comments, grants
- Views and materialized views
- Functions and procedures
- Types: `ENUM`, `DOMAIN`, composite
- Sequences (user-defined; identity-owned sequences are filtered)
- Roles
- Extensions
- Schemas

**Differ**
- `CREATE`, `ALTER`, and `DROP` generation for all supported object types
- Safety classification: every generated statement tagged `SAFE`, `CAUTION`, `DESTRUCTIVE`, or `MANUAL`
- Destructive operations blocked by default; require `--allow-destructive`
- Warning on `dpg apply` for new tables created without a primary key

**Snapshot**
- JSON snapshot format committed alongside source files
- `dpg dump` rebuilds the snapshot from compiled source to ensure the first `dpg plan` after a dump produces no diff

**Linter**
- Configurable static analysis: deprecated column detection, hardcoded password detection, missing column comments
- Lint diagnostics printed to stderr before any migration is emitted

**Emit**
- Transactional wrapper (`BEGIN` / `COMMIT`) for all safe operations
- Non-transactional post-commit block for `CREATE INDEX CONCURRENTLY` and similar
- Safety labels and source position annotations in rendered output
- ANSI colour support

**Portability analysis**
- Detection and reporting of PostgreSQL-specific constructs with standard SQL alternatives

**Project structure**
- `dpg.toml` discovery with cluster and database directory layout
- Secret resolution via `env:` and `link:` URI schemes
- Migration archiving to a configurable directory

[Unreleased]: https://github.com/dullkingsman/dpg/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/dullkingsman/dpg/releases/tag/v0.1.0
