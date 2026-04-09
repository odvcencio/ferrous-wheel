package ferrouswheel

import (
	"sort"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// LintSeverity indicates how serious a lint diagnostic is.
type LintSeverity int

const (
	LintError   LintSeverity = iota // blocks transpilation
	LintWarning                     // reported, doesn't block
	LintInfo                        // informational
)

func (s LintSeverity) String() string {
	switch s {
	case LintError:
		return "error"
	case LintWarning:
		return "warning"
	case LintInfo:
		return "info"
	default:
		return "unknown"
	}
}

// LintDiagnostic is a single lint finding.
type LintDiagnostic struct {
	Rule     string
	Line     int
	Col      int
	Message  string
	Severity LintSeverity
}

// LintContext provides the shared state available to lint rules.
type LintContext struct {
	Lang    *gotreesitter.Language
	Src     []byte
	TypeEnv *TypeEnv
}

// NodeLintRule checks individual AST nodes.
type NodeLintRule interface {
	Name() string
	Description() string
	Severity() LintSeverity
	Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic
}

// ScopeLintRule performs whole-scope analysis (e.g., unused bindings).
type ScopeLintRule interface {
	Name() string
	Description() string
	Severity() LintSeverity
	CheckScope(scope *Scope, root *gotreesitter.Node, ctx *LintContext) []LintDiagnostic
}

// Global rule registries.
var (
	nodeRules  []NodeLintRule
	scopeRules []ScopeLintRule
)

func init() {
	nodeRules = []NodeLintRule{
		&emptyMatchRule{},
		&unreachableMatchArmRule{},
		&missingElseIfExprRule{},
		&emptyBlockRule{},
		&unreachableAfterGuardRule{},
		// Type-aware rules:
		&redundantTryRule{},
		&unnecessarySafeNavRule{},
		// Logging rules:
		&bareErrorRule{},
		&bareFatalRule{},
		&debugNoAttrsRule{},
		&timeEmptyBlockRule{},
		&withNoLogsRule{},
		&withSingleLogRule{},
		&nestedTimeSameNameRule{},
		&logInHotLoopRule{},
	}
	scopeRules = []ScopeLintRule{
		&unusedLetRule{},
		&unusedMutRule{},
		&shadowedLetRule{},
	}
}

// Lint runs all lint rules on the given source and returns diagnostics.
func Lint(source []byte) ([]LintDiagnostic, error) {
	lang, err := getFWLanguage()
	if err != nil {
		return nil, err
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(source)
	if err != nil {
		return nil, err
	}
	root := tree.RootNode()

	// Build a type environment for scope-aware rules.
	var env *TypeEnv
	if collected, collectErr := collectTypes(source); collectErr == nil {
		env = collected
	}

	ctx := &LintContext{
		Lang:    lang,
		Src:     source,
		TypeEnv: env,
	}

	var diags []LintDiagnostic

	// Walk the CST, running all NodeLintRules on each node.
	var walk func(n *gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		for _, rule := range nodeRules {
			diags = append(diags, rule.Check(n, ctx)...)
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)

	// Run all ScopeLintRules on the root scope.
	scope := buildLintScope(root, lang, source)
	for _, rule := range scopeRules {
		diags = append(diags, rule.CheckScope(scope, root, ctx)...)
	}

	sort.Slice(diags, func(i, j int) bool {
		if diags[i].Line != diags[j].Line {
			return diags[i].Line < diags[j].Line
		}
		return diags[i].Col < diags[j].Col
	})

	return diags, nil
}

// buildLintScope walks the CST and collects let/let mut bindings into a Scope.
func buildLintScope(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte) *Scope {
	scope := newScope(nil)

	text := func(n *gotreesitter.Node) string {
		return string(src[n.StartByte():n.EndByte()])
	}

	var walk func(n *gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		nodeType := n.Type(lang)
		switch nodeType {
		case "let_declaration":
			nameNode := n.ChildByFieldName("name", lang)
			mutNode := n.ChildByFieldName("mutable", lang)
			if nameNode != nil {
				name := text(nameNode)
				isMut := mutNode != nil
				scope.setWithMut(name, nil, isMut)
			}
		case "let_multi_declaration":
			mutNode := n.ChildByFieldName("mutable", lang)
			isMut := mutNode != nil
			for i := 0; i < int(n.NamedChildCount()); i++ {
				child := n.NamedChild(i)
				switch child.Type(lang) {
				case "identifier":
					scope.setWithMut(text(child), nil, isMut)
				case "let_typed_binding":
					nameNode := child.ChildByFieldName("name", lang)
					if nameNode != nil {
						scope.setWithMut(text(nameNode), nil, isMut)
					}
				}
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
	return scope
}

// ---------- empty-match rule ----------

type emptyMatchRule struct{}

func (r *emptyMatchRule) Name() string           { return "empty-match" }
func (r *emptyMatchRule) Description() string    { return "match expression with zero arms" }
func (r *emptyMatchRule) Severity() LintSeverity { return LintWarning }

func (r *emptyMatchRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	nodeType := n.Type(ctx.Lang)

	// Case 1: valid match_expression with zero arms (grammar usually
	// requires at least one arm, but check anyway).
	if nodeType == "match_expression" {
		armCount := 0
		for i := 0; i < int(n.NamedChildCount()); i++ {
			child := n.NamedChild(i)
			if child.Type(ctx.Lang) == "match_arm" {
				armCount++
			}
		}
		if armCount == 0 {
			pt := n.StartPoint()
			return []LintDiagnostic{{
				Rule:     r.Name(),
				Line:     int(pt.Row) + 1,
				Col:      int(pt.Column) + 1,
				Message:  "match expression has zero arms",
				Severity: r.Severity(),
			}}
		}
		return nil
	}

	// Case 2: error-recovery pattern. The grammar requires >= 1 arm, so
	// "match v {}" produces error nodes instead of a match_expression.
	// Detect the "match" keyword token among children and check whether
	// the next braced body is empty (no "=>" inside).
	if !n.HasError() {
		return nil
	}
	childCount := n.ChildCount()
	for i := 0; i < childCount; i++ {
		child := n.Child(i)
		if child == nil {
			continue
		}
		// Look for an unnamed "match" token.
		if child.Type(ctx.Lang) == "match" && !child.IsNamed() {
			// Scan forward siblings for the braced body.
			if diag := r.checkErrorMatch(child, n, i, ctx); diag != nil {
				return []LintDiagnostic{*diag}
			}
		}
	}
	return nil
}

// checkErrorMatch looks for an empty match body after a "match" token
// found inside an error-recovery tree.
func (r *emptyMatchRule) checkErrorMatch(matchTok *gotreesitter.Node, parent *gotreesitter.Node, idx int, ctx *LintContext) *LintDiagnostic {
	// After the "match" token we expect: subject expression, then "{...}".
	// In error recovery, "match v {}" often becomes:
	//   match (token) | composite_literal(v {}) or identifier + literal_value
	// We need to find the braces. Scan forward looking for a composite_literal
	// whose literal_value is empty, or for a literal_value or block with no
	// match_arm-like content.
	childCount := parent.ChildCount()
	for j := idx + 1; j < childCount; j++ {
		sibling := parent.Child(j)
		if sibling == nil {
			continue
		}
		sibType := sibling.Type(ctx.Lang)
		// composite_literal: v {}
		if sibType == "composite_literal" || sibType == "_expression" {
			if r.isEmptyMatchBody(sibling, ctx) {
				pt := matchTok.StartPoint()
				return &LintDiagnostic{
					Rule:     r.Name(),
					Line:     int(pt.Row) + 1,
					Col:      int(pt.Column) + 1,
					Message:  "match expression has zero arms",
					Severity: r.Severity(),
				}
			}
			return nil
		}
		// If we hit a block or literal_value, check directly
		if sibType == "literal_value" || sibType == "block" {
			if sibling.NamedChildCount() == 0 {
				pt := matchTok.StartPoint()
				return &LintDiagnostic{
					Rule:     r.Name(),
					Line:     int(pt.Row) + 1,
					Col:      int(pt.Column) + 1,
					Message:  "match expression has zero arms",
					Severity: r.Severity(),
				}
			}
			return nil
		}
	}
	return nil
}

// isEmptyMatchBody checks whether a node (typically composite_literal or
// _expression wrapper) contains an empty braced body that represents an
// empty match.
func (r *emptyMatchRule) isEmptyMatchBody(n *gotreesitter.Node, ctx *LintContext) bool {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child.Type(ctx.Lang) == "literal_value" && child.NamedChildCount() == 0 {
			return true
		}
	}
	// Recurse one level for _expression wrappers.
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if r.isEmptyMatchBody(child, ctx) {
			return true
		}
	}
	return false
}

// ---------- unused-let rule ----------

type unusedLetRule struct{}

func (r *unusedLetRule) Name() string           { return "unused-let" }
func (r *unusedLetRule) Description() string    { return "let binding never read" }
func (r *unusedLetRule) Severity() LintSeverity { return LintWarning }

func (r *unusedLetRule) CheckScope(scope *Scope, root *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	// Collect all let bindings (both let and let mut).
	var bindings []letBinding
	collectLetBindings(root, ctx.Lang, ctx.Src, &bindings)

	if len(bindings) == 0 {
		return nil
	}

	// Count reads for each binding name.
	reads := countIdentifierReads(root, ctx.Lang, ctx.Src)

	var diags []LintDiagnostic
	for _, b := range bindings {
		if reads[b.name] == 0 {
			diags = append(diags, LintDiagnostic{
				Rule:     r.Name(),
				Line:     b.line,
				Col:      b.col,
				Message:  "let binding '" + b.name + "' is never read",
				Severity: r.Severity(),
			})
		}
	}
	return diags
}

// ---------- unused-mut rule ----------

type unusedMutRule struct{}

func (r *unusedMutRule) Name() string           { return "unused-mut" }
func (r *unusedMutRule) Description() string    { return "let mut binding never reassigned" }
func (r *unusedMutRule) Severity() LintSeverity { return LintWarning }

func (r *unusedMutRule) CheckScope(scope *Scope, root *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	// Collect only let mut bindings.
	var bindings []letBinding
	collectMutBindings(root, ctx.Lang, ctx.Src, &bindings)

	if len(bindings) == 0 {
		return nil
	}

	// Count writes (reassignments) for each binding name.
	writes := countIdentifierWrites(root, ctx.Lang, ctx.Src)

	var diags []LintDiagnostic
	for _, b := range bindings {
		if writes[b.name] == 0 {
			diags = append(diags, LintDiagnostic{
				Rule:     r.Name(),
				Line:     b.line,
				Col:      b.col,
				Message:  "let mut binding '" + b.name + "' is never reassigned; consider using 'let' instead",
				Severity: r.Severity(),
			})
		}
	}
	return diags
}

// ---------- helpers ----------

type letBinding struct {
	name string
	line int
	col  int
}

// collectLetBindings finds all let/let mut binding names and positions.
func collectLetBindings(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, out *[]letBinding) {
	text := func(n *gotreesitter.Node) string {
		return string(src[n.StartByte():n.EndByte()])
	}
	var walk func(n *gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		nodeType := n.Type(lang)
		switch nodeType {
		case "let_declaration":
			nameNode := n.ChildByFieldName("name", lang)
			if nameNode != nil {
				pt := nameNode.StartPoint()
				*out = append(*out, letBinding{
					name: text(nameNode),
					line: int(pt.Row) + 1,
					col:  int(pt.Column) + 1,
				})
			}
		case "let_multi_declaration":
			for i := 0; i < int(n.NamedChildCount()); i++ {
				child := n.NamedChild(i)
				switch child.Type(lang) {
				case "identifier":
					pt := child.StartPoint()
					*out = append(*out, letBinding{
						name: text(child),
						line: int(pt.Row) + 1,
						col:  int(pt.Column) + 1,
					})
				case "let_typed_binding":
					nameNode := child.ChildByFieldName("name", lang)
					if nameNode != nil {
						pt := nameNode.StartPoint()
						*out = append(*out, letBinding{
							name: text(nameNode),
							line: int(pt.Row) + 1,
							col:  int(pt.Column) + 1,
						})
					}
				}
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
}

// collectMutBindings finds only let mut binding names and positions.
func collectMutBindings(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, out *[]letBinding) {
	text := func(n *gotreesitter.Node) string {
		return string(src[n.StartByte():n.EndByte()])
	}
	var walk func(n *gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		nodeType := n.Type(lang)
		switch nodeType {
		case "let_declaration":
			mutNode := n.ChildByFieldName("mutable", lang)
			if mutNode == nil {
				break
			}
			nameNode := n.ChildByFieldName("name", lang)
			if nameNode != nil {
				pt := nameNode.StartPoint()
				*out = append(*out, letBinding{
					name: text(nameNode),
					line: int(pt.Row) + 1,
					col:  int(pt.Column) + 1,
				})
			}
		case "let_multi_declaration":
			mutNode := n.ChildByFieldName("mutable", lang)
			if mutNode == nil {
				break
			}
			for i := 0; i < int(n.NamedChildCount()); i++ {
				child := n.NamedChild(i)
				switch child.Type(lang) {
				case "identifier":
					pt := child.StartPoint()
					*out = append(*out, letBinding{
						name: text(child),
						line: int(pt.Row) + 1,
						col:  int(pt.Column) + 1,
					})
				case "let_typed_binding":
					nameNode := child.ChildByFieldName("name", lang)
					if nameNode != nil {
						pt := nameNode.StartPoint()
						*out = append(*out, letBinding{
							name: text(nameNode),
							line: int(pt.Row) + 1,
							col:  int(pt.Column) + 1,
						})
					}
				}
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
}

// countIdentifierReads walks the CST and counts how many times each identifier
// appears in a read position (i.e., NOT on the LHS of an assignment and NOT
// as the name in a let declaration).
func countIdentifierReads(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte) map[string]int {
	reads := make(map[string]int)
	text := func(n *gotreesitter.Node) string {
		return string(src[n.StartByte():n.EndByte()])
	}

	// Collect all identifiers that appear on the LHS of assignments or as
	// let binding names -- these are NOT reads. We use the node pointer as key.
	writeLHS := make(map[*gotreesitter.Node]bool)
	var markWrites func(n *gotreesitter.Node)
	markWrites = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		nodeType := n.Type(lang)
		switch nodeType {
		case "let_declaration":
			nameNode := n.ChildByFieldName("name", lang)
			if nameNode != nil {
				writeLHS[nameNode] = true
			}
		case "let_multi_declaration":
			for i := 0; i < int(n.NamedChildCount()); i++ {
				child := n.NamedChild(i)
				switch child.Type(lang) {
				case "identifier":
					writeLHS[child] = true
				case "let_typed_binding":
					nameNode := child.ChildByFieldName("name", lang)
					if nameNode != nil {
						writeLHS[nameNode] = true
					}
				}
			}
		case "assignment_statement":
			left := n.ChildByFieldName("left", lang)
			if left != nil {
				markLHSIdents(left, lang, writeLHS)
			}
		case "short_var_declaration":
			left := n.ChildByFieldName("left", lang)
			if left != nil {
				markLHSIdents(left, lang, writeLHS)
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			markWrites(n.NamedChild(i))
		}
	}
	markWrites(root)

	// Now count reads: any identifier NOT in writeLHS.
	var countReads func(n *gotreesitter.Node)
	countReads = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "identifier" && !writeLHS[n] {
			reads[text(n)]++
		}
		// f-strings: identifiers inside {expr} are reads but not separate CST nodes.
		// Parse the raw token text to extract them.
		if n.Type(lang) == "fstring" {
			countFStringReads(text(n), reads)
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			countReads(n.NamedChild(i))
		}
	}
	countReads(root)

	return reads
}

// countFStringReads extracts identifiers from f-string interpolation expressions
// and counts them as reads. The raw token looks like: f"text {expr} more {expr2}"
func countFStringReads(raw string, reads map[string]int) {
	// Strip f" prefix and trailing "
	if len(raw) < 3 || raw[0] != 'f' || raw[1] != '"' {
		return
	}
	inner := raw[2 : len(raw)-1]
	depth := 0
	start := -1
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '{':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				expr := inner[start:i]
				// Extract identifiers from the expression.
				// Simple approach: split on non-identifier chars and count each token
				// that looks like a Go identifier.
				extractIdentReads(expr, reads)
				start = -1
			}
		}
	}
}

// extractIdentReads scans an expression string for identifier-like tokens and
// increments their read count. Handles dotted access (a.b.c counts "a"),
// function calls (foo() counts "foo"), and index expressions.
func extractIdentReads(expr string, reads map[string]int) {
	var ident []byte
	for i := 0; i <= len(expr); i++ {
		var ch byte
		if i < len(expr) {
			ch = expr[i]
		}
		isIdentChar := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || (ch >= '0' && ch <= '9' && len(ident) > 0)
		if isIdentChar {
			ident = append(ident, ch)
		} else {
			if len(ident) > 0 {
				name := string(ident)
				// Skip Go keywords and builtins
				if !isGoKeyword(name) {
					reads[name]++
				}
				ident = ident[:0]
			}
		}
	}
}

func isGoKeyword(s string) bool {
	switch s {
	case "break", "case", "chan", "const", "continue", "default", "defer",
		"else", "fallthrough", "for", "func", "go", "goto", "if", "import",
		"interface", "map", "package", "range", "return", "select", "struct",
		"switch", "type", "var", "true", "false", "nil", "len", "cap",
		"append", "make", "new", "string", "int", "bool", "error", "fmt":
		return true
	}
	return false
}

// countIdentifierWrites walks the CST and counts how many times each identifier
// appears on the LHS of an assignment (excluding the initial let declaration).
func countIdentifierWrites(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte) map[string]int {
	writes := make(map[string]int)
	text := func(n *gotreesitter.Node) string {
		return string(src[n.StartByte():n.EndByte()])
	}

	var walk func(n *gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		nodeType := n.Type(lang)
		if nodeType == "assignment_statement" {
			left := n.ChildByFieldName("left", lang)
			if left != nil {
				collectLHSNames(left, lang, src, writes, text)
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
	return writes
}

// markLHSIdents marks all identifier nodes within an expression list as writes.
func markLHSIdents(n *gotreesitter.Node, lang *gotreesitter.Language, ids map[*gotreesitter.Node]bool) {
	if n == nil {
		return
	}
	if n.Type(lang) == "identifier" {
		ids[n] = true
		return
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		markLHSIdents(n.NamedChild(i), lang, ids)
	}
}

// collectLHSNames adds identifiers from a LHS expression list to the writes map.
func collectLHSNames(n *gotreesitter.Node, lang *gotreesitter.Language, src []byte, writes map[string]int, text func(*gotreesitter.Node) string) {
	if n == nil {
		return
	}
	if n.Type(lang) == "identifier" {
		writes[text(n)]++
		return
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		collectLHSNames(n.NamedChild(i), lang, src, writes, text)
	}
}

// ---------- unreachable-match-arm rule ----------

type unreachableMatchArmRule struct{}

func (r *unreachableMatchArmRule) Name() string { return "unreachable-match-arm" }
func (r *unreachableMatchArmRule) Description() string {
	return "arm after wildcard/default pattern is unreachable"
}
func (r *unreachableMatchArmRule) Severity() LintSeverity { return LintWarning }

func (r *unreachableMatchArmRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "match_expression" {
		return nil
	}

	text := func(nd *gotreesitter.Node) string {
		return string(ctx.Src[nd.StartByte():nd.EndByte()])
	}

	var diags []LintDiagnostic
	wildcardSeen := false
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child.Type(ctx.Lang) != "match_arm" {
			continue
		}
		if wildcardSeen {
			pt := child.StartPoint()
			diags = append(diags, LintDiagnostic{
				Rule:     r.Name(),
				Line:     int(pt.Row) + 1,
				Col:      int(pt.Column) + 1,
				Message:  "unreachable match arm after wildcard pattern",
				Severity: r.Severity(),
			})
			continue
		}
		pattern := child.ChildByFieldName("pattern", ctx.Lang)
		if pattern != nil {
			ptext := text(pattern)
			if ptext == "_" {
				wildcardSeen = true
			}
		}
	}
	return diags
}

// ---------- missing-else-if-expr rule ----------

type missingElseIfExprRule struct{}

func (r *missingElseIfExprRule) Name() string           { return "missing-else-if-expr" }
func (r *missingElseIfExprRule) Description() string    { return "if-expression without else" }
func (r *missingElseIfExprRule) Severity() LintSeverity { return LintError }

func (r *missingElseIfExprRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "if_statement" {
		return nil
	}
	// Only applies to if in expression position
	if !lintIsExpressionPosition(n, ctx.Lang) {
		return nil
	}
	// Walk the chain: every terminal if must have an else
	if diag := r.checkChain(n, ctx); diag != nil {
		return []LintDiagnostic{*diag}
	}
	return nil
}

func (r *missingElseIfExprRule) checkChain(n *gotreesitter.Node, ctx *LintContext) *LintDiagnostic {
	alt := n.ChildByFieldName("alternative", ctx.Lang)
	if alt == nil {
		pt := n.StartPoint()
		return &LintDiagnostic{
			Rule:     r.Name(),
			Line:     int(pt.Row) + 1,
			Col:      int(pt.Column) + 1,
			Message:  "if-expression requires an else branch",
			Severity: r.Severity(),
		}
	}
	if alt.Type(ctx.Lang) == "if_statement" {
		return r.checkChain(alt, ctx)
	}
	return nil
}

// lintIsExpressionPosition checks whether an if_statement is in expression position
// (e.g. as the value of a let declaration, short_var_declaration, return statement, etc.)
func lintIsExpressionPosition(n *gotreesitter.Node, lang *gotreesitter.Language) bool {
	parent := n.Parent()
	if parent == nil {
		return false
	}
	parentType := parent.Type(lang)
	switch parentType {
	case "let_declaration", "let_multi_declaration":
		return parent.ChildByFieldName("value", lang) == n
	case "short_var_declaration":
		return true
	case "return_statement":
		return true
	case "call_expression":
		return true
	case "expression_list":
		gp := parent.Parent()
		if gp != nil {
			switch gp.Type(lang) {
			case "short_var_declaration", "assignment_statement", "return_statement":
				return true
			}
		}
	}
	return false
}

// ---------- redundant-try rule ----------

type redundantTryRule struct{}

func (r *redundantTryRule) Name() string { return "redundant-try" }
func (r *redundantTryRule) Description() string {
	return "try or ? on a function that doesn't return error"
}
func (r *redundantTryRule) Severity() LintSeverity { return LintWarning }

func (r *redundantTryRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if ctx.TypeEnv == nil {
		return nil
	}
	nodeType := n.Type(ctx.Lang)
	if nodeType != "error_propagation" && nodeType != "postfix_try" {
		return nil
	}
	// The operand is the first named child (the call expression)
	if n.NamedChildCount() == 0 {
		return nil
	}
	operand := n.NamedChild(0)
	if operand == nil {
		return nil
	}
	// Try to resolve the operand's type
	typ, err := ctx.TypeEnv.Resolve(operand, ctx.Lang, ctx.Src)
	if err != nil || typ == nil {
		return nil
	}
	// Check if the resolved type is a tuple/result that ends with error
	// If it's a FuncType, check its return types
	if ft, ok := typ.(*FuncType); ok {
		if len(ft.Results) > 0 {
			lastRet := ft.Results[len(ft.Results)-1]
			if lastRet.String() == "error" {
				return nil // valid try usage
			}
		}
		pt := n.StartPoint()
		return []LintDiagnostic{{
			Rule:     r.Name(),
			Line:     int(pt.Row) + 1,
			Col:      int(pt.Column) + 1,
			Message:  "try/? on a function that doesn't return error",
			Severity: r.Severity(),
		}}
	}
	return nil
}

// ---------- shadowed-let rule ----------

type shadowedLetRule struct{}

func (r *shadowedLetRule) Name() string { return "shadowed-let" }
func (r *shadowedLetRule) Description() string {
	return "let binding shadows same name in parent scope"
}
func (r *shadowedLetRule) Severity() LintSeverity { return LintWarning }

func (r *shadowedLetRule) CheckScope(_ *Scope, root *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	var diags []LintDiagnostic
	type scopeFrame struct {
		names map[string]bool
	}
	stack := []scopeFrame{{names: make(map[string]bool)}}

	text := func(nd *gotreesitter.Node) string {
		return string(ctx.Src[nd.StartByte():nd.EndByte()])
	}

	var walk func(n *gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		nodeType := n.Type(ctx.Lang)

		// Push scope on block entry
		isBlock := nodeType == "block" || nodeType == "function_declaration" || nodeType == "func_literal"
		if isBlock {
			stack = append(stack, scopeFrame{names: make(map[string]bool)})
		}

		// Check let declarations
		if nodeType == "let_declaration" {
			nameNode := n.ChildByFieldName("name", ctx.Lang)
			if nameNode != nil {
				name := text(nameNode)
				// Check if name exists in any parent scope (not the current one)
				if len(stack) >= 2 {
					for i := len(stack) - 2; i >= 0; i-- {
						if stack[i].names[name] {
							pt := nameNode.StartPoint()
							diags = append(diags, LintDiagnostic{
								Rule:     r.Name(),
								Line:     int(pt.Row) + 1,
								Col:      int(pt.Column) + 1,
								Message:  "let binding '" + name + "' shadows binding in outer scope",
								Severity: r.Severity(),
							})
							break
						}
					}
				}
				stack[len(stack)-1].names[name] = true
			}
		}

		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}

		if isBlock {
			stack = stack[:len(stack)-1]
		}
	}
	walk(root)
	return diags
}

// ---------- empty-block rule ----------

type emptyBlockRule struct{}

func (r *emptyBlockRule) Name() string           { return "empty-block" }
func (r *emptyBlockRule) Description() string    { return "empty {} body (likely a mistake)" }
func (r *emptyBlockRule) Severity() LintSeverity { return LintInfo }

func (r *emptyBlockRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "block" {
		return nil
	}
	// Only flag if parent is if_statement, for_statement, for_in_statement, or match_arm
	parent := n.Parent()
	if parent == nil {
		return nil
	}
	parentType := parent.Type(ctx.Lang)
	switch parentType {
	case "if_statement", "for_statement", "for_in_statement", "for_in_index_statement", "match_arm":
		// ok
	default:
		return nil
	}
	// Check if block is truly empty (no named children)
	if n.NamedChildCount() > 0 {
		return nil
	}
	pt := n.StartPoint()
	return []LintDiagnostic{{
		Rule:     r.Name(),
		Line:     int(pt.Row) + 1,
		Col:      int(pt.Column) + 1,
		Message:  "empty block body in " + parentType,
		Severity: r.Severity(),
	}}
}

// ---------- unnecessary-safe-nav rule ----------

type unnecessarySafeNavRule struct{}

func (r *unnecessarySafeNavRule) Name() string           { return "unnecessary-safe-nav" }
func (r *unnecessarySafeNavRule) Description() string    { return "?. on a non-pointer type" }
func (r *unnecessarySafeNavRule) Severity() LintSeverity { return LintWarning }

func (r *unnecessarySafeNavRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if ctx.TypeEnv == nil {
		return nil
	}
	if n.Type(ctx.Lang) != "safe_navigation" {
		return nil
	}
	obj := n.ChildByFieldName("object", ctx.Lang)
	if obj == nil {
		return nil
	}
	objType, err := ctx.TypeEnv.Resolve(obj, ctx.Lang, ctx.Src)
	if err != nil || objType == nil {
		return nil
	}
	if _, isPtr := objType.(*PointerType); isPtr {
		return nil // pointer, safe nav is needed
	}
	pt := n.StartPoint()
	return []LintDiagnostic{{
		Rule:     r.Name(),
		Line:     int(pt.Row) + 1,
		Col:      int(pt.Column) + 1,
		Message:  "unnecessary ?. on non-pointer type " + objType.String(),
		Severity: r.Severity(),
	}}
}

// ---------- unreachable-after-guard rule ----------

type unreachableAfterGuardRule struct{}

func (r *unreachableAfterGuardRule) Name() string           { return "unreachable-after-guard" }
func (r *unreachableAfterGuardRule) Description() string    { return "code after guard ... else return" }
func (r *unreachableAfterGuardRule) Severity() LintSeverity { return LintWarning }

func (r *unreachableAfterGuardRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	// This rule is intentionally conservative. A guard statement's else branch
	// executes when the condition is false. If the condition can be true, code
	// after the guard IS reachable. We only flag code after a guard whose
	// condition is the literal `false`.
	if n.Type(ctx.Lang) != "guard_statement" {
		return nil
	}
	cond := n.ChildByFieldName("condition", ctx.Lang)
	if cond == nil {
		return nil
	}
	condText := string(ctx.Src[cond.StartByte():cond.EndByte()])
	if condText != "false" {
		return nil // guard might pass, code after is reachable
	}
	// Check if the else body always exits (has return/panic)
	body := n.ChildByFieldName("body", ctx.Lang)
	if body == nil {
		return nil
	}
	if !containsExitNode(body, ctx.Lang) {
		return nil
	}
	// Find statements after this guard in the parent block
	parent := n.Parent()
	if parent == nil {
		return nil
	}
	// The guard is inside a statement_list or directly in a block
	foundGuard := false
	var diags []LintDiagnostic
	for i := 0; i < int(parent.NamedChildCount()); i++ {
		child := parent.NamedChild(i)
		if child == n {
			foundGuard = true
			continue
		}
		if foundGuard {
			pt := child.StartPoint()
			diags = append(diags, LintDiagnostic{
				Rule:     r.Name(),
				Line:     int(pt.Row) + 1,
				Col:      int(pt.Column) + 1,
				Message:  "unreachable code after guard with false condition",
				Severity: r.Severity(),
			})
			break // only flag the first unreachable statement
		}
	}
	return diags
}

// containsExitNode checks whether a node subtree contains a return_statement or
// a call to panic.
func containsExitNode(n *gotreesitter.Node, lang *gotreesitter.Language) bool {
	if n == nil {
		return false
	}
	nodeType := n.Type(lang)
	if nodeType == "return_statement" {
		return true
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if containsExitNode(n.Child(i), lang) {
			return true
		}
	}
	return false
}

// ---------- logging lint helpers ----------

// logStatementLevel returns the level keyword text for a log_statement node.
// The level is the first (anonymous) child token.
func logStatementLevel(n *gotreesitter.Node, ctx *LintContext) string {
	if n.ChildCount() == 0 {
		return ""
	}
	first := n.Child(0)
	if first == nil {
		return ""
	}
	return string(ctx.Src[first.StartByte():first.EndByte()])
}

// countLogAttrs counts the number of log_attr named children of n.
func countLogAttrs(n *gotreesitter.Node, lang *gotreesitter.Language) int {
	count := 0
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if n.NamedChild(i).Type(lang) == "log_attr" {
			count++
		}
	}
	return count
}

// countLogDescendants counts log_statement, log_time_block, and log_with_block
// descendants inside a subtree (not counting the root node itself).
func countLogDescendants(n *gotreesitter.Node, lang *gotreesitter.Language) int {
	count := 0
	var walk func(nd *gotreesitter.Node)
	walk = func(nd *gotreesitter.Node) {
		if nd == nil {
			return
		}
		for i := 0; i < int(nd.ChildCount()); i++ {
			child := nd.Child(i)
			if child == nil {
				continue
			}
			switch child.Type(lang) {
			case "log_statement", "log_time_block", "log_with_block":
				count++
			}
			walk(child)
		}
	}
	walk(n)
	return count
}

// ---------- bare-error rule ----------

type bareErrorRule struct{}

func (r *bareErrorRule) Name() string           { return "bare-error" }
func (r *bareErrorRule) Description() string    { return "error log with no context attributes" }
func (r *bareErrorRule) Severity() LintSeverity { return LintWarning }

func (r *bareErrorRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "log_statement" {
		return nil
	}
	if logStatementLevel(n, ctx) != "error" {
		return nil
	}
	if countLogAttrs(n, ctx.Lang) > 0 {
		return nil
	}
	pt := n.StartPoint()
	return []LintDiagnostic{{
		Rule:     r.Name(),
		Line:     int(pt.Row) + 1,
		Col:      int(pt.Column) + 1,
		Message:  "error log with no context attributes",
		Severity: r.Severity(),
	}}
}

// ---------- bare-fatal rule ----------

type bareFatalRule struct{}

func (r *bareFatalRule) Name() string           { return "bare-fatal" }
func (r *bareFatalRule) Description() string    { return "fatal log with no context attributes" }
func (r *bareFatalRule) Severity() LintSeverity { return LintWarning }

func (r *bareFatalRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "log_statement" {
		return nil
	}
	if logStatementLevel(n, ctx) != "fatal" {
		return nil
	}
	if countLogAttrs(n, ctx.Lang) > 0 {
		return nil
	}
	pt := n.StartPoint()
	return []LintDiagnostic{{
		Rule:     r.Name(),
		Line:     int(pt.Row) + 1,
		Col:      int(pt.Column) + 1,
		Message:  "fatal log with no context attributes",
		Severity: r.Severity(),
	}}
}

// ---------- debug-no-attrs rule ----------

type debugNoAttrsRule struct{}

func (r *debugNoAttrsRule) Name() string           { return "debug-no-attrs" }
func (r *debugNoAttrsRule) Description() string    { return "debug/trace log with no attributes" }
func (r *debugNoAttrsRule) Severity() LintSeverity { return LintInfo }

func (r *debugNoAttrsRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "log_statement" {
		return nil
	}
	level := logStatementLevel(n, ctx)
	if level != "debug" && level != "trace" {
		return nil
	}
	if countLogAttrs(n, ctx.Lang) > 0 {
		return nil
	}
	pt := n.StartPoint()
	return []LintDiagnostic{{
		Rule:     r.Name(),
		Line:     int(pt.Row) + 1,
		Col:      int(pt.Column) + 1,
		Message:  "debug/trace log with no attributes",
		Severity: r.Severity(),
	}}
}

// ---------- time-empty-block rule ----------

type timeEmptyBlockRule struct{}

func (r *timeEmptyBlockRule) Name() string           { return "time-empty-block" }
func (r *timeEmptyBlockRule) Description() string    { return "time block with empty body" }
func (r *timeEmptyBlockRule) Severity() LintSeverity { return LintWarning }

func (r *timeEmptyBlockRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "log_time_block" {
		return nil
	}
	// Find the block child
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child.Type(ctx.Lang) == "block" {
			if child.NamedChildCount() == 0 {
				pt := n.StartPoint()
				return []LintDiagnostic{{
					Rule:     r.Name(),
					Line:     int(pt.Row) + 1,
					Col:      int(pt.Column) + 1,
					Message:  "time block with empty body",
					Severity: r.Severity(),
				}}
			}
			return nil
		}
	}
	return nil
}

// ---------- with-no-logs rule ----------

type withNoLogsRule struct{}

func (r *withNoLogsRule) Name() string           { return "with-no-logs" }
func (r *withNoLogsRule) Description() string    { return "with block contains no log calls" }
func (r *withNoLogsRule) Severity() LintSeverity { return LintWarning }

func (r *withNoLogsRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "log_with_block" {
		return nil
	}
	// Find the block child and count log descendants
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child.Type(ctx.Lang) == "block" {
			if countLogDescendants(child, ctx.Lang) == 0 {
				pt := n.StartPoint()
				return []LintDiagnostic{{
					Rule:     r.Name(),
					Line:     int(pt.Row) + 1,
					Col:      int(pt.Column) + 1,
					Message:  "with block contains no log calls",
					Severity: r.Severity(),
				}}
			}
			return nil
		}
	}
	return nil
}

// ---------- with-single-log rule ----------

type withSingleLogRule struct{}

func (r *withSingleLogRule) Name() string           { return "with-single-log" }
func (r *withSingleLogRule) Description() string    { return "with block wraps a single log call" }
func (r *withSingleLogRule) Severity() LintSeverity { return LintInfo }

func (r *withSingleLogRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "log_with_block" {
		return nil
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child.Type(ctx.Lang) == "block" {
			if countLogDescendants(child, ctx.Lang) == 1 {
				pt := n.StartPoint()
				return []LintDiagnostic{{
					Rule:     r.Name(),
					Line:     int(pt.Row) + 1,
					Col:      int(pt.Column) + 1,
					Message:  "with block wraps a single log call — consider inlining attributes",
					Severity: r.Severity(),
				}}
			}
			return nil
		}
	}
	return nil
}

// ---------- nested-time-same-name rule ----------

type nestedTimeSameNameRule struct{}

func (r *nestedTimeSameNameRule) Name() string { return "nested-time-same-name" }
func (r *nestedTimeSameNameRule) Description() string {
	return "nested time blocks with identical name"
}
func (r *nestedTimeSameNameRule) Severity() LintSeverity { return LintWarning }

func (r *nestedTimeSameNameRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "log_time_block" {
		return nil
	}
	// Get this node's name field text
	nameNode := n.ChildByFieldName("name", ctx.Lang)
	if nameNode == nil {
		return nil
	}
	myName := string(ctx.Src[nameNode.StartByte():nameNode.EndByte()])

	// Walk up parent chain looking for another log_time_block with the same name
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Type(ctx.Lang) == "log_time_block" {
			pName := p.ChildByFieldName("name", ctx.Lang)
			if pName != nil {
				parentName := string(ctx.Src[pName.StartByte():pName.EndByte()])
				if parentName == myName {
					pt := n.StartPoint()
					return []LintDiagnostic{{
						Rule:     r.Name(),
						Line:     int(pt.Row) + 1,
						Col:      int(pt.Column) + 1,
						Message:  "nested time blocks with identical name",
						Severity: r.Severity(),
					}}
				}
			}
		}
	}
	return nil
}

// ---------- log-in-hot-loop rule ----------

type logInHotLoopRule struct{}

func (r *logInHotLoopRule) Name() string           { return "log-in-hot-loop" }
func (r *logInHotLoopRule) Description() string    { return "log call inside loop" }
func (r *logInHotLoopRule) Severity() LintSeverity { return LintInfo }

func (r *logInHotLoopRule) Check(n *gotreesitter.Node, ctx *LintContext) []LintDiagnostic {
	if n.Type(ctx.Lang) != "log_statement" {
		return nil
	}
	// Walk parent chain looking for loop constructs
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(ctx.Lang) {
		case "repeat_statement", "for_statement", "for_in_statement", "for_in_index_statement":
			pt := n.StartPoint()
			return []LintDiagnostic{{
				Rule:     r.Name(),
				Line:     int(pt.Row) + 1,
				Col:      int(pt.Column) + 1,
				Message:  "log call inside loop may produce excessive output",
				Severity: r.Severity(),
			}}
		}
	}
	return nil
}
