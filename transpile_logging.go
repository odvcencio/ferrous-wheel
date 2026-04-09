package ferrouswheel

import (
	"fmt"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// _fwLevelTrace is defined as a const in the emitted log helper (Task 9).
// It equals slog.LevelDebug - 4. The generated code references the named constant.
var logLevelToSlog = map[string]string{
	"trace": "slog.Log(context.Background(), _fwLevelTrace, ",
	"debug": "slog.Debug(",
	"info":  "slog.Info(",
	"warn":  "slog.Warn(",
	"error": "slog.Error(",
	"fatal": "slog.Error(",
}

func (t *fwTranspiler) emitLogStatement(n *gotreesitter.Node) string {
	t.needsSlog = true
	t.needsLogHelper = true
	t.needsContext = true

	levelNode := t.childByField(n, "level")
	if levelNode == nil {
		return t.text(n)
	}
	level := t.text(levelNode)

	msgNode := t.childByField(n, "message")
	if msgNode == nil {
		return t.text(n)
	}
	msg := t.emit(msgNode)

	slogCall := logLevelToSlog[level]

	attrs := t.collectLogAttrs(n)

	var b strings.Builder
	if len(attrs) == 0 {
		fmt.Fprintf(&b, "%s%s)", slogCall, msg)
	} else {
		fmt.Fprintf(&b, "%s%s, %s)", slogCall, msg, strings.Join(attrs, ", "))
	}

	if level == "fatal" {
		t.needsOS = true
		b.WriteString("\nos.Exit(1)")
	}

	return b.String()
}

var colorStyleToANSI = map[string]string{
	"red":       "31",
	"green":     "32",
	"yellow":    "33",
	"blue":      "34",
	"magenta":   "35",
	"cyan":      "36",
	"gray":      "90",
	"bold":      "1",
	"dim":       "2",
	"italic":    "3",
	"underline": "4",
}

func (t *fwTranspiler) emitColorCall(n *gotreesitter.Node) string {
	t.needsColorHelper = true
	t.needsFmt = true // generated code uses fmt.Sprint
	t.needsOS = true  // color helper uses os.Stdout.Stat and os.Getenv

	styleNode := t.childByField(n, "style")
	valueNode := t.childByField(n, "value")
	if styleNode == nil || valueNode == nil {
		return t.text(n)
	}

	style := t.text(styleNode)
	code := colorStyleToANSI[style]
	val := t.emit(valueNode)

	return fmt.Sprintf("_fwcolor(%q, fmt.Sprint(%s))", code, val)
}

func (t *fwTranspiler) emitLogWithBlock(n *gotreesitter.Node) string {
	t.needsSlog = true
	t.needsLogHelper = true // defines _fwLevelTrace const
	t.needsContext = true
	t.needsTime = true

	attrs := t.collectLogAttrs(n)
	block := t.findBlock(n)

	var b strings.Builder
	b.WriteString("{\n")

	// Save previous logger, push scoped logger
	b.WriteString("\t_fwlogPrev := slog.Default()\n")
	if len(attrs) > 0 {
		fmt.Fprintf(&b, "\tslog.SetDefault(slog.Default().With(%s))\n", strings.Join(attrs, ", "))
	}

	// Trace-level entry span
	fmt.Fprintf(&b, "\tslog.Log(context.Background(), _fwLevelTrace, \"▶ enter\")\n")
	b.WriteString("\t_fwWithStart := time.Now()\n")

	// Body
	fmt.Fprintf(&b, "\t%s\n", block)

	// Trace-level exit span with duration
	fmt.Fprintf(&b, "\tslog.Log(context.Background(), _fwLevelTrace, \"◀ exit\", \"duration\", time.Since(_fwWithStart))\n")

	// Restore previous logger
	b.WriteString("\tslog.SetDefault(_fwlogPrev)\n")
	b.WriteString("}")

	return b.String()
}

func (t *fwTranspiler) collectLogAttrs(n *gotreesitter.Node) []string {
	var attrs []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if t.nodeType(child) != "log_attr" {
			continue
		}
		keyNode := t.childByField(child, "key")
		valNode := t.childByField(child, "value")
		bareNode := t.childByField(child, "bare")

		if bareNode != nil {
			name := t.text(bareNode)
			attrs = append(attrs, fmt.Sprintf("%q, %s", name, name))
		} else if keyNode != nil && valNode != nil {
			key := t.text(keyNode)
			val := t.emit(valNode)
			attrs = append(attrs, fmt.Sprintf("%q, %s", key, val))
		}
	}
	return attrs
}
