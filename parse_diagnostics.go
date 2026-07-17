package ferrouswheel

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// maxReportedParseErrors caps how many individual parse-error sites get
// printed in a single diagnostic. Real ERROR/MISSING recovery trees can be
// large (a single missing token near the top of a file can cascade into
// dozens of nested ERROR nodes); we only want to show the handful that are
// actually useful to a human.
const maxReportedParseErrors = 5

// parseErrorLocation is a single diagnosed parse-error site.
type parseErrorLocation struct {
	Line    int // 1-indexed
	Col     int // 1-indexed
	Message string
	Excerpt string // source line + caret, already formatted for printing
}

func (p parseErrorLocation) format(sourceFile string) string {
	loc := fmt.Sprintf("%d:%d", p.Line, p.Col)
	if sourceFile != "" {
		loc = sourceFile + ":" + loc
	}
	if p.Excerpt == "" {
		return fmt.Sprintf("%s: %s", loc, p.Message)
	}
	return fmt.Sprintf("%s: %s\n%s", loc, p.Message, p.Excerpt)
}

// newParseErrorLocation builds a diagnosed location from a tree-sitter
// Point (0-indexed row/column, per gotreesitter convention) and message.
func newParseErrorLocation(src []byte, pt gotreesitter.Point, message string) parseErrorLocation {
	line := int(pt.Row) + 1
	col := int(pt.Column) + 1
	return parseErrorLocation{
		Line:    line,
		Col:     col,
		Message: message,
		Excerpt: sourceExcerpt(src, int(pt.Row), int(pt.Column)),
	}
}

// sourceExcerpt renders the source line containing (row, col) — both
// 0-indexed — with a caret pointing at the column, indented for display
// under a "file:line:col: message" diagnostic header.
func sourceExcerpt(src []byte, row, col int) string {
	lines := bytes.Split(src, []byte("\n"))
	if row < 0 || row >= len(lines) {
		return ""
	}
	line := strings.TrimRight(string(lines[row]), "\r")
	const maxExcerptLen = 120
	displayLine := line
	displayCol := col
	if len(displayLine) > maxExcerptLen {
		displayLine = displayLine[:maxExcerptLen] + " ..."
		if displayCol > maxExcerptLen {
			displayCol = maxExcerptLen
		}
	}
	if displayCol < 0 {
		displayCol = 0
	}
	if displayCol > len(displayLine) {
		displayCol = len(displayLine)
	}
	caret := strings.Repeat(" ", displayCol) + "^"
	return "    " + displayLine + "\n    " + caret
}

// byteOffsetToPoint converts a byte offset into a source buffer into a
// 0-indexed (row, column) tree-sitter Point by counting newlines. Used for
// diagnostics about spans that aren't backed by a specific CST node (e.g.
// "unparsed text" gaps between sibling nodes).
func byteOffsetToPoint(src []byte, offset uint32) gotreesitter.Point {
	if int(offset) > len(src) {
		offset = uint32(len(src))
	}
	head := src[:offset]
	row := uint32(bytes.Count(head, []byte("\n")))
	col := offset
	if lastNL := bytes.LastIndexByte(head, '\n'); lastNL >= 0 {
		col = offset - uint32(lastNL+1)
	}
	return gotreesitter.Point{Row: row, Column: col}
}

// collectParseErrorLocations walks the CST looking for ERROR and MISSING
// nodes (tree-sitter's error-recovery markers) and turns them into precise,
// human-readable diagnostics instead of a single generic "parse errors"
// message. It stops once maxReportedParseErrors locations have been found.
func collectParseErrorLocations(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte) []parseErrorLocation {
	var out []parseErrorLocation
	type span struct{ start, end uint32 }
	seen := make(map[span]bool)

	// walk reports whether it (or a descendant) already added a location
	// for n's subtree. ERROR nodes prefer to descend first: if a nested
	// ERROR/MISSING node pins down a more precise site, we report that
	// instead of also reporting the (often much larger) enclosing ERROR
	// span. The coarse parent location is only used as a fallback when no
	// child contributed anything more specific.
	var walk func(n *gotreesitter.Node) bool
	walk = func(n *gotreesitter.Node) bool {
		if n == nil || len(out) >= maxReportedParseErrors {
			return false
		}

		if n.IsMissing() {
			key := span{n.StartByte(), n.EndByte()}
			if !seen[key] {
				seen[key] = true
				msg := "missing token"
				if lang != nil {
					if t := n.Type(lang); t != "" {
						msg = fmt.Sprintf("missing %q", t)
					}
				}
				out = append(out, newParseErrorLocation(src, n.StartPoint(), msg))
			}
			return true
		}

		if n.IsError() {
			fromChildren := false
			for i := 0; i < n.ChildCount() && len(out) < maxReportedParseErrors; i++ {
				if walk(n.Child(i)) {
					fromChildren = true
				}
			}
			if fromChildren {
				return true
			}
			key := span{n.StartByte(), n.EndByte()}
			if !seen[key] {
				seen[key] = true
				text := strings.TrimSpace(n.Text(src))
				msg := "unexpected syntax"
				if text != "" {
					if len(text) > 40 {
						text = text[:40] + "..."
					}
					msg = fmt.Sprintf("unexpected syntax near %q", text)
				}
				out = append(out, newParseErrorLocation(src, n.StartPoint(), msg))
			}
			return true
		}

		contributed := false
		for i := 0; i < n.ChildCount() && len(out) < maxReportedParseErrors; i++ {
			if walk(n.Child(i)) {
				contributed = true
			}
		}
		return contributed
	}
	walk(root)
	return out
}

