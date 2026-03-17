# Ferrous Wheel

Rust-inspired syntax sugar, low-level memory primitives, and concurrency patterns for Go. 33 language features compiled to standard Go. No runtime library, no CGO.

Built on [gotreesitter](https://github.com/odvcencio/gotreesitter)'s `grammargen` — a pure-Go grammar generator with production-grade Go grammar support (100% parity with tree-sitter's C implementation). Inspired by [dingo](https://github.com/MadAppGang/dingo) by MadAppGang — this is a separate project exploring similar ideas through grammar composition.

## Quick taste

```fw
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

### Result and Option types

```fw
func findUser(id int) Result[User] {
    if id <= 0 {
        return Err[User](errors.New("invalid id"))
    }
    return Ok(User{ID: id, Name: "Alice"})
}

func main() {
    let result = findUser(1)
    let user = result.UnwrapOr(User{Name: "default"})
    let mapped = result.Map(func(u User) User { u.Name = strings.ToUpper(u.Name); return u })
}
```

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

### If-let — pattern destructuring

```fw
func process(color Color) {
    if let Blue(intensity) = color {
        fmt.Println(f"Blue with intensity {intensity}")
    } else {
        fmt.Println("Not blue")
    }
}
```

---

## Expressions and bindings

### Let bindings

```fw
let x = 42
let name = "Alice"
let (host, port) = parseAddr("localhost:8080")
let (user, err) = db.FindUser(id)
```

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

### Error propagation

```fw
let file = try os.Open("data.txt")
let data = try io.ReadAll(file)
let config = try json.Unmarshal(data)
```

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

    try f.Write(data)
    return nil
}
```

---

## Low-level memory

### Arena allocation — bypass the GC

```fw
arena scratch {
    // Everything allocated here lives on a single []byte slab.
    // GC never scans it. Freed all at once when the block exits.
    let buf = make([]byte, 4096)
    processInPlace(buf)
}

arena bigPool 16 * 1024 * 1024 {
    // 16MB arena for bulk processing
    let nodes = buildTree(data)
    let result = traverse(nodes)
}
```

### Pin / Unpin — control GC lifetime

```fw
let data = loadLargeDataset()
pin data       // GC won't collect or move this

// ... hot path using data, no GC pauses ...

unpin data     // GC can reclaim when ready
```

### Unsafe cast — zero-copy type punning

```fw
// String to []byte without allocation
let bytes = unsafe cast(str, []byte)

// Reinterpret raw memory as a struct
let header = unsafe cast(rawBytes[0:12], PacketHeader)
```

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

### Packed structs and vectorize hints

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
concurrent {
    let users = fetchUsers()
    let orders = fetchOrders()
    let inventory = fetchInventory()
}
// All three complete. Results available here.
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
throttle 1000 {
    for req in requests {
        handle(req)
    }
}
```

### Retry with exponential backoff

```fw
retry 5 {
    let resp = http.Get("https://flaky-api.com/data")
    guard resp.StatusCode == 200 else return errors.New(f"status {resp.StatusCode}")
}
```

### Circuit breaker

```fw
breaker "payment-gateway" {
    let charge = paymentClient.Charge(amount)
    guard charge.Success else return charge.Error
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

### Zero-copy binary parser

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

    let user = retry 3 {
        breaker "user-service" {
            try userClient.Get(ctx, order.UserID)
        }
    }

    concurrent {
        let inventory = inventoryClient.Reserve(order.Items)
        let payment = paymentClient.Authorize(order.Total)
    }

    guard inventory.OK else return errors.New(f"stock: {inventory.Error}")
    guard payment.OK else return errors.New(f"payment: {payment.Error}")

    throttle 100 {
        analytics.Track(f"order:{order.ID}", user.ID)
    }

    return nil
}
```

### High-performance data pipeline

```fw
func processCSV(path string) []Record {
    mmap file path as data []byte {
        arena pool 64 * 1024 * 1024 {
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
}
```

---

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
