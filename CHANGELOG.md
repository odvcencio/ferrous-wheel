# Changelog

## v0.6.0 — 2026-07-17

Stabilization pass: seven parser/transpiler correctness bugs found by an
external evaluation, each of which previously produced silently-wrong or
non-compiling output with no diagnostic. All fixed with regression tests;
where a full grammar-level fix wasn't feasible without giving up a rarely
used piece of Go syntax, the trade-off is documented below and the failure
mode is now a clear error instead of silent corruption.

### Range lexing (`0..10` without spaces)

- `for i in 0..10` (no space around `..`) now parses correctly as a range.
  Previously the lexer's longest-match rule preferred the digit-dot float
  form (`0.`) over the range operator, since Go's float grammar allows a
  bare trailing decimal point with nothing after it (`1.` is a valid Go
  float64 literal).
- Fix: ferrous-wheel's `float_literal` now requires at least one fraction
  digit or an exponent when it ends in a bare `.` — matching the same
  design choice Rust and Kotlin's grammars make, and for the identical
  reason (disambiguating from range syntax). `1.5`, `.5`, `1.5e10`, and
  `1.e10` are all still valid; a lone trailing `1.` with nothing else is
  the one piece of Go float syntax given up. See README for the documented
  trade-off.

### Range comprehensions

- `[x*x for x in 0 .. 5]` now desugars to a real counting loop
  (`for x := 0; x < 5; x++`), matching what `for i in 0..10` already did.
  Previously the comprehension lowering called the generic range emitter,
  which only knows how to produce a `/* range 0..5 */` comment placeholder
  (meant for `for-in` to special-case, not to be emitted directly) —
  producing generated Go that didn't parse.
- A range expression used anywhere *other* than a `for-in` iterable or
  comprehension source (e.g. as a bare value, or as an index/subscript like
  `arr[0..2]`) is now a clear transpile error instead of the same broken
  comment placeholder — Go has no range value usable in an arbitrary
  expression position, so there's no valid translation to fall back to.

### Precise parse-error locations

- Parse errors now report `file:line:col`, a short description, and a
  source excerpt with a caret — for every ERROR/MISSING node found (capped
  at 5) — instead of a single generic "parse errors in ferrous-wheel
  source" with no location at all.
- The same treatment applies to "unparsed text" recovery garbage (e.g.
  `package mx n`) and to two new categories of validation: a `.fw` file
  missing (or not leading with) a `package` clause, and a Go reserved
  keyword used where an identifier is expected (e.g. `package func`).

### Silent let-swallow

- `let x =` followed immediately by a newline no longer silently absorbs
  the next statement into the initializer with zero diagnostics (e.g.
  `let x =\nfmt.Println(x)` used to transpile to `x := fmt.Println(x)`,
  discarding the assignment and using `x` in its own initializer).
- New `let-swallow` lint rule (error severity, blocks transpilation):
  flags any `let`/`let (a, b)` declaration whose initializer expression
  starts on a different source line than the `=` token.

### Fuzz testing in CI

- `go test -fuzz=FuzzTranspileProducesParsableGo -fuzztime=60s` now runs in
  CI as a `fuzz-smoke` job (matching the convention used in sibling repos
  like graft), on top of the existing unit test and lint jobs.
- The bounded fuzz run surfaced several more instances of the same root
  cause underlying the bugs above — cases where the parser accepts and
  silently drops or mis-tokenizes a byte that has no valid meaning at that
  position, instead of erroring — and each is now fixed with a
  regression-seeded corpus entry: an empty/garbage-only source file, digits
  silently dropped between a keyword and an identifier that can't start
  with one (`package 0A`, `func A()0A`, `08%0`), non-Go control bytes
  treated as insignificant whitespace (vertical tab, form feed) or as a
  statement terminator (a raw NUL byte), garbage floating alone in an
  otherwise-empty or already-terminated block (`{#}`, `{0\n#}`, `{#0}`), a
  `package` clause that exists but isn't the file's first declaration, and
  an int literal followed by a bare `.` being misread as a selector
  operand (`0.A0`) as a side effect of the range-lexing fix above.

### Docs

- README: documented the `color.` package-vs-builtin-call-syntax gotcha and
  the existing logging DSL section is unchanged (already present).
- README: corrected "10 built-in lint rules" to the current count (see
  below) — it was already stale before this pass (18 rules registered in
  `lint.go` versus 10 documented), and this release adds one more
  (`let-swallow`), so the count is now accurate.
- `register/register.go`'s intended consumer-side wiring (blank-importing
  it from `gts`/editor tooling so `.fw` is discoverable) is verified
  working end-to-end from ferrous-wheel's side; see the report from this
  stabilization pass for exactly what's still needed on the consumer side
  (a cross-repo change, not made here).

## v0.5.0 — 2026-06-24

- **Grammar-free core**: `gotreesitter` bumped to v0.19.1, and the
  `grammars.RegisterExtension` call that makes `.fw` discoverable by
  registry consumers (editors, `gts`) moved out of the ferrous-wheel core
  into a new opt-in `m31labs.dev/ferrous-wheel/register` subpackage. The
  core transpiler (`transpile`, `format`, the grammar DSL) no longer links
  the ~200-grammar, ~22MB `gotreesitter/grammars` registry; only code that
  wants `.fw` registered blank-imports `.../ferrous-wheel/register` and
  pays for it.
