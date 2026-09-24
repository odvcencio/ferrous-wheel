package ferrouswheel

import (
	"bytes"
	"os"
	"strings"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammargen"
)

func TestGeneratedGrammarKeepsBinaryConditionInsideTernary(t *testing.T) {
	lang, err := GenerateLanguage(Grammar())
	if err != nil {
		t.Fatalf("generate grammar: %v", err)
	}
	src := []byte("package main\nfunc f(value int) { let result = value > 0 ? value : 1 }\n")
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	if err != nil {
		t.Fatalf("parse ternary: %v", err)
	}
	if tree == nil || tree.RootNode() == nil || tree.RootNode().HasError() {
		t.Fatal("ternary source produced an invalid syntax tree")
	}
	var ternary *gotreesitter.Node
	var visit func(*gotreesitter.Node)
	visit = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		if node.Type(lang) == "ternary_expression" {
			ternary = node
		}
		for i := 0; i < node.ChildCount(); i++ {
			visit(node.Child(i))
		}
	}
	visit(tree.RootNode())
	if ternary == nil || ternary.Text(src) != "value > 0 ? value : 1" {
		t.Fatalf("ternary must enclose the comparison: %s", tree.RootNode().SExpr(lang))
	}
	condition := ternary.ChildByFieldName("condition", lang)
	if condition == nil || condition.Type(lang) != "binary_expression" || condition.Text(src) != "value > 0" {
		t.Fatalf("ternary condition must be value > 0: %s", tree.RootNode().SExpr(lang))
	}
}

func TestTrackedGrammarBlobRecognizesCurrentLoggingSyntax(t *testing.T) {
	blob, err := os.ReadFile("grammar.bin")
	if err != nil {
		t.Fatalf("read grammar.bin: %v", err)
	}
	lang, err := gotreesitter.LoadLanguage(blob)
	if err != nil {
		t.Fatalf("load grammar.bin: %v", err)
	}
	src := []byte("package main\nfunc f() { debug \"checking\" }\n")
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	if err != nil {
		t.Fatalf("parse current logging syntax: %v", err)
	}
	if tree == nil || tree.RootNode() == nil || tree.RootNode().HasError() || !strings.Contains(tree.RootNode().SExpr(lang), "log_statement") {
		t.Fatal("grammar.bin does not parse current logging syntax")
	}
}

func TestEmbeddedGrammarMatchesCurrentSource(t *testing.T) {
	generated, err := grammargen.Generate(Grammar())
	if err != nil {
		t.Fatalf("generate grammar: %v", err)
	}
	if !bytes.Equal(generated, fwGrammarBlob) {
		t.Fatal("embedded grammar differs from Grammar(); run go generate .")
	}
	lang, err := GetFWLanguage()
	if err != nil {
		t.Fatalf("load embedded grammar: %v", err)
	}
	if lang.Name != "ferrous_wheel" || !lang.CompatibleWithRuntime() {
		t.Fatalf("embedded grammar has unexpected identity: name %q, ABI %d", lang.Name, lang.Version())
	}
}

func TestEmbeddedGrammarRejectsCorruptData(t *testing.T) {
	corrupt := append([]byte(nil), fwGrammarBlob...)
	corrupt[len(corrupt)/2] ^= 1
	_, err := loadVerifiedFWLanguage(corrupt)
	if err == nil || !strings.Contains(err.Error(), "embedded grammar checksum mismatch") {
		t.Fatalf("corrupt grammar error = %v", err)
	}
}
