# Ferrous Wheel Benchmarks

All numbers collected on an Intel Core Ultra 9 285 (20 threads), Linux/amd64, Go 1.25, `-count=3 -benchmem`.

## Runtime Parity

Measures whether transpiled `.fw` code performs the same as idiomatic Go at runtime.

| Benchmark | FW (ns/op) | Go (ns/op) | Allocs | Verdict |
|---|---|---|---|---|
| Fibonacci(30) | ~6 | ~6 | 0 | Parity |
| JSON Roundtrip | ~657 | ~615 | 8 | Parity |
| Enum Classify (×1000) | ~749 | ~80 | 0 | ~9x overhead (IIFE) |
| Fan Out (1000) | ~224µs | ~224µs | 1002 | Parity |
| Comprehension (10000) | ~95µs | ~103µs | 4887 | Parity |

## Transpilation Speed

Measures how long the transpiler takes to parse and emit Go code for each `.fw` source file (steady-state, excluding first-run JIT/GC warmup).

| Benchmark | ms/op | MB/op | Allocs/op |
|---|---|---|---|
| fibonacci.fw | ~4.15 | ~8.8 | 23,500 |
| enum.fw | ~4.00 | ~8.8 | 23,652 |
| fanout.fw | ~3.65 | ~8.8 | 23,588 |
| comprehension.fw | ~3.68 | ~8.8 | 23,495 |
| json.fw | ~36.3 | ~11.4 | 38,237 |

`json.fw` is ~9x slower to transpile than the others because it exercises significantly more grammar surface area (struct literals, method calls, interface assertions, error handling chains).

## Note on match/enum IIFE overhead

The `Enum Classify` benchmark shows ~9x overhead compared to idiomatic Go. This is expected and honest: the transpiler compiles `match` expressions to immediately-invoked function expressions (IIFEs) so they work as expressions in any position. Each call allocates a closure frame, whereas the Go version uses a plain `switch` statement. All other benchmarks show full parity — the IIFE overhead only materialises in tight loops over `match` expressions.

## How to run

```bash
# Compile benchmarks only
go test -bench=BenchmarkTranspile -benchmem -count=3 ./bench/...

# All benchmarks
go test -bench=. -benchmem -count=3 ./bench/...

# Save results
go test -bench=. -benchmem -count=3 ./bench/... | tee bench/results.txt
```
