# Ferrous Wheel — VS Code Extension

Ferrous Wheel is a Rust-inspired syntax layer that transpiles to idiomatic Go. It brings enums, pattern matching, impl blocks, guard statements, and structured concurrency primitives (`fan`, `retry`, `breaker`, `select!`) to Go codebases — with zero runtime overhead, since all constructs compile down to plain Go.

## Features

- **Syntax highlighting** — full TextMate grammar for `.fw` files covering keywords, types, literals, operators, and comments
- **Language Server Protocol** — powered by `ferrous-wheel lsp` (stdin/stdout JSON-RPC)
  - Hover documentation
  - Completion suggestions
  - Go-to-definition
  - Diagnostics on save
- **Snippets** — ready-to-use templates for all FW constructs: `enum`, `match`, `impl`, `let`, `let mut`, `guard`, `fan`, `retry`, `breaker`, `mmap`, `select!`

## Requirements

The `ferrous-wheel` binary must be on your `PATH`. Install it with:

```sh
go install github.com/odvcencio/ferrous-wheel@latest
```

Or download a release binary from the [main repository](https://github.com/odvcencio/ferrous-wheel).

## Settings

| Setting | Default | Description |
|---|---|---|
| `ferrous-wheel.serverPath` | `""` | Absolute path to the `ferrous-wheel` binary. Leave empty to use `PATH`. |

## Links

- [Repository](https://github.com/odvcencio/ferrous-wheel)
- [Language Reference](https://github.com/odvcencio/ferrous-wheel/blob/main/docs/)
