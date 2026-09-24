package ferrouswheel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectTypesPreservesCompositeFunctionSignature(t *testing.T) {
	source := []byte(`package main
func transform(values map[string][]int, send chan<- int) (<-chan string, error) {
    return nil, nil
}`)
	env, err := CollectTypes(source)
	if err != nil {
		t.Fatalf("collect types: %v", err)
	}
	fn, err := env.LookupFunc("transform")
	if err != nil {
		t.Fatalf("lookup transform: %v", err)
	}
	if len(fn.Params) != 2 || len(fn.Results) != 2 {
		t.Fatalf("signature arity = %s, want two parameters and two results", fn)
	}
	for _, tc := range []struct {
		name string
		typ  Type
		want string
	}{
		{"map parameter", fn.Params[0], "map[string][]int"},
		{"send channel", fn.Params[1], "chan<- int"},
		{"receive channel", fn.Results[0], "<-chan string"},
		{"error result", fn.Results[1], "error"},
	} {
		if tc.typ.String() != tc.want {
			t.Fatalf("%s = %s, want %s", tc.name, tc.typ, tc.want)
		}
	}
}

func TestCollectedFileSymbolsAreFilteredAndSnapshotIsIndependent(t *testing.T) {
	env, err := CollectTypesMulti(map[string][]byte{
		"alpha.fw": []byte("package main\nfunc Alpha() {}\n"),
		"beta.fw":  []byte("package main\nfunc Beta() {}\n"),
	})
	if err != nil {
		t.Fatalf("collect types: %v", err)
	}
	alpha := env.FindFileSymbols("alpha.fw")
	if len(alpha) != 1 || alpha[0].Name != "Alpha" {
		t.Fatalf("alpha symbols = %+v", alpha)
	}
	if got := env.FindFileSymbols("missing.fw"); len(got) != 0 {
		t.Fatalf("missing file symbols = %+v", got)
	}
	snapshot := env.Symbols()
	snapshot[0].Name = "changed"
	if sym, ok := env.FindSymbol("Alpha"); !ok || sym.Name != "Alpha" {
		t.Fatalf("symbol registry changed through snapshot: %+v, %t", sym, ok)
	}
}

func TestTypeEnvCloneKeepsLexicalBindingsIndependent(t *testing.T) {
	env := NewTypeEnv()
	env.RegisterVar("shared", Primitive("int"))
	clone := env.Clone()
	clone.RegisterVar("shared", Primitive("string"))
	clone.RegisterVar("new", Primitive("bool"))

	original, err := env.LookupVar("shared")
	if err != nil || original.String() != "int" {
		t.Fatalf("original shared binding = %v, %v", original, err)
	}
	if _, err := env.LookupVar("new"); err == nil {
		t.Fatal("clone-only binding reached original environment")
	}
	changed, err := clone.LookupVar("shared")
	if err != nil || changed.String() != "string" {
		t.Fatalf("clone shared binding = %v, %v", changed, err)
	}
}

func TestFindModuleDirWalksFromSourcePath(t *testing.T) {
	moduleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module example.test/tool\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	path := filepath.Join(moduleDir, "scripts", "nested", "task.fw")
	if got := FindModuleDir(path); got != moduleDir {
		t.Fatalf("module directory = %q, want %q", got, moduleDir)
	}
}
