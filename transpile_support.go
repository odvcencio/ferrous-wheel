package ferrouswheel

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// detectGenericTypes scans transpiled output for Result[T] and Option[T] usage.
func (t *fwTranspiler) detectGenericTypes(code string) {
	if strings.Contains(code, "Result[") || strings.Contains(code, "Ok[") || strings.Contains(code, "Err[") {
		t.needsResultType = true
	}
	if strings.Contains(code, "Option[") || strings.Contains(code, "Some[") || strings.Contains(code, "None[") {
		t.needsOptionType = true
	}
}

const resultTypeDef = `
// Result is a Rust-inspired Result type for explicit error handling.
type Result[T any] struct {
	val T
	err error
	ok  bool
}

func Ok[T any](v T) Result[T]    { return Result[T]{val: v, ok: true} }
func Err[T any](e error) Result[T] { return Result[T]{err: e} }
func (r Result[T]) Unwrap() T     { if !r.ok { panic(r.err) }; return r.val }
func (r Result[T]) UnwrapOr(def T) T {
	if !r.ok {
		return def
	}
	return r.val
}
func (r Result[T]) IsOk() bool  { return r.ok }
func (r Result[T]) IsErr() bool { return !r.ok }
func (r Result[T]) Map(f func(T) T) Result[T] {
	if r.ok {
		return Ok[T](f(r.val))
	}
	return r
}
func (r Result[T]) AndThen(f func(T) Result[T]) Result[T] {
	if r.ok {
		return f(r.val)
	}
	return r
}
`

const optionTypeDef = `
// Option is a Rust-inspired Option type for explicit nil handling.
type Option[T any] struct {
	val  T
	some bool
}

func Some[T any](v T) Option[T] { return Option[T]{val: v, some: true} }
func None[T any]() Option[T]    { return Option[T]{} }
func (o Option[T]) Unwrap() T   { if !o.some { panic("unwrap on None") }; return o.val }
func (o Option[T]) UnwrapOr(def T) T {
	if !o.some {
		return def
	}
	return o.val
}
func (o Option[T]) IsSome() bool { return o.some }
func (o Option[T]) IsNone() bool { return !o.some }
func (o Option[T]) Map(f func(T) T) Option[T] {
	if o.some {
		return Some[T](f(o.val))
	}
	return o
}
func (o Option[T]) Filter(f func(T) bool) Option[T] {
	if o.some && f(o.val) {
		return o
	}
	return None[T]()
}
`

const breakerHelperDef = `
var _fwBreakerStates sync.Map

type _fwBreakerState struct {
	mu       sync.Mutex
	failures int
	lastFail time.Time
}

func _fwBreakerGetState(name string) *_fwBreakerState {
	state, _ := _fwBreakerStates.LoadOrStore(name, &_fwBreakerState{})
	return state.(*_fwBreakerState)
}

func _fwBreakerDo(name string, threshold int, cooldown time.Duration, fn func()) {
	state := _fwBreakerGetState(name)
	state.mu.Lock()
	open := state.failures >= threshold && time.Since(state.lastFail) < cooldown
	state.mu.Unlock()
	if open {
		return
	}

	success := false
	func() {
		defer func() {
			if recover() != nil {
				return
			}
			success = true
		}()
		fn()
	}()

	state.mu.Lock()
	defer state.mu.Unlock()
	if success {
		state.failures = 0
		return
	}
	state.failures++
	state.lastFail = time.Now()
}
`

const throttleHelperDef = `
var _fwThrottleStates sync.Map

type _fwThrottleState struct {
	mu     sync.Mutex
	last   time.Time
	tokens float64
}

func _fwThrottleWait(name string, rate int, burst int) {
	if rate <= 0 {
		panic("throttle rate must be > 0")
	}
	if burst <= 0 {
		panic("throttle burst must be > 0")
	}

	stateAny, _ := _fwThrottleStates.LoadOrStore(name, &_fwThrottleState{
		last:   time.Now(),
		tokens: 0,
	})
	state := stateAny.(*_fwThrottleState)

	for {
		now := time.Now()

		state.mu.Lock()
		elapsed := now.Sub(state.last).Seconds()
		state.last = now
		if elapsed > 0 {
			state.tokens += elapsed * float64(rate)
			if state.tokens > float64(burst) {
				state.tokens = float64(burst)
			}
		}

		if state.tokens >= 1 {
			state.tokens--
			state.mu.Unlock()
			return
		}

		waitSeconds := (1 - state.tokens) / float64(rate)
		state.mu.Unlock()

		wait := time.Duration(waitSeconds * float64(time.Second))
		if wait <= 0 {
			wait = time.Nanosecond
		}
		time.Sleep(wait)
	}
}
`

const fanInHelperDef = `
func _fwFanIn(chans ...interface{}) <-chan interface{} {
	out := make(chan interface{})
	var wg sync.WaitGroup

	for _, ch := range chans {
		rv := reflect.ValueOf(ch)
		if !rv.IsValid() || rv.Kind() != reflect.Chan {
			continue
		}

		wg.Add(1)
		go func(c reflect.Value) {
			defer wg.Done()
			for {
				v, ok := c.Recv()
				if !ok {
					return
				}
				out <- v.Interface()
			}
		}(rv)
	}

	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}
`