// newParseErrorsError formats a "parse errors" diagnostic for a tree whose
// root reported HasError(), including file:line:col and a source excerpt
// for each error site (capped at maxReportedParseErrors).
func newParseErrorsError(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, sourceFile string) error {
	locs := collectParseErrorLocations(root, lang, src)
	if len(locs) == 0 {
		// Should be rare (HasError() was true but we found no ERROR/MISSING
		// node — e.g. an unexpected tree shape); fall back to the old
		// generic message rather than claiming false precision.
		return errors.New("parse errors in ferrous-wheel source")
	}
	var b strings.Builder
	b.WriteString("parse errors in ferrous-wheel source:\n")
	for i, loc := range locs {
		b.WriteString(loc.format(sourceFile))
		if i < len(locs)-1 {
			b.WriteString("\n")
		}
	}
	return errors.New(b.String())
}

// validateNoIllegalControlBytes rejects raw C0 control bytes that are never
// legal in Go source (outside of \t, \n, \r) — see the call site in
// TranspileWithOptions for why this exists as a blanket, byte-level
// safety net rather than another grammar-specific special case.
func validateNoIllegalControlBytes(src []byte, sourceFile string) error {
	isLegal := func(b byte) bool {
		if b == '\t' || b == '\n' || b == '\r' {
			return true
		}
		return b >= 0x20 && b != 0x7f
	}
	for i, b := range src {
		if isLegal(b) {
			continue
		}
		pt := byteOffsetToPoint(src, uint32(i))
		loc := newParseErrorLocation(src, pt, fmt.Sprintf("illegal control byte 0x%02x in source", b))
		return errors.New("parse errors in ferrous-wheel source:\n" + loc.format(sourceFile))
	}
	// Go source must be valid UTF-8 ("Source code is Unicode text encoded
	// in UTF-8" — Go spec). ferrous-wheel's string/rune literal token
	// patterns (e.g. interpreted_string_literal_content's
	// `[^"\n\\]+`-shaped patterns) match arbitrary bytes, not just valid
	// UTF-8 runes, so a raw invalid byte (e.g. a lone 0xFF) pasted into a
	// string literal gets echoed straight into the generated Go, which
	// go/scanner rejects with "illegal UTF-8 encoding". Reject it here
	// with a location instead.
	for i := 0; i < len(src); {
		r, size := utf8.DecodeRune(src[i:])
		if r == utf8.RuneError && size <= 1 {
			pt := byteOffsetToPoint(src, uint32(i))
			loc := newParseErrorLocation(src, pt, "invalid UTF-8 byte sequence in source")
			return errors.New("parse errors in ferrous-wheel source:\n" + loc.format(sourceFile))
		}
		i += size
	}
	return nil
}

