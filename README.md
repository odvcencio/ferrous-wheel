# Ferrous Wheel

Rust-inspired syntax sugar for Go. Built on [gotreesitter](https://github.com/odvcencio/gotreesitter)'s `grammargen` — a pure-Go grammar generator with production-grade Go grammar support (100% parity with tree-sitter's C implementation). Inspired by [dingo](https://github.com/MadAppGang/dingo) by MadAppGang — this is a separate project exploring similar ideas through grammar composition.

Ferrous Wheel extends Go's grammar at the tree-sitter level, so your `.fw` files get parsed by a real incremental parser with error recovery. The same infrastructure powers [danmuji](https://github.com/odvcencio/danmuji), a BDD testing DSL for Go.

## Features

Write `.fw` files with Rust-inspired syntax, compile to standard Go.

```
package main

import "fmt"

enum Shape {
    Circle(float64),
    Rect(float64)
}

func main() {
    let c = Circle(5.0)
    let r = Rect(3.0)
    fmt.Println(c)
    fmt.Println(r)
}
```

Compiles to valid Go with `ferrous-wheel build`:

```go
type Shape struct {
    tag int
    circle0 float64
    rect0 float64
}

const (
    ShapeCircle = 0
    ShapeRect = 1
)

func Circle(v0 float64) Shape { return Shape{tag: 0, circle0: v0} }
func Rect(v0 float64) Shape { return Shape{tag: 1, rect0: v0} }

func main() {
    c := Circle(5.0)
    r := Rect(3.0)
    fmt.Println(c)
    fmt.Println(r)
}
```

### All constructs

| Ferrous Wheel | Compiles to | Status |
|--------------|-------------|--------|
| `enum Color { Red, Green, Blue(int) }` | struct + const + constructors | Working |
| `match val { 1 => "one", 2 => "two" }` | switch IIFE | Working |
| `try doSomething()` | error check + early return | Working |
| `obj?.field` | nil-safe field access (via reflect) | Working |
| `val ?? "default"` | nil check with fallback | Working (nillable types) |
| `let x = 42` | `x := 42` | Working |
| `fn(x) { return x * 2 }` | `func(x interface{}) interface{} { return x * 2 }` | Working |

## Install

```bash
go install github.com/odvcencio/ferrous-wheel/cmd/ferrous-wheel@latest
```

## Usage

```bash
ferrous-wheel build myfile.fw
go run myfile_generated.go
```

## Known limitations

The Go grammar backing this is production-grade (100% parity). The transpiler has known edges:

- `??` only works with nillable types (interface, pointer, slice, map, channel)
- `match` is not exhaustive — unmatched values return nil
- `fn(x) expr` only works for simple expressions; use `fn(x) { body }` for binary operations
- `?.` uses reflection for field access on interface values (requires `reflect` import)
- Ferrous Wheel keywords (`enum`, `match`, `let`, `fn`, `try`) are reserved in `.fw` files

## How it works

Ferrous Wheel extends Go's grammar using gotreesitter's `grammargen.ExtendGrammar`. The extended grammar adds ~15 rules on top of Go's 116 rules. A tree-sitter parser (pure Go, no CGO) parses `.fw` files into a concrete syntax tree, then a transpiler walks the tree and emits standard Go.

The same architecture powers any grammar extension — see [danmuji](https://github.com/odvcencio/danmuji) for a BDD testing DSL built the same way, and [grammarlsp](https://github.com/odvcencio/gotreesitter/tree/danmuji/grammarlsp) for a generic LSP proxy that gives any grammar extension IDE support.

## License

MIT
