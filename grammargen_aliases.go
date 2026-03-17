package ferrouswheel

// Re-export grammargen types and DSL functions for use in grammar definitions.
// This avoids dot-imports while keeping grammar code readable.
//
// Note: the Grammar type is aliased as GrammarType to avoid conflict with
// the Grammar() function that returns the ferrous-wheel grammar definition.

import (
	"github.com/odvcencio/gotreesitter/grammargen"
)

// Type aliases
type GrammarType = grammargen.Grammar
type Rule = grammargen.Rule

// Constructor aliases
var (
	NewGrammar    = grammargen.NewGrammar
	ExtendGrammar = grammargen.ExtendGrammar
)

// DSL function aliases
var (
	Str         = grammargen.Str
	Pat         = grammargen.Pat
	Sym         = grammargen.Sym
	Seq         = grammargen.Seq
	Choice      = grammargen.Choice
	Repeat      = grammargen.Repeat
	Repeat1     = grammargen.Repeat1
	Optional    = grammargen.Optional
	Token       = grammargen.Token
	ImmToken    = grammargen.ImmToken
	Field       = grammargen.Field
	Prec        = grammargen.Prec
	PrecLeft    = grammargen.PrecLeft
	PrecRight   = grammargen.PrecRight
	PrecDynamic = grammargen.PrecDynamic
	Alias       = grammargen.Alias
	Blank       = grammargen.Blank
	CommaSep    = grammargen.CommaSep
	CommaSep1   = grammargen.CommaSep1
)

// Helper function aliases
var (
	AppendChoice     = grammargen.AppendChoice
	AddConflict      = grammargen.AddConflict
	GenerateLanguage = grammargen.GenerateLanguage
)

// Rule kind constants
var (
	RuleBlank       = grammargen.RuleBlank
	RuleString      = grammargen.RuleString
	RulePattern     = grammargen.RulePattern
	RuleSymbol      = grammargen.RuleSymbol
	RuleSeq         = grammargen.RuleSeq
	RuleChoice      = grammargen.RuleChoice
	RuleRepeat      = grammargen.RuleRepeat
	RuleRepeat1     = grammargen.RuleRepeat1
	RuleOptional    = grammargen.RuleOptional
	RuleToken       = grammargen.RuleToken
	RuleImmToken    = grammargen.RuleImmToken
	RuleField       = grammargen.RuleField
	RulePrec        = grammargen.RulePrec
	RulePrecLeft    = grammargen.RulePrecLeft
	RulePrecRight   = grammargen.RulePrecRight
	RulePrecDynamic = grammargen.RulePrecDynamic
	RuleAlias       = grammargen.RuleAlias
)

// GenerateHighlightQueries re-exports the highlight query generator.
var GenerateHighlightQueries = grammargen.GenerateHighlightQueries
