package ferrouswheel

import (
	"bytes"
	"fmt"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// Format formats .fw source code and returns the formatted result.
// It parses the source with tree-sitter, walks the CST, and makes targeted
// whitespace adjustments while preserving comments and intentional structure.
// The formatter is idempotent: Format(Format(source)) equals Format(source).
func Format(source []byte) ([]byte, error) {
	lang, err := getFWLanguage()
	if err != nil {
		return nil, fmt.Errorf("generate ferrous-wheel language: %w", err)
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	root := tree.RootNode()
	if root == nil {
		return nil, fmt.Errorf("parse returned nil root")
	}

	f := &formatter{
		src:  source,
		lang: lang,
		out:  bytes.Buffer{},
	}
	f.out.Grow(len(source))
	f.formatNode(root, 0)
	// Emit any trailing content after the last token (e.g., final newline).
	if f.pos < uint32(len(source)) {
		f.out.Write(source[f.pos:])
	}
	return f.out.Bytes(), nil
}

type formatter struct {
	src  []byte
	lang *gotreesitter.Language
	out  bytes.Buffer
	// pos tracks the current position in the source being consumed.
	pos uint32
}

// nodeType returns the tree-sitter node type string.
func (f *formatter) nodeType(n *gotreesitter.Node) string {
	return n.Type(f.lang)
}

// formatNode processes a node. For leaf nodes, it emits the token text with
// appropriate whitespace. For internal nodes, it recurses into children.
func (f *formatter) formatNode(n *gotreesitter.Node, depth int) {
	if n == nil {
		return
	}

	ntype := f.nodeType(n)

	switch ntype {
	case "let_declaration":
		f.formatLetDecl(n, depth)
		return
	case "let_multi_declaration":
		f.formatLetMultiDecl(n, depth)
		return
	case "match_expression":
		f.formatMatchExpr(n, depth)
		return
	case "enum_declaration":
		f.formatEnumDecl(n, depth)
		return
	}

	childCount := n.ChildCount()
	if childCount == 0 {
		// Leaf node: emit any whitespace gap before this token, then the token itself.
		f.emitGap(n.StartByte())
		f.emitToken(n)
		return
	}

	for i := 0; i < childCount; i++ {
		child := n.Child(i)
		if child != nil {
			f.formatNode(child, depth)
		}
	}
}

// emitGap writes the whitespace between the current position and targetStart.
// It preserves newlines but normalizes horizontal whitespace on each line
// (though the leading indent is preserved from source, since we operate
// on the gaps between CST tokens).
func (f *formatter) emitGap(targetStart uint32) {
	if targetStart <= f.pos {
		return
	}
	gap := f.src[f.pos:targetStart]
	f.out.Write(gap)
	f.pos = targetStart
}

// emitToken writes the source text for a leaf node.
func (f *formatter) emitToken(n *gotreesitter.Node) {
	start := n.StartByte()
	end := n.EndByte()
	if start < f.pos {
		start = f.pos
	}
	if end > start {
		f.out.Write(f.src[start:end])
		f.pos = end
	}
}

// emitString writes a literal string, advancing past the source range.
func (f *formatter) emitString(s string, skipTo uint32) {
	f.out.WriteString(s)
	if skipTo > f.pos {
		f.pos = skipTo
	}
}

// emitChild emits a child node: recurse if it has children, otherwise emit the token.
func (f *formatter) emitChild(child *gotreesitter.Node, depth int) {
	if child.ChildCount() > 0 {
		f.formatNode(child, depth)
	} else {
		f.emitToken(child)
	}
}

// formatLetDecl normalizes spacing in let declarations.
// "let   x   =   1" becomes "let x = 1"
// "let  mut  x = 1" becomes "let mut x = 1"
func (f *formatter) formatLetDecl(n *gotreesitter.Node, depth int) {
	// Emit any gap (including indentation) before this node.
	f.emitGap(n.StartByte())

	// Collect all children in order and emit them with normalized single spaces.
	childCount := n.ChildCount()
	for i := 0; i < childCount; i++ {
		child := n.Child(i)
		if child == nil {
			continue
		}

		ctype := f.nodeType(child)

		if i == 0 {
			// First child is "let" keyword - emit directly.
			f.pos = child.StartByte()
			f.emitToken(child)
			continue
		}

		// Skip source gap — we control all whitespace between let children.
		f.pos = child.StartByte()

		switch ctype {
		case ":":
			// "x: int" — no space before colon.
			f.out.WriteByte(':')
			f.pos = child.EndByte()
		case "=":
			// " =" — space before =. The space after = is added by the next
			// child's default case.
			f.out.WriteString(" =")
			f.pos = child.EndByte()
		default:
			f.out.WriteByte(' ')
			f.emitChild(child, depth)
		}
	}
}

// formatLetMultiDecl normalizes spacing in let multi-declarations.
// "let  (a, b)  =  f()" becomes "let (a, b) = f()"
func (f *formatter) formatLetMultiDecl(n *gotreesitter.Node, depth int) {
	f.emitGap(n.StartByte())

	childCount := n.ChildCount()
	for i := 0; i < childCount; i++ {
		child := n.Child(i)
		if child == nil {
			continue
		}

		ctype := f.nodeType(child)

		if i == 0 {
			// "let" keyword
			f.pos = child.StartByte()
			f.emitToken(child)
			continue
		}

		// Skip source gap — we control whitespace.
		f.pos = child.StartByte()

		switch ctype {
		case "(":
			f.out.WriteString(" (")
			f.pos = child.EndByte()
		case ")":
			f.out.WriteByte(')')
			f.pos = child.EndByte()
		case ",":
			f.out.WriteByte(',')
			f.pos = child.EndByte()
		case "=":
			f.out.WriteString(" =")
			f.pos = child.EndByte()
		default:
			// For identifiers after "(" or ",", add a space.
			// After "(" we want no space, after "," we want one space.
			if i > 0 {
				prev := n.Child(i - 1)
				prevType := ""
				if prev != nil {
					prevType = f.nodeType(prev)
				}
				if prevType == "(" {
					// No space after open paren.
				} else {
					f.out.WriteByte(' ')
				}
			}
			f.emitChild(child, depth)
		}
	}
}

// formatMatchExpr formats match expressions with aligned => arrows.
func (f *formatter) formatMatchExpr(n *gotreesitter.Node, depth int) {
	f.emitGap(n.StartByte())

	// Collect match arms and find the longest pattern for alignment.
	type armInfo struct {
		node       *gotreesitter.Node
		patternLen int
	}
	var arms []armInfo

	for i := 0; i < n.ChildCount(); i++ {
		child := n.Child(i)
		if child != nil && f.nodeType(child) == "match_arm" {
			plen := f.matchArmPatternLen(child)
			arms = append(arms, armInfo{node: child, patternLen: plen})
		}
	}

	// Find max pattern length for alignment.
	maxPatLen := 0
	for _, a := range arms {
		if a.patternLen > maxPatLen {
			maxPatLen = a.patternLen
		}
	}

	// Walk all children. When we hit a match_arm, format it specially.
	armIdx := 0
	childCount := n.ChildCount()
	for i := 0; i < childCount; i++ {
		child := n.Child(i)
		if child == nil {
			continue
		}
		ctype := f.nodeType(child)

		if ctype == "match_arm" && armIdx < len(arms) {
			f.formatMatchArm(child, depth+1, maxPatLen, arms[armIdx].patternLen)
			armIdx++
		} else {
			f.emitGap(child.StartByte())
			if child.ChildCount() > 0 {
				f.formatNode(child, depth)
			} else {
				f.emitToken(child)
			}
		}
	}
}

// matchArmPatternLen computes the text length of the pattern portion of a match arm
// (everything before the => token).
func (f *formatter) matchArmPatternLen(arm *gotreesitter.Node) int {
	for i := 0; i < arm.ChildCount(); i++ {
		child := arm.Child(i)
		if child != nil && f.nodeType(child) == "=>" {
			// Everything from arm start to => start, trimmed.
			patText := bytes.TrimSpace(f.src[arm.StartByte():child.StartByte()])
			return len(patText)
		}
	}
	return 0
}

// formatMatchArm formats a single match arm with => alignment.
func (f *formatter) formatMatchArm(arm *gotreesitter.Node, depth int, maxPatLen, thisPatLen int) {
	// Emit the gap (newline + indentation) before this arm.
	f.emitGap(arm.StartByte())

	padding := ""
	if maxPatLen > thisPatLen {
		padding = spaces(maxPatLen - thisPatLen)
	}

	childCount := arm.ChildCount()
	for i := 0; i < childCount; i++ {
		child := arm.Child(i)
		if child == nil {
			continue
		}
		ctype := f.nodeType(child)

		if ctype == "=>" {
			f.emitString(padding+" => ", child.EndByte())
		} else if i > 0 {
			// For children after =>, preserve gap.
			prev := arm.Child(i - 1)
			if prev != nil && f.nodeType(prev) == "=>" {
				// Already handled space in => emission.
				f.pos = child.StartByte()
			} else {
				f.emitGap(child.StartByte())
			}
			if child.ChildCount() > 0 {
				f.formatNode(child, depth)
			} else {
				f.emitToken(child)
			}
		} else {
			f.emitGap(child.StartByte())
			if child.ChildCount() > 0 {
				f.formatNode(child, depth)
			} else {
				f.emitToken(child)
			}
		}
	}
}

// formatEnumDecl formats enum declarations with one variant per line.
func (f *formatter) formatEnumDecl(n *gotreesitter.Node, depth int) {
	// For now, just use the default formatting (preserve source structure).
	// Enum declarations in the grammar already expect one variant per line.
	f.emitGap(n.StartByte())
	childCount := n.ChildCount()
	for i := 0; i < childCount; i++ {
		child := n.Child(i)
		if child != nil {
			f.formatNode(child, depth)
		}
	}
}

// spaces returns a string of n spaces.
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