// validatePackageClausePresent rejects source that parses without error
// (per gotreesitter's permissive source_file rule, which allows an empty
// top-level declaration list) but doesn't actually contain a Go `package`
// clause, OR has one that isn't the first declaration. Real Go requires
// the package clause to be the very first thing in the file (Go spec:
// "Each source file consists of a package clause defining the package to
// which it belongs"); our grammar doesn't enforce that positionally (it's
// just one more alternative source_file can Repeat()), so e.g.
// `func A()\npackage A` parses fine here but real Go's parser rejects it.
// Without this check, garbage-only or mis-ordered input transpiles
// "successfully" to Go that doesn't parse. Caught by
// FuzzTranspileProducesParsableGo on the empty-string input and on
// `func A()\npackage A`.
func validatePackageClausePresent(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, sourceFile string) error {
	if root == nil {
		return errors.New("ferrous-wheel source is missing a 'package' declaration")
	}
	sawPackageClause := false
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child == nil || !child.IsNamed() {
			continue
		}
		// Leading doc comments before the package clause are ordinary,
		// idiomatic Go (`// Package foo does X.\npackage foo`) — comment
		// nodes appear as regular named children of source_file here
		// (extras are attached as siblings, not hidden), so they must be
		// skipped rather than treated as "the first declaration".
		if child.Type(lang) == "comment" {
			continue
		}
		if child.Type(lang) == "package_clause" {
			sawPackageClause = true
		}
		// First non-comment named child decides it: either it's the
		// package clause (fine, whether or not more are missing
		// elsewhere) or it isn't (a package clause anywhere after this
		// point is out of order).
		break
	}
	if sawPackageClause {
		// It's first — but source_file's Repeat() doesn't stop a SECOND
		// package_clause from appearing later either (e.g.
		// `package A\npackage A`), which real Go also rejects ("expected
		// declaration, found 'package'"). Count them all.
		count := 0
		var second *gotreesitter.Node
		for i := 0; i < root.ChildCount(); i++ {
			child := root.Child(i)
			if child != nil && child.Type(lang) == "package_clause" {
				count++
				if count == 2 && second == nil {
					second = child
				}
			}
		}
		if count > 1 {
			loc := newParseErrorLocation(src, second.StartPoint(), "only one 'package' declaration is allowed per file")
			return errors.New("parse errors in ferrous-wheel source:\n" + loc.format(sourceFile))
		}
		return nil
	}
	// Distinguish "no package clause anywhere" (i.e. the loop above never
	// found one at position 0) from "package clause exists but isn't
	// first" for a clearer message.
	hasPackageClauseAnywhere := false
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child != nil && child.Type(lang) == "package_clause" {
			hasPackageClauseAnywhere = true
			break
		}
	}
	loc := fmt.Sprintf("%d:%d", 1, 1)
	if sourceFile != "" {
		loc = sourceFile + ":" + loc
	}
	if hasPackageClauseAnywhere {
		return fmt.Errorf("%s: 'package' declaration must be the first declaration in the file", loc)
	}
	return fmt.Errorf("%s: ferrous-wheel source is missing a 'package' declaration", loc)
}

