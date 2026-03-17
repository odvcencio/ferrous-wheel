# Ferrous Wheel

Rust-inspired syntax sugar for Go, built on [grammargen](https://github.com/odvcencio/gotreesitter). Inspired by [dingo](https://github.com/nicholasgasior/gopher-dingo).

A proof-of-concept showing how grammargen can extend any language grammar with custom syntax that compiles to standard code.

## Features

| Ferrous Wheel | Go output |
|--------------|-----------|
| `enum Color { Red, Green, Blue(int) }` | struct + const + constructors |
| `match val { 1 => "one", 2 => "two" }` | switch expression |
| `try doSomething()` | error propagation |
| `obj?.field` | nil-safe field access |
| `val ?? "default"` | null coalescing |
| `let x = 42` | `x := 42` |
| `fn(x) x * 2` | anonymous function |

## Install

```bash
go install github.com/odvcencio/ferrous-wheel/cmd/ferrous-wheel@latest
```

## Usage

```bash
ferrous-wheel build myfile.fw
go run myfile_generated.go
```

## How it works

Ferrous Wheel extends Go's grammar using gotreesitter's grammargen package. The extended grammar parses `.fw` files into a syntax tree, then a transpiler walks the tree and emits standard Go code.

## Status

Proof of concept. 14 tests passing.

## License

MIT
