package ferrouswheel

import (
	"sort"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// LintSeverity indicates how serious a lint diagnostic is.
type LintSeverity int

const (
	LintError   LintSeverity = iota // blocks transpilation
	LintWarning                      // reported, doesn't block
	LintInfo                         // informational
)

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
	}
	scopeRules = []ScopeLintRule{
		&unusedLetRule{},
		&unusedMutRule{},
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

func (r *emptyMatchRule) Name() string        { return "empty-match" }
func (r *emptyMatchRule) Description() string  { return "match expression with zero arms" }
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

func (r *unusedLetRule) Name() string        { return "unused-let" }
func (r *unusedLetRule) Description() string  { return "let binding never read" }
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

func (r *unusedMutRule) Name() string        { return "unused-mut" }
func (r *unusedMutRule) Description() string  { return "let mut binding never reassigned" }
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
		for i := 0; i < int(n.NamedChildCount()); i++ {
			countReads(n.NamedChild(i))
		}
	}
	countReads(root)

	return reads
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