// validateNoMultilineInterpretedStrings rejects a raw (unescaped) newline
// embedded inside a double-quoted string literal, e.g. `"foo<LF>bar"`
// pasted directly rather than written as `"foo\nbar"`. Go's interpreted
// string literals must not contain a literal newline (only raw/backtick
// strings can span lines); ferrous-wheel's grammar rule for
// interpreted_string_literal only excludes `\n` from its *content* pattern
// (`[^"\n\\]+`), not from the gap between the opening `"` and the first
// content/escape piece, so a bare newline right after the opening quote
// gets silently treated as insignificant extras instead of ending the
// token or producing a parse error — see grammar.go's narrowed SetExtras.
// Detected post-parse by comparing the node's start and end rows.
func validateNoMultilineInterpretedStrings(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, sourceFile string) error {
	if root == nil || lang == nil {
		return nil
	}
	var walk func(n *gotreesitter.Node) error
	walk = func(n *gotreesitter.Node) error {
		if n == nil {
			return nil
		}
		if n.Type(lang) == "interpreted_string_literal" && n.StartPoint().Row != n.EndPoint().Row {
			loc := newParseErrorLocation(src, n.StartPoint(), "interpreted string literal contains a raw newline; use \\n or a raw string (`...`) instead")
			return errors.New("parse errors in ferrous-wheel source:\n" + loc.format(sourceFile))
		}
		for i := 0; i < n.ChildCount(); i++ {
			if err := walk(n.Child(i)); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

// validateNoBareCommaParameterList rejects a parameter_list consisting of
// only a comma and no actual parameters (e.g. `func A(,)`). go_grammar.go's
// parameter_list rule (ported from tree-sitter-go, which appears to accept
// this for editor error-tolerance while typing) makes both the parameter
// sequence AND the trailing comma independently optional, so `(,)` parses
// as a parameter_list with zero parameter_declaration children and a bare
// "," among its unnamed children — real Go's parser rejects this
// ("expected ')', found ','").
func validateNoBareCommaParameterList(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, sourceFile string) error {
	if root == nil || lang == nil {
		return nil
	}
	var walk func(n *gotreesitter.Node) error
	walk = func(n *gotreesitter.Node) error {
		if n == nil {
			return nil
		}
		if n.Type(lang) == "parameter_list" && n.NamedChildCount() == 0 {
			for i := 0; i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c != nil && !c.IsNamed() && c.Type(lang) == "," {
					loc := newParseErrorLocation(src, n.StartPoint(), "empty parameter list cannot have a comma")
					return errors.New("parse errors in ferrous-wheel source:\n" + loc.format(sourceFile))
				}
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			if err := walk(n.Child(i)); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

// goReservedWords is the complete set of Go keywords (Go spec, ยง Keywords).
// None of them can legally be used as an identifier anywhere in Go source.
var goReservedWords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// validateNoReservedWordIdentifiers rejects a Go reserved keyword used
// anywhere an identifier is expected (e.g. `package func`). The base
// identifier token — `Pat([_\p{XID_Start}][_\p{XID_Continue}]*)` in
// go_grammar.go — matches keyword text just as readily as a real
// identifier; keywords are normally disambiguated by the grammar offering
// them as an explicit alternative at valid keyword positions, but once
// inside a rule that ONLY expects `_package_identifier` (or any other bare
// identifier field), there's no such alternative to prefer the keyword
// token, so the lexer accepts it as a plain identifier. Real Go's parser
// rejects this outright; we do the same, post-parse, by scanning every
// identifier-shaped node in the tree.
func validateNoReservedWordIdentifiers(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, sourceFile string) error {
	if root == nil || lang == nil {
		return nil
	}
	var walk func(n *gotreesitter.Node) error
	walk = func(n *gotreesitter.Node) error {
		if n == nil {
			return nil
		}
		nodeType := n.Type(lang)
		if nodeType == "identifier" || strings.HasSuffix(nodeType, "_identifier") {
			text := n.Text(src)
			if goReservedWords[text] {
				loc := newParseErrorLocation(src, n.StartPoint(), fmt.Sprintf("%q is a reserved word and cannot be used as an identifier", text))
				return errors.New("parse errors in ferrous-wheel source:\n" + loc.format(sourceFile))
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			if err := walk(n.Child(i)); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

// validateNoSelectorOnIntLiteral rejects a selector_expression whose
// operand is a bare int_literal (e.g. `0.Foo`, produced by fuzzing as
// `for 0.A0{}`). This is never valid — an integer literal has no fields or
// methods in either Go or ferrous-wheel — but it becomes syntactically
// reachable as a side effect of the range-operator fix in grammar.go
// (restrictBareTrailingDotFloat): FW no longer lexes a bare trailing-dot
// numeral like "0." as a complete float (so that `0..10` lexes as a range
// instead), which means "0.A0" now lexes as int_literal("0") + "."
// (selector) + field_identifier("A0") — a real, if useless,
// selector_expression from FW's point of view. But *real* Go's scanner
// still allows the bare trailing-dot float form, so it lexes the same text
// completely differently (float "0." followed by an unexpected "A0"),
// meaning the Go we echo out and the Go a real `go build` sees disagree.
// Rejecting it here keeps that disagreement from ever reaching output.
func validateNoSelectorOnIntLiteral(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, sourceFile string) error {
	if root == nil || lang == nil {
		return nil
	}
	var walk func(n *gotreesitter.Node) error
	walk = func(n *gotreesitter.Node) error {
		if n == nil {
			return nil
		}
		if n.Type(lang) == "selector_expression" {
			if operand := n.ChildByFieldName("operand", lang); operand != nil && operand.Type(lang) == "int_literal" {
				msg := fmt.Sprintf("%q looks like a truncated float literal followed by a selector; "+
					"add a space (\"%s .field\") or a decimal point (\"%s.0.field\") if a selector was really intended",
					n.Text(src), operand.Text(src), operand.Text(src))
				loc := newParseErrorLocation(src, n.StartPoint(), msg)
				return errors.New("parse errors in ferrous-wheel source:\n" + loc.format(sourceFile))
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			if err := walk(n.Child(i)); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

// topLevelAllowedNodeTypes is the closed set of node types gotreesitter's
// permissive source_file rule allows to reach the top of a `.fw` file that
// are ALSO legitimate at Go package scope. source_file's grammar rule is
// `Repeat(Choice(_statement, _top_level_declaration))`, and `_statement`
// pulls in essentially every executable-statement and expression form
// (bare expressions, if/for/return/assignment, FW's let/match/log/etc.
// sugar) so they can appear inside function bodies — but that means they're
// also *syntactically* reachable directly at package level, where none of
// them are valid Go. Real `go/parser` rejects them; we'd otherwise emit
// invalid Go silently (e.g. a fuzzer-found `package A\n0\n` transpiles
// to a bare `0` at package scope).
//
// This is deliberately an allowlist (fail closed) rather than a denylist of
// known-bad statement types (fail open): grammar.go's AppendChoice calls
// add new sugar to `_statement` over time, and an allowlist means a
// forgotten update here only makes the check *stricter* than necessary
// (rejecting something that's actually fine, an easy bug report to fix) —
// never silently permits a fresh instance of this bug class.
var topLevelAllowedNodeTypes = map[string]bool{
	"package_clause":       true,
	"import_declaration":   true,
	"function_declaration": true,
	"method_declaration":   true,
	"const_declaration":    true,
	"type_declaration":     true,
	"var_declaration":      true,
	// FW sugar explicitly added to _top_level_declaration in grammar.go:
	"enum_declaration":   true,
	"derive_declaration": true,
	"impl_block":         true,
	"log_config":         true,
	// packed_annotation wraps a _statement (in practice a type_declaration:
	// `packed type Packet struct {...}`) and is only added to `_statement`,
	// not `_top_level_declaration`, but top-level packed struct
	// declarations are a real, tested feature (see stress_test.go /
	// transpile_test.go packed-struct cases).
	"packed_annotation": true,
	// Comments are always legitimate at package scope (file-header doc
	// comments before `package`, doc comments on declarations, standalone
	// comments between declarations, ...) — comment nodes appear as
	// ordinary named children of source_file here (extras are attached as
	// siblings, not hidden), so without this they'd be wrongly rejected
	// as "not a declaration". See validatePackageClausePresent's matching
	// skip for the leading-comment-before-package-clause case.
	"comment": true,
	// gotreesitter parse-error recovery nodes are handled separately by
	// newParseErrorsError (root.HasError() is checked first); don't
	// double-report them here.
	"ERROR": true,
}

// validateTopLevelDeclarationsOnly rejects any direct child of source_file
// that isn't a recognized top-level declaration. See topLevelAllowedNodeTypes.
func validateTopLevelDeclarationsOnly(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, sourceFile string) error {
	if root == nil || lang == nil {
		return nil
	}
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child == nil || !child.IsNamed() {
			continue
		}
		nodeType := child.Type(lang)
		if topLevelAllowedNodeTypes[nodeType] {
			continue
		}
		pt := child.StartPoint()
		text := strings.TrimSpace(child.Text(src))
		if len(text) > 40 {
			text = text[:40] + "..."
		}
		msg := fmt.Sprintf("%s is not allowed at package level (only declarations are valid here)", nodeType)
		if text != "" {
			msg = fmt.Sprintf("%s: %q", msg, text)
		}
		loc := newParseErrorLocation(src, pt, msg)
		return errors.New("parse errors in ferrous-wheel source:\n" + loc.format(sourceFile))
	}
	return nil
}

// newUnparsedTextError formats a diagnostic for a byte range of source that
// tree-sitter's recovery left uncovered by any sibling node (garbage text
// that isn't inside an ERROR/MISSING node at all, e.g. `package mx n`).
func newUnparsedTextError(src []byte, sourceFile string, offset uint32, gap []byte) error {
	trimmed := bytes.TrimLeft(gap, " \t\r\n")
	leadingWS := len(gap) - len(trimmed)
	pt := byteOffsetToPoint(src, offset+uint32(leadingWS))
	text := strings.TrimSpace(string(gap))
	if text == "" {
		// The gap is non-empty per isFWWhitespaceOnly (which is narrower
		// than unicode.IsSpace) but strings.TrimSpace ate it anyway —
		// e.g. a vertical tab or form feed, which FW doesn't treat as
		// insignificant whitespace even though it looks blank. Fall back
		// to the raw (untrimmed-by-TrimSpace) gap so the diagnostic isn't
		// "unparsed text \"\"".
		text = string(trimmed)
	} else if len(text) > 40 {
		text = text[:40] + "..."
	}
	loc := newParseErrorLocation(src, pt, fmt.Sprintf("unparsed text %q", text))
	return errors.New("parse errors in ferrous-wheel source:\n" + loc.format(sourceFile))
}
