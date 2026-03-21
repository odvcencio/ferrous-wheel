package ferrouswheel

import "testing"

// --- TypeEnv core tests (Task 4) ---

func TestTypeEnvRegisterAndResolveVariable(t *testing.T) {
	env := NewTypeEnv()
	env.RegisterVar("x", Primitive("int"))
	typ, err := env.LookupVar("x")
	if err != nil {
		t.Fatalf("LookupVar: %v", err)
	}
	if typ.String() != "int" {
		t.Errorf("got %s, want int", typ)
	}
}

func TestTypeEnvScopeStack(t *testing.T) {
	env := NewTypeEnv()
	env.RegisterVar("x", Primitive("int"))
	env.PushScope()
	env.RegisterVar("x", Primitive("string")) // shadows
	typ, _ := env.LookupVar("x")
	if typ.String() != "string" {
		t.Errorf("inner scope: got %s, want string", typ)
	}
	env.PopScope()
	typ, _ = env.LookupVar("x")
	if typ.String() != "int" {
		t.Errorf("outer scope: got %s, want int", typ)
	}
}

func TestTypeEnvRegisterFunc(t *testing.T) {
	env := NewTypeEnv()
	env.RegisterFunc("greet", &FuncType{
		Params:  []Type{Primitive("string")},
		Results: []Type{Primitive("string")},
	})
	typ, err := env.LookupFunc("greet")
	if err != nil {
		t.Fatalf("LookupFunc: %v", err)
	}
	if typ.String() != "func(string) string" {
		t.Errorf("got %s", typ)
	}
}

func TestTypeEnvRegisterStruct(t *testing.T) {
	env := NewTypeEnv()
	env.RegisterStruct("Point", &StructType{
		Name:       "Point",
		Fields:     map[string]Type{"x": Primitive("float64"), "y": Primitive("float64")},
		Comparable: true,
	})
	typ, err := env.LookupStruct("Point")
	if err != nil {
		t.Fatalf("LookupStruct: %v", err)
	}
	if typ.Fields["x"].String() != "float64" {
		t.Errorf("field x: got %s", typ.Fields["x"])
	}
}

func TestTypeEnvRegisterEnum(t *testing.T) {
	env := NewTypeEnv()
	env.RegisterEnum("Shape", &EnumType{
		Name: "Shape",
		Variants: map[string][]Type{
			"Circle": {Primitive("float64")},
			"Rect":   {Primitive("float64"), Primitive("float64")},
		},
	})
	typ, err := env.LookupEnum("Shape")
	if err != nil {
		t.Fatalf("LookupEnum: %v", err)
	}
	if len(typ.Variants["Circle"]) != 1 {
		t.Errorf("Circle variants: got %d", len(typ.Variants["Circle"]))
	}
}

func TestTypeEnvLookupMissing(t *testing.T) {
	env := NewTypeEnv()
	_, err := env.LookupVar("nonexistent")
	if err == nil {
		t.Error("expected error for missing variable")
	}
}

// --- Collect pass tests (Task 6) ---

