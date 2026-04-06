# Ferrous Wheel

Rust-inspired syntax sugar, low-level memory primitives, and concurrency patterns for Go. Generic enums, `derive`, `impl`, and dozens of other language features compile to standard Go. No runtime library, no CGO.

Built on [gotreesitter](https://github.com/odvcencio/gotreesitter)'s `grammargen` — a pure-Go grammar generator with production-grade Go grammar support (100% parity with tree-sitter's C implementation). Original idea inspired by [dingo](https://github.com/MadAppGang/dingo).

## Quick taste

```fw
package main

import (
    "fmt"
    "os"
)

enum Color { Red, Green, Blue(int) }

derive Stringer for Color

func loadConfig(path string) (string, error) {
    let data = os.ReadFile(path)?
    let mut result = if len(data) > 0 { string(data) } else { "default" }
    result = fmt.Sprintf("[config] %s", result)
    return result, nil
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
ferrous-wheel emit  myfile.fw                      # transpile to Go on stdout
ferrous-wheel run   myfile.fw                      # transpile + execute
ferrous-wheel build myfile.fw -o dist/myapp        # compile a native binary
ferrous-wheel fmt   myfile.fw                      # format .fw source (stdout)
ferrous-wheel fmt   -w myfile.fw                   # format in-place
ferrous-wheel fmt   --check myfile.fw              # check formatting (CI)
ferrous-wheel lint  myfile.fw                      # run lint rules
ferrous-wheel lsp                                  # start language server
```

`emit` writes standard Go source to stdout with a generated-file header. `build` accepts `-o` for the output path. `fmt` formats `.fw` source files (tabs, operator spacing, match arm alignment). `lint` runs 10 built-in rules and reports diagnostics — errors block transpilation, warnings don't.

---

## Type system

### Enums (sum types)

```fw
enum Shape {
    Circle(float64),
    Rect(float64, float64),
    Triangle(float64, float64, float64)
}

func main() {
    let c = Circle(5.0)
    let r = Rect(3.0, 4.0)
    fmt.Println(c, r)
}
```

### Derive — auto-generate implementations

```fw
enum Direction { North, South, East, West }

derive Stringer for Direction   // generates String() string
derive JSON for Direction       // generates MarshalJSON / UnmarshalJSON
derive Equal for Direction      // generates Equal(other Direction) bool
```

### Impl blocks — group methods with their type

```fw
impl Vector {
    fn length(self) float64 {
        return math.Sqrt(self.x*self.x + self.y*self.y)
    }

    fn normalize(self) Vector {
        let l = self.length()
        return Vector{x: self.x / l, y: self.y / l}
    }

    fn dot(self, other Vector) float64 {
        return self.x*other.x + self.y*other.y
    }
}
```

### Generic enums, derives, and impl blocks

```fw
enum Option[T any] {
    Some(T),
    None,
}

type Box[T any] struct {
    value T
}

derive Equal for Box[T]
derive JSON for Box[T]

impl Box[T] {
    fn unwrap(self) T {
        return self.value
    }
}
```

FW-native constructs now preserve generic parameters end to end, so enum constructors, derived methods, and `impl` receivers emit valid generic Go.

### Result and Option types

```fw
func findUser(id int) Result[User] {
    if id <= 0 {
        return Err[User](errors.New("invalid id"))
    }
    return Ok(User{ID: id, Name: "Alice"})   // T inferred from the value
}

func findNickname(id int) Option[string] {
    if id <= 0 {
        return None[string]()
    }
    return Some("alice")                     // T inferred from the value
}

func main() {
    let result = findUser(1)
    let user = result.UnwrapOr(User{Name: "default"})
    let mapped = result.Map(func(u User) User { u.Name = strings.ToUpper(u.Name); return u })
}
```

Built-in `Result` and `Option` helpers are injected only when that helper family is actually used. Ferrous Wheel also avoids colliding with user-defined `Result`, `Option`, `Ok`, `Err`, `Some`, or `None` declarations.

---

## Pattern matching

### Match expressions

```fw
func describe(val interface{}) string {
    return match val {
        1 => "one",
        2 => "two",
        3 => "three",
    }
}
```

### Match with guard clauses

```fw
func classify(n int) string {
    return match n {
        0 => "zero",
        n if n < 0 => "negative",
        n if n > 100 => "large",
        n if n % 2 == 0 => "small even",
        _ => "small odd",
    }
}
```

### If-let — conditional binding

```fw
func process(user *User) {
    if let u = user {
        fmt.Println(f"Hello, {u.Name}")
    } else {
        fmt.Println("No user")
    }
}
```

---

## Expressions and bindings

### Let bindings — immutable by default

```fw
let x = 42                                      // immutable — reassignment is a compile error
let mut counter = 0                              // mutable — reassignment allowed
let name = "Alice"
let (host, port) = parseAddr("localhost:8080")
let mut (a, b) = getCoords()                    // mutable multi-binding
```

Go-native `:=` and `var` remain unrestricted. Immutability is a compile-time check — both `let` and `let mut` emit `:=` in Go.

### Lambdas

```fw
let double = fn(x) x * 2
let add = fn(x, y) { return x + y }
let greet = fn(name) f"Hello, {name}!"

let sorted = sort(items, fn(a, b) a.Score > b.Score)
```

### F-strings — string interpolation

```fw
let name = "world"
let count = 42
fmt.Println(f"Hello, {name}! You have {count} items.")
fmt.Println(f"Total: {price * quantity}")
```

### Null coalescing — works on ALL types

```fw
let name = username ?? "anonymous"     // string: empty → default
let count = getCount() ?? 0           // int: zero → default
let user = findUser(id) ?? fallback   // pointer: nil → default
```

### Safe navigation

```fw
let city = user?.Address?.City
let name = getManager()?.Name ?? "unassigned"
```

### Ternary

```fw
let label = count > 0 ? "items" : "empty"
let status = connected ? (healthy ? "ok" : "degraded") : "offline"
```

### If-expressions

```fw
let x = if cond { a } else { b }
let y = if n > 0 { "positive" } else if n == 0 { "zero" } else { "negative" }
```

`if`/`else` in expression position (let RHS, return, call args) compiles to an IIFE. `else` is required. `try`/`?` inside branches is disallowed (would return from the IIFE, not the enclosing function).

### Error propagation

```fw
func readData(path string) ([]byte, error) {
    let file = try os.Open(path)         // prefix try
    let data = io.ReadAll(file)?          // postfix ?
    return data, nil
}
```

Both `try` (prefix) and `?` (postfix) strip the error from `(T, error)` and propagate on failure. Use `try` for statement-level readability, `?` for inline/chained expressions. The enclosing function must return `error`.

### List comprehensions

```fw
let squares = [x * x for x in 0..10]
let evens = [x for x in items if x % 2 == 0]
let names = [user.Name for user in users if user.Active]
```

### Pipe operator

```fw
let result = rawData
    |> parse
    |> validate
    |> normalize
    |> store
```

---

## Control flow

### For-in loops

```fw
for i in 0..100 {
    fmt.Println(i)
}

for line in lines {
    process(line)
}

for i, item in inventory {
    fmt.Println(f"{i}: {item.Name}")
}
```

### Guard clauses — flatten the pyramid

```fw
func processRequest(req Request) error {
    guard req.Valid() else return errors.New("invalid request")
    guard req.Authenticated() else return errors.New("unauthorized")
    guard len(req.Body) > 0 else return errors.New("empty body")

    // happy path — no nesting
    return handle(req)
}
```

### Unless and until

```fw
unless debug {
    disableVerboseLogging()
}

until server.Ready() {
    time.Sleep(100 * time.Millisecond)
}
```

### Repeat

```fw
repeat 10 {
    sendHeartbeat()
    time.Sleep(time.Second)
}
```

### Swap

```fw
swap(a, b)
swap(matrix[i][j], matrix[j][i])
```

### Defer with error capture

```fw
func writeFile(path string, data []byte) (err error) {
    let f = try os.Create(path)
    defer! f.Close()    // if Close() fails, err captures it

    written, writeErr := f.Write(data)
    _ = written
    guard writeErr == nil else return writeErr
    return nil
}
```

---

## Low-level memory

### Arena allocation — manual bump allocator helpers

```fw
arena scratch {
    // Emits `_arenaAlloc_scratch(size)` backed by a reusable byte slab.
    // Ordinary Go allocations still behave normally unless you call the helper.
    let ptr = _arenaAlloc_scratch(4096)
    let buf = unsafe cast(ptr, *[4096]byte)
    processInPlace(buf[:])
}

arena bigPool 16 * 1024 * 1024 {
    let nodePtr = _arenaAlloc_bigPool(int(unsafe.Sizeof(Node{})))
    let node = unsafe cast(nodePtr, *Node)
    node.Value = 42
}
```

Arena helpers are intentionally explicit: negative sizes panic, zero-size allocations return `nil`, and over-capacity allocations fail fast with a clear panic instead of falling through to a raw slice panic.

### Pin / Unpin — experimental liveness hints

```fw
let data = loadLargeDataset()
pin data       // emit a runtime liveness barrier for the surrounding function

// ... hot path using data ...

unpin data     // emit an immediate KeepAlive barrier at this point
```

### Unsafe cast — raw reinterpretation of fixed-size values and byte slices

```fw
// Reinterpret a float64 as its IEEE-754 bits
let bits = unsafe cast(value, uint64)

// Copy raw bytes into a struct-shaped value
let header = unsafe cast(headerBytes, PacketHeader)
```

When the source is `[]byte`, `unsafe cast` copies the leading bytes into the destination value. For non-slice values of equal size, it reinterprets the in-memory representation directly.

### Memory-mapped I/O

```fw
mmap file "database.bin" as data []byte {
    // data is memory-mapped — reads go straight to the page cache.
    // No heap allocation for the file contents.
    let header = data[0:64]
    let records = data[64:]
    processRecords(records)
}
// File automatically unmapped and closed here.
```

`mmap` currently requires the target binding type to be `[]byte`. Open, stat, map, and unmap failures panic immediately, and empty files produce an empty slice without attempting to map zero bytes.

### Packed annotations and vectorize hints

```fw
packed struct NetworkPacket {
    Version  uint8
    Type     uint8
    Length   uint16
    Checksum uint32
    Payload  [1024]byte
}

vectorize for i in 0..len(data) {
    result[i] = data[i] * scale + offset
}
```

`packed` currently emits a layout warning comment rather than changing Go's field alignment rules.

---

## Concurrency

### Select with sugar

```fw
select! {
    msg from inbox => handleMessage(msg),
    err from errors => log.Fatal(f"error: {err}"),
    tick from heartbeat => sendPing(),
    timeout 30 => log.Warn("idle timeout"),
    default => runtime.Gosched(),
}
```

### Fan out — parallel worker pool

```fw
fan out workers, runtime.NumCPU() {
    for job in jobs {
        let result = process(job)
        results <- result
    }
}
// All workers complete before this line.
```

### Fan in — merge channels

```fw
let merged = fan in [userEvents, systemEvents, adminEvents]

for event in merged {
    dispatch(event)
}
```

### Structured concurrency

```fw
var users []User
var orders []Order
var inventory []Inventory

concurrent {
    users = fetchUsers()
    orders = fetchOrders()
    inventory = fetchInventory()
}
// All three assignments complete here.
let report = buildReport(users, orders, inventory)
```

### Pipeline operator with channels

```fw
let processed = rawStream
    |> decode
    |> validate
    |> enrich
    |> store
```

### Throttle — rate limiting

```fw
for req in requests {
    throttle 1000 {
        handle(req)
    }
}
```

### Retry with exponential backoff

```fw
retry 5 {
    let (resp, err) = http.Get("https://flaky-api.com/data")
    guard err == nil else return err
    guard resp.StatusCode == 200 else return errors.New(f"status {resp.StatusCode}")
}
```

### Circuit breaker

```fw
breaker "payment-gateway" {
    mustCharge(amount)   // panic on failure
}
// After 5 failures, the breaker opens for 30s.
// Requests during cooldown skip the body entirely.
```

---

## Real-world examples

### Concurrent web scraper

```fw
func scrape(urls []string) []Page {
    let pages = make([]Page, len(urls))

    fan out scrapers, 10 {
        for i, url in urls {
            retry 3 {
                let resp = try http.Get(url)
                let body = try io.ReadAll(resp.Body)
                pages[i] = Page{URL: url, Body: body}
            }
        }
    }

    return [p for p in pages if len(p.Body) > 0]
}
```

### Binary parser

```fw
func parseFrame(raw []byte) Frame {
    guard len(raw) >= 8 else return Frame{}

    let header = unsafe cast(raw[0:4], uint32)
    let length = unsafe cast(raw[4:8], uint32)

    match header {
        0x01 => Frame{Type: "data", Payload: raw[8:8+length]},
        0x02 => Frame{Type: "ack"},
        0xFF => Frame{Type: "close"},
    }
}
```

### Resilient service handler

```fw
func handleOrder(ctx context.Context, order Order) (err error) {
    guard order.Valid() else return errors.New("invalid order")

    var user User
    retry 3 {
        user, err = userClient.Get(ctx, order.UserID)
        guard err == nil else return err
    }

    var inventory Reservation
    var payment Payment
    concurrent {
        inventory = inventoryClient.Reserve(order.Items)
        payment = paymentClient.Authorize(order.Total)
    }

    guard inventory.OK else return errors.New(f"stock: {inventory.Error}")
    guard payment.OK else return errors.New(f"payment: {payment.Error}")

    throttle 100 {
        analytics.Track(f"order:{order.ID}", user.ID)
    }

    breaker "audit-log" {
        mustAudit(order.ID)
    }

    return nil
}
```

### High-performance data pipeline

```fw
func processCSV(path string) []Record {
    mmap file path as data []byte {
        let lines = split(data, '\n')
        let records = [parseLine(l) for l in lines if len(l) > 0]

        fan out workers, runtime.NumCPU() {
            vectorize for i in 0..len(records) {
                records[i].Score = computeScore(records[i])
            }
        }

        return records
    }
}
```

---

## Design notes

- All output is standard Go. No runtime library, no hidden dependencies.
- `let` bindings are immutable by default; `let mut` opts into reassignment. Go-native `:=`/`var` remain unrestricted.
- Bidirectional type inference with constraint solving resolves concrete types through FW constructs (ternary, match, if-expressions, lambdas, pipelines). `interface{}` in generated code only appears when the source actually uses `any`.
- Lambda parameter types are inferred from call-site context — `Map(items, fn(x) x * 2)` infers `x` as `int` from `Map`'s signature.
- Generic support covers plain Go generics plus FW-native `enum`, `derive`, and `impl` constructs.
- `??` uses typed zero-value checks when the type is resolved; falls back to `reflect.ValueOf` only when type information is unavailable.
- `match` is exhaustive at runtime — unmatched values panic.
- `?.` uses direct field access when the object type is resolved; falls back to reflection only for unresolved types.
- Built-in `Result` and `Option` helpers are injected only when actually used.
- 10 built-in lint rules run automatically before transpilation. Errors block, warnings report.
- Concurrency primitives compile to Go patterns (`sync.WaitGroup`, `select`, channels).
- `arena` generates bump-allocation helpers; `mmap` validates `[]byte` targets.
- Ferrous Wheel feature keywords are parsed contextually inside `.fw` files.
- Auto-injected imports: `fmt`, `reflect`, `unsafe`, `runtime`, `os`, `syscall`, `sync`, `time` — only when the corresponding feature is used.

## How it works

Ferrous Wheel extends Go's grammar using gotreesitter's `grammargen.ExtendGrammar`. The extended grammar adds ~80 rules on top of Go's 116 rules. A tree-sitter parser (pure Go, no CGO) parses `.fw` files into a concrete syntax tree, then a transpiler walks the tree and emits standard Go.

The suite includes parser tests, transpiler tests, end-to-end compile/run checks, fuzzing, and race detection.

The same architecture powers any grammar extension — see [danmuji](https://github.com/odvcencio/danmuji) for a BDD testing DSL built the same way.

## Tooling

### VS Code extension

Install from the [VS Code Marketplace](https://marketplace.visualstudio.com/items?itemName=odvcencio.ferrous-wheel) or build from source:

```bash
cd editor/vscode && npm install && npx vsce package
```

Features: syntax highlighting, LSP integration (hover, completion, go-to-definition, diagnostics), and snippets for all FW constructs.

### Playground

Try Ferrous Wheel in the browser at [play.ferrous-wheel.dev](https://odvcencio.github.io/ferrous-wheel/) — write `.fw` code, see the Go output live, and run it.

### Benchmarks

Runtime parity benchmarks prove FW-generated Go performs identically to hand-written Go:

```bash
go test -bench=. -benchmem ./bench/...
```

See [bench/README.md](bench/README.md) for results.

## License

MIT
