# Ferrous Wheel

Rust-inspired syntax sugar, low-level memory primitives, and concurrency patterns for Go. 33 language features compiled to standard Go. No runtime library, no CGO.

Built on [gotreesitter](https://github.com/odvcencio/gotreesitter)'s `grammargen` — a pure-Go grammar generator with production-grade Go grammar support (100% parity with tree-sitter's C implementation). Inspired by [dingo](https://github.com/MadAppGang/dingo) by MadAppGang — this is a separate project exploring similar ideas through grammar composition.

## Quick taste

```
package main

import "fmt"

enum Color { Red, Green, Blue(int) }

derive Stringer for Color

impl Color {
    fn describe(self) {
        match self.tag {
            0 => fmt.Println("warm"),
            1 => fmt.Println("natural"),
            2 => fmt.Println("cool"),
        }
    }
}

func main() {
    let colors = [Color{} for _ in 0..3]

    for c in colors {
        unless c.tag == 0 {
            fmt.Println(c)
        }
    }

    let name = "world"
    fmt.Println(f"Hello, {name}!")
}
```

## Install

```bash
go install github.com/odvcencio/ferrous-wheel/cmd/ferrous-wheel@latest
```

## Usage

```bash
ferrous-wheel build myfile.fw    # transpile to Go
ferrous-wheel run myfile.fw      # transpile + execute
go run myfile_generated.go       # or run the output directly
```

## All 33 features

### Type system

| Syntax | Compiles to |
|--------|------------|
| `enum Color { Red, Green, Blue(int) }` | struct + const + constructors |
| `derive Stringer for Color` | auto-generated `String()` method |
| `derive JSON for Color` | auto-generated `MarshalJSON`/`UnmarshalJSON` |
| `derive Equal for Point` | auto-generated `Equal()` method |
| `Result[T]` / `Option[T]` | generic types with `Unwrap`, `Map`, `AndThen`, `Filter` (auto-injected) |

### Pattern matching

| Syntax | Compiles to |
|--------|------------|
| `match val { 1 => "one", 2 => "two" }` | switch IIFE (panics on unmatched — exhaustive) |
| `match x { n if n > 0 => "pos" }` | guarded switch arms |
| `if let Blue(n) = color { ... }` | tag check + field extraction |

### Bindings and expressions

| Syntax | Compiles to |
|--------|------------|
| `let x = 42` | `x := 42` |
| `let (a, b) = f()` | `a, b := f()` |
| `fn(x) x * 2` | `func(x interface{}) interface{} { return x * 2 }` |
| `val ?? "default"` | zero-value check with fallback (works on all types via reflect) |
| `obj?.field` | nil-safe field access (via reflect, auto-imported) |
| `try doSomething()` | error propagation IIFE |
| `cond ? a : b` | ternary IIFE (unlimited nesting) |
| `f"hello {name}"` | `fmt.Sprintf("hello %v", name)` |
| `[x * 2 for x in items if x > 0]` | filter+map IIFE |
| `data |> filter(valid) |> transform` | nested function calls (pipe operator) |

### Control flow

| Syntax | Compiles to |
|--------|------------|
| `for i in 0..10 { }` | `for i := 0; i < 10; i++` |
| `for v in slice { }` | `for _, v := range slice` |
| `for i, v in slice { }` | `for i, v := range slice` |
| `guard len(data) > 0 else return err` | `if !(cond) { return err }` |
| `unless done { }` | `if !done { }` |
| `until ready { }` | `for !ready { }` |
| `repeat 5 { }` | `for _i := 0; _i < 5; _i++` |
| `swap(a, b)` | `a, b = b, a` |
| `impl Point { fn dist... }` | method declarations with receiver |
| `defer! f.Close()` | deferred call that captures returned error |

### Low-level memory

| Syntax | Compiles to |
|--------|------------|
| `arena scratch { ... }` | bump allocator via `unsafe.Pointer` on pre-allocated `[]byte` — GC-free |
| `arena scratch 4096 { ... }` | same with explicit size |
| `pin data` / `unpin data` | `runtime.SetFinalizer(nil)` + `runtime.KeepAlive` — keep GC from collecting |
| `unsafe cast(s, []byte)` | `*(*[]byte)(unsafe.Pointer(&s))` — zero-copy type punning |
| `mmap file "data.bin" as buf []byte { }` | `os.Open` + `syscall.Mmap` + auto cleanup |
| `packed struct Packet { ... }` | struct with alignment hint comment |
| `vectorize for i in 0..n { ... }` | loop with SIMD intent hint |

### Concurrency

| Syntax | Compiles to |
|--------|------------|
| `select! { msg from ch => f(msg), timeout 5s => ... }` | `select` with `<-ch` and `time.After` |
| `fan out workers, 10 { ... }` | goroutine pool with `sync.WaitGroup` |
| `fan in [ch1, ch2, ch3]` | channel merge IIFE with goroutine per source |
| `concurrent { a(); b(); c() }` | each statement in a goroutine, `WaitGroup` barrier |
| `throttle 100 { ... }` | `time.NewTicker`-based rate limiting |
| `retry 3 { ... }` | exponential backoff retry loop |
| `breaker "api" { ... }` | circuit breaker with failure tracking + cooldown |

## Design notes

- All output is standard Go. No runtime library, no hidden dependencies.
- `??` uses `reflect.ValueOf` zero-value checks — works with all types.
- `match` is exhaustive at runtime — unmatched values panic.
- `?.` uses reflection for field access on interface values.
- `arena` generates a real bump allocator using `unsafe.Pointer` — objects bypass GC.
- Concurrency primitives compile to idiomatic Go patterns (`sync.WaitGroup`, `select`, channels).
- Ferrous Wheel keywords are reserved in `.fw` files, like Rust reserves `fn`, `let`, `match`.
- Auto-injected imports: `fmt`, `reflect`, `unsafe`, `runtime`, `os`, `syscall`, `sync`, `time` — only when the corresponding feature is used.

## How it works

Ferrous Wheel extends Go's grammar using gotreesitter's `grammargen.ExtendGrammar`. The extended grammar adds ~80 rules on top of Go's 116 rules. A tree-sitter parser (pure Go, no CGO) parses `.fw` files into a concrete syntax tree, then a transpiler walks the tree and emits standard Go.

103 tests verify grammar parsing and transpiler output, including end-to-end compile-and-run tests.

The same architecture powers any grammar extension — see [danmuji](https://github.com/odvcencio/danmuji) for a BDD testing DSL built the same way.

## License

MIT