- MIT License added.

## v0.4.0 — 2026-05-25

- **Breaking**: module path migrated from `github.com/odvcencio/ferrous-wheel`
  to `m31labs.dev/ferrous-wheel`. All imports, the CLI, and the playground
  updated accordingly.

## v0.3.1 — 2026-04-14

- `ferrous-wheel run`/`build` now inherit the enclosing Go module's
  `go.mod` (including `replace` directives) when a `.fw` script lives
  inside one, instead of always generating an isolated stdlib-only temp
  module — lets scripts import third-party packages the host project
  already depends on.
- Fixed a SIGSEGV when transpiling an untyped top-level `const`/`var`
  declaration (`const Greeting = "hi"` with no explicit type): the CST
  field lookup for the (absent) type annotation returned nil and got
  dereferenced unguarded.

## v0.3.0 — 2026-04-09

Logging and color DSL.

- Level-based `log.info(...)`/`log.warn(...)`/`log.error(...)` (etc.)
  statements transpiling to a `log/slog` backend with a pretty-printing
  handler.
- `with { ... }` blocks for scoped logger attributes, `time { ... }` blocks
  for duration-tracked, tree-drawing-prefixed logging.
- `log.config!(level: .info, time: .relative, format: .pretty)` top-level
  directive.
- `color.red(...)`/`color.bold(...)`/etc. styled-output expression helpers.
- F-strings accepted as log message arguments.
- Eight new lint rules for the logging DSL: `bare-error`, `bare-fatal`,
  `debug-no-attrs`, `time-empty-block`, `with-no-logs`, `with-single-log`,
  `nested-time-same-name`, `log-in-hot-loop`.
- Log level/block command keywords disambiguated as identifiers to avoid
  colliding with user identifiers of the same name.

## v0.2.0 — 2026-04-05

Six new features closing the ergonomic gap with typed-language transpilers.

### Immutable bindings

- `let` bindings are now immutable by default — reassignment is a hard compile error
- `let mut` opts into mutability for bindings that need reassignment
- `if let` bindings are immutable (no `mut` variant)
- Go-native `:=` and `var` remain unrestricted (backwards compatible)
- Compiler-generated temporaries bypass mutability tracking

### If-expressions

- `if`/`else` in expression position transpiles to an IIFE
- Supports `else if` chains and nested if-expressions
- `else` branch is required (compile error if missing)
- `try`/`?` inside branches is disallowed (would escape the IIFE)
- Branch types are unified; falls back to `interface{}` only when unification fails

### Postfix `?` error propagation

- `expr?` strips the error from `(T, error)` and propagates on failure
- Coexists with prefix `try` — both syntaxes supported, no deprecation
- Works in `let`, `:=`, `=`, and standalone expression contexts
- Validates enclosing function returns `error`
- `??` (null coalesce) and `?.` (safe navigation) remain unambiguous via token priority

### Source formatter

- `ferrous-wheel fmt` command formats `.fw` source files
- CST-preserving: adjusts whitespace without regenerating from AST
- Normalizes `let`/`let mut` spacing, aligns match arm `=>` arrows
- Preserves comments and intentional line breaks
- `-w` flag writes in-place, `--check` flag exits 1 if unformatted (CI)
- Idempotent: formatting twice produces identical output

### Lint framework

- 10 built-in rules across three severity levels (Error, Warning, Info)
- Two rule interfaces: `NodeLintRule` (per-node) and `ScopeLintRule` (whole-scope analysis)
- Runs automatically before transpilation — errors block, warnings report
- `ferrous-wheel lint` command for standalone checks
- LSP integration: diagnostics published with proper severity mapping
- Rules: `unused-let`, `unused-mut`, `empty-match`, `unreachable-match-arm`, `missing-else-if-expr`, `redundant-try`, `shadowed-let`, `empty-block`, `unnecessary-safe-nav`, `unreachable-after-guard`

### Bidirectional type inference

- Constraint-based inference with `TypeVar` and substitution solving
- Checking mode: expected types push down into sub-expressions
- Synthesis mode: types inferred bottom-up from leaves
- Lambda parameters inferred from call-site context (`Map(items, fn(x) x * 2)` infers `x` as `int`)
- `UnifyWithContext` replaces ad-hoc `Unify` calls in emit methods
- Ternary, match, if-expression, null-coalesce, safe-navigation, and list comprehension all use the inference engine
- `interface{}` in generated code now only appears when the source actually uses `any`

## v0.1.0 — 2026-03-17

Initial release. Enums, match, try, let, lambdas, f-strings, derive, impl, generics, Result/Option, for-in, guard, defer!, unless/until/repeat, list comprehensions, swap, pipe operator, arena, pin/unpin, unsafe cast, mmap, packed, vectorize, select!, fan out/in, concurrent, throttle, retry, breaker. LSP server with completion and go-to-definition.