const unsafeCastHelperDef = `
func _fwUnsafeCast[To any, From any](from From) To {
	var out To

	if data, ok := any(from).([]byte); ok {
		size := int(unsafe.Sizeof(out))
		if len(data) < size {
			panic("unsafe cast requires enough bytes in source slice")
		}
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&out)), size), data[:size])
		return out
	}

	if unsafe.Sizeof(from) != unsafe.Sizeof(out) {
		panic("unsafe cast size mismatch")
	}
	return *(*To)(unsafe.Pointer(&from))
}
`

// injectGenericTypes appends Result and Option type definitions at the end of
// the file when the transpiled code references them.
func (t *fwTranspiler) injectGenericTypes(code string) string {
	if !t.needsResultType && !t.needsOptionType {
		return code
	}
	var b strings.Builder
	b.WriteString(code)
	if t.needsResultType {
		b.WriteString(resultTypeDef)
	}
	if t.needsOptionType {
		b.WriteString(optionTypeDef)
	}
	return b.String()
}

func (t *fwTranspiler) injectSupportCode(code string) string {
	if !t.needsBreaker && !t.needsThrottle && !t.needsFanIn && !t.needsUnsafeCast {
		return code
	}
	var b strings.Builder
	b.WriteString(code)
	if t.needsBreaker {
		b.WriteString(breakerHelperDef)
	}
	if t.needsThrottle {
		b.WriteString(throttleHelperDef)
	}
	if t.needsFanIn {
		b.WriteString(fanInHelperDef)
	}
	if t.needsUnsafeCast {
		b.WriteString(unsafeCastHelperDef)
	}
	return b.String()
}

func importedPaths(code string) map[string]struct{} {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generated.go", code, parser.ImportsOnly)
	if err == nil {
		imports := make(map[string]struct{}, len(file.Imports))
		for _, imp := range file.Imports {
			if imp == nil || imp.Path == nil {
				continue
			}
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			imports[path] = struct{}{}
		}
		return imports
	}

	imports := make(map[string]struct{})
	lines := strings.Split(code, "\n")
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case inBlock:
			if trimmed == ")" {
				inBlock = false
				continue
			}
			if path, ok := importPathFromLine(trimmed); ok {
				imports[path] = struct{}{}
			}
		case strings.HasPrefix(trimmed, "import ("):
			inBlock = true
		case strings.HasPrefix(trimmed, "import "):
			if path, ok := importPathFromLine(strings.TrimSpace(strings.TrimPrefix(trimmed, "import"))); ok {
				imports[path] = struct{}{}
			}
		}
	}
	return imports
}

func importPathFromLine(line string) (string, bool) {
	start := strings.IndexByte(line, '"')
	end := strings.LastIndexByte(line, '"')
	if start < 0 || end <= start {
		return "", false
	}
	path, err := strconv.Unquote(line[start : end+1])
	if err != nil {
		return "", false
	}
	return path, true
}

// injectImports adds required imports after transpilation.
// It appends to an existing import block when present, or inserts a new block
// after the package clause otherwise.
func (t *fwTranspiler) injectImports(code string) string {
	imported := importedPaths(code)

	var needed []string
	if t.needsReflect {
		if _, ok := imported["reflect"]; !ok {
			needed = append(needed, `"reflect"`)
		}
	}
	if t.needsFmt {
		if _, ok := imported["fmt"]; !ok {
			needed = append(needed, `"fmt"`)
		}
	}
	if t.needsJSON {
		if _, ok := imported["encoding/json"]; !ok {
			needed = append(needed, `"encoding/json"`)
		}
	}
	if t.needsUnsafe {
		if _, ok := imported["unsafe"]; !ok {
			needed = append(needed, `"unsafe"`)
		}
	}
	if t.needsRuntime {
		if _, ok := imported["runtime"]; !ok {
			needed = append(needed, `"runtime"`)
		}
	}
	if t.needsOS {
		if _, ok := imported["os"]; !ok {
			needed = append(needed, `"os"`)
		}
	}
	if t.needsSyscall {
		if _, ok := imported["syscall"]; !ok {
			needed = append(needed, `"syscall"`)
		}
	}
	if t.needsSync {
		if _, ok := imported["sync"]; !ok {
			needed = append(needed, `"sync"`)
		}
	}
	if t.needsTime {
		if _, ok := imported["time"]; !ok {
			needed = append(needed, `"time"`)
		}
	}
	if len(needed) == 0 {
		return code
	}

	if idx := strings.Index(code, "import ("); idx >= 0 {
		insertAt := idx + len("import (")
		var inject strings.Builder
		for _, imp := range needed {
			inject.WriteString("\n\t")
			inject.WriteString(imp)
		}
		return code[:insertAt] + inject.String() + code[insertAt:]
	}

	if idx := strings.Index(code, "import "); idx >= 0 {
		endIdx := strings.Index(code[idx:], "\n")
		if endIdx >= 0 {
			endIdx += idx
			var inject strings.Builder
			for _, imp := range needed {
				inject.WriteString("\nimport ")
				inject.WriteString(imp)
			}
			return code[:endIdx] + inject.String() + code[endIdx:]
		}
	}

	if idx := strings.Index(code, "\n"); idx >= 0 {
		var inject strings.Builder
		inject.WriteString("\n\nimport (")
		for _, imp := range needed {
			inject.WriteString("\n\t")
			inject.WriteString(imp)
		}
		inject.WriteString("\n)")
		return code[:idx] + inject.String() + code[idx:]
	}

	return code
}
