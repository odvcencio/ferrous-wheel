package ferrouswheel

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestEmitGrammarGoIncludesRulesAndMetadata(t *testing.T) {
	g := NewGrammar("mini")
	g.Define("source_file", Seq(
		Field("item", Choice(
			Sym("identifier"),
			Alias(Token(Pat(`[a-z]+`)), "word", true),
			Prec(1, Sym("identifier")),
			PrecLeft(2, Sym("identifier")),
			PrecRight(3, Sym("identifier")),
			PrecDynamic(4, Sym("identifier")),
			Optional(Sym("identifier")),
			Repeat(Sym("identifier")),
			Repeat1(Sym("identifier")),
			ImmToken(Str("!")),
			Blank(),
		)),
	))
	g.Define("identifier", Token(Pat(`[a-z_][a-z0-9_]*`)))
	g.SetExtras(Pat(`\s+`))
	g.SetConflicts([]string{"source_file", "identifier"})
	g.SetExternals(Sym("_newline"))
	g.SetInline("identifier")
	g.SetWord("identifier")
	g.SetSupertypes("_expression")
	g.EnableLRSplitting = true
	g.BinaryRepeatMode = true

	src, err := EmitGrammarGo(g, "ferrouswheel", "MiniGrammar")
	if err != nil {
		t.Fatalf("EmitGrammarGo: %v", err)
	}

	text := string(src)
	checks := []string{
		"func MiniGrammar() *Grammar",
		`g := NewGrammar("mini")`,
		`g.Define("source_file"`,
		`Field("item"`,
		`Alias(`,
		`Token(`,
		`ImmToken(`,
		`Optional(`,
		`Repeat(`,
		`Repeat1(`,
		`Prec(`,
		`PrecLeft(`,
		`PrecRight(`,
		`PrecDynamic(`,
		`Blank()`,
		`g.SetExtras(`,
		`g.SetConflicts(`,
		`g.SetExternals(`,
		`g.SetInline("identifier")`,
		`g.SetWord("identifier")`,
		`g.SetSupertypes("_expression")`,
		`g.EnableLRSplitting = true`,
		`g.BinaryRepeatMode = true`,
	}
	for _, check := range checks {
		if !strings.Contains(text, check) {
			t.Fatalf("expected generated source to contain %q\n%s", check, text)
		}
	}

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "generated.go", src, parser.AllErrors); err != nil {
		t.Fatalf("generated source should parse: %v\n%s", err, src)
	}
}

func TestEmitGoPatternQuoting(t *testing.T) {
	if got := emitGoPattern(`[a-z]+`); got != "`[a-z]+`" {
		t.Fatalf("expected raw string, got %q", got)
	}
	if got := emitGoPattern("contains`tick"); !strings.HasPrefix(got, "\"") {
		t.Fatalf("expected quoted string for backtick pattern, got %q", got)
	}
	if got := emitGoPattern("line1\nline2"); !strings.HasPrefix(got, "\"") {
		t.Fatalf("expected quoted string for multiline pattern, got %q", got)
	}
}
