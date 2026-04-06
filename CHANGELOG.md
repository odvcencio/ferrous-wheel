# Changelog

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