func TestCollectLetBinding(t *testing.T) {
	src := []byte(`package main
func main() {
	let x = 42
}`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	// After collect, function "main" should be registered
	_, err = env.LookupFunc("main")
	if err != nil {
		t.Error("main function not collected")
	}
}

func TestCollectFunctionSignature(t *testing.T) {
	src := []byte(`package main
func add(a int, b int) int {
	return a + b
}`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	fn, err := env.LookupFunc("add")
	if err != nil {
		t.Fatalf("LookupFunc: %v", err)
	}
	if len(fn.Params) != 2 || fn.Params[0].String() != "int" {
		t.Errorf("params: %v", fn.Params)
	}
	if len(fn.Results) != 1 || fn.Results[0].String() != "int" {
		t.Errorf("results: %v", fn.Results)
	}
}

func TestCollectFunctionNoReturn(t *testing.T) {
	src := []byte(`package main
func hello(name string) {
	fmt.Println(name)
}`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	fn, err := env.LookupFunc("hello")
	if err != nil {
		t.Fatalf("LookupFunc: %v", err)
	}
	if len(fn.Params) != 1 || fn.Params[0].String() != "string" {
		t.Errorf("params: %v", fn.Params)
	}
	if len(fn.Results) != 0 {
		t.Errorf("results: expected 0, got %d", len(fn.Results))
	}
}

func TestCollectEnum(t *testing.T) {
	src := []byte(`package main
enum Shape { Circle(float64), Rect(float64, float64) }
`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	e, err := env.LookupEnum("Shape")
	if err != nil {
		t.Fatalf("LookupEnum: %v", err)
	}
	if len(e.Variants["Circle"]) != 1 {
		t.Errorf("Circle: got %d params", len(e.Variants["Circle"]))
	}
	if len(e.Variants["Rect"]) != 2 {
		t.Errorf("Rect: got %d params", len(e.Variants["Rect"]))
	}
}

func TestCollectEnumSimpleVariants(t *testing.T) {
	src := []byte(`package main
enum Color { Red, Green, Blue }
`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	e, err := env.LookupEnum("Color")
	if err != nil {
		t.Fatalf("LookupEnum: %v", err)
	}
	for _, variant := range []string{"Red", "Green", "Blue"} {
		types, ok := e.Variants[variant]
		if !ok {
			t.Errorf("variant %s not found", variant)
		}
		if len(types) != 0 {
			t.Errorf("variant %s: expected 0 types, got %d", variant, len(types))
		}
	}
}

func TestCollectStructFromGoType(t *testing.T) {
	src := []byte(`package main
type Point struct {
	x float64
	y float64
}`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	st, err := env.LookupStruct("Point")
	if err != nil {
		t.Fatalf("LookupStruct: %v", err)
	}
	if st.Fields["x"].String() != "float64" {
		t.Errorf("field x: %s", st.Fields["x"])
	}
	if st.Fields["y"].String() != "float64" {
		t.Errorf("field y: %s", st.Fields["y"])
	}
}

func TestCollectAnnotatedLet(t *testing.T) {
	src := []byte(`package main
func main() {
	let x = 42
}`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	// The annotation should be stored for the emit pass to use
	// (full resolution tested in Task 8)
	_ = env
}

func TestCollectImport(t *testing.T) {
	src := []byte(`package main
import "fmt"
func main() {}`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, ok := env.imports["fmt"]; !ok {
		t.Error("import 'fmt' not collected")
	}
}

func TestCollectMultiImport(t *testing.T) {
	src := []byte(`package main
import (
	"fmt"
	"os"
	"net/http"
)
func main() {}`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, pkg := range []string{"fmt", "os", "http"} {
		if _, ok := env.imports[pkg]; !ok {
			t.Errorf("import %q not collected", pkg)
		}
	}
}

// --- Import resolution tests (Task 7) ---

func TestResolveStdlibImport(t *testing.T) {
	env := NewTypeEnv()
	err := env.LoadImports([]string{"fmt"}, "")
	if err != nil {
		t.Fatalf("LoadImports: %v", err)
	}
	// fmt.Println should be resolvable
	typ, err := env.LookupImportedFunc("fmt", "Println")
	if err != nil {
		t.Fatalf("LookupImportedFunc: %v", err)
	}
	// Println has variadic params and returns (int, error)
	if len(typ.Results) != 2 {
		t.Errorf("Println results: got %d, want 2", len(typ.Results))
	}
}

func TestResolveOsOpen(t *testing.T) {
	env := NewTypeEnv()
	err := env.LoadImports([]string{"os"}, "")
	if err != nil {
		t.Fatalf("LoadImports: %v", err)
	}
	typ, err := env.LookupImportedFunc("os", "Open")
	if err != nil {
		t.Fatalf("LookupImportedFunc: %v", err)
	}
	// os.Open returns (*os.File, error)
	if len(typ.Results) != 2 {
		t.Errorf("Open results: got %d, want 2", len(typ.Results))
	}
	if typ.Results[1].String() != "error" {
		t.Errorf("Open result[1]: got %s, want error", typ.Results[1])
	}
}

func TestResolveImportedType(t *testing.T) {
	env := NewTypeEnv()
	err := env.LoadImports([]string{"net/http"}, "")
	if err != nil {
		t.Fatalf("LoadImports: %v", err)
	}
	typ, err := env.LookupImportedType("http", "Request")
	if err != nil {
		t.Fatalf("LookupImportedType: %v", err)
	}
	if typ.String() != "http.Request" {
		t.Errorf("got %s", typ)
	}
}

func TestImportResolutionFailsGracefully(t *testing.T) {
	env := NewTypeEnv()
	err := env.LoadImports([]string{"nonexistent/package"}, "")
	// Should return error, not panic
	if err == nil {
		t.Error("expected error for nonexistent package")
	}
}

func TestCollectImplBlock(t *testing.T) {
	src := []byte(`package main
impl Point {
	func getX() float64 {
		return self.x
	}
}`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	fn, err := env.LookupFunc("Point.getX")
	if err != nil {
		t.Fatalf("LookupFunc: %v", err)
	}
	if len(fn.Results) != 1 || fn.Results[0].String() != "float64" {
		t.Errorf("result: %v", fn.Results)
	}
}
