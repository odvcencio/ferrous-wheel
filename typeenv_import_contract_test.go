package ferrouswheel

import (
	"strings"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

func TestImportedConstantsRetainGoConstantType(t *testing.T) {
	env := NewTypeEnv()
	if err := env.LoadImports([]string{"math", "time"}, ""); err != nil {
		t.Fatalf("load standard library imports: %v", err)
	}
	pi, err := env.LookupImportedVar("math", "Pi")
	if err != nil {
		t.Fatalf("lookup math.Pi: %v", err)
	}
	if !TypeEquals(pi, &UntypedConstType{Kind: UntypedFloat}) {
		t.Fatalf("math.Pi type = %v, want untyped float", pi)
	}
	second, err := env.LookupImportedVar("time", "Second")
	if err != nil {
		t.Fatalf("lookup time.Second: %v", err)
	}
	if got := second.String(); got != "time.Duration" {
		t.Fatalf("time.Second type = %s, want time.Duration", got)
	}
}

func TestImportAliasKeepsTypeOwnershipAndGoAliases(t *testing.T) {
	src := []byte(`package main
import files "os"
import h "net/http"
func inspect() {
    _, _ = files.Stat("item")
}`)
	env, err := collectTypes(src)
	if err != nil {
		t.Fatalf("collect imports: %v", err)
	}
	if err := env.LoadCollectedImports(""); err != nil {
		t.Fatalf("load imports: %v", err)
	}
	stat, err := env.LookupImportedFunc("files", "Stat")
	if err != nil {
		t.Fatalf("lookup aliased os.Stat: %v", err)
	}
	if got := stat.Results[0].String(); got != "files.FileInfo" {
		t.Fatalf("os.Stat result = %s, want files.FileInfo", got)
	}
	file, err := env.LookupImportedType("files", "File")
	if err != nil {
		t.Fatalf("lookup aliased os.File: %v", err)
	}
	if got := file.String(); got != "files.File" {
		t.Fatalf("os.File = %s, want files.File", got)
	}
	newRequest, err := env.LookupImportedFunc("h", "NewRequestWithContext")
	if err != nil {
		t.Fatalf("lookup aliased http.NewRequestWithContext: %v", err)
	}
	if got := newRequest.Params[0].String(); got != "context.Context" {
		t.Fatalf("foreign parameter = %s, want context.Context", got)
	}
	if got := newRequest.Params[3].String(); got != "io.Reader" {
		t.Fatalf("foreign parameter = %s, want io.Reader", got)
	}
	if got := newRequest.Results[0].String(); got != "*h.Request" {
		t.Fatalf("local result = %s, want *h.Request", got)
	}
}

func TestLocalBindingShadowsImportedPackageInSelector(t *testing.T) {
	source := []byte(`package main
import h "net/http"
type Holder struct { Request int }
func inspect(h Holder) {
    _ = h.Request
}`)
	lang, err := GetFWLanguage()
	if err != nil {
		t.Fatalf("language: %v", err)
	}
	tree, err := gotreesitter.NewParser(lang).Parse(source)
	if err != nil || tree.RootNode().HasError() {
		t.Fatalf("parse: %v", err)
	}
	env, err := CollectTypes(source)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if err := env.LoadCollectedImports(""); err != nil {
		t.Fatalf("load imports: %v", err)
	}
	start := strings.LastIndex(string(source), "h.Request")
	target := tree.RootNode().NamedDescendantForByteRange(uint32(start), uint32(start+len("h.Request")))
	if target == nil || target.Type(lang) != "selector_expression" {
		t.Fatalf("selector node = %v", target)
	}
	typ, err := env.ResolveAt(tree.RootNode(), target, lang, source)
	if err != nil || !TypeEquals(typ, Primitive("int")) {
		t.Fatalf("local h.Request type = %v, %v; want int", typ, err)
	}
}
