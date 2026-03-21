package ferrouswheel

import (
	"fmt"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// arena scratch { body } or arena scratch 1024*1024 { body }
// -> bump allocator with make([]byte, 0, size)
func (t *fwTranspiler) emitArena(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	if nameNode == nil {
		return t.text(n)
	}
	name := t.text(nameNode)
	t.needsUnsafe = true

	sizeExpr := "1 << 20"
	if sizeNode := t.childByField(n, "size"); sizeNode != nil {
		sizeExpr = t.emit(sizeNode)
	}

	block := t.findBlock(n)

	var b strings.Builder
	fmt.Fprintf(&b, "_arenaSize := %s\n", sizeExpr)
	fmt.Fprintf(&b, "_arena_%s := make([]byte, 0, _arenaSize)\n", name)
	fmt.Fprintf(&b, "_arenaAlloc_%s := func(size int) unsafe.Pointer {\n", name)
	fmt.Fprintf(&b, "\toff := len(_arena_%s)\n", name)
	fmt.Fprintf(&b, "\t_arena_%s = _arena_%s[:off+size]\n", name, name)
	fmt.Fprintf(&b, "\treturn unsafe.Pointer(&_arena_%s[off])\n", name)
	fmt.Fprintf(&b, "}\n")
	fmt.Fprintf(&b, "_ = _arenaAlloc_%s\n", name)
	fmt.Fprintf(&b, "defer func() { _arena_%s = nil }()\n", name)
	b.WriteString(block)
	return b.String()
}

// pin data -> runtime.KeepAlive + SetFinalizer(nil)
func (t *fwTranspiler) emitPin(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	if nameNode == nil {
		return t.text(n)
	}
	name := t.text(nameNode)
	t.needsRuntime = true

	var b strings.Builder
	fmt.Fprintf(&b, "_pin_%s := &%s\n", name, name)
	fmt.Fprintf(&b, "runtime.SetFinalizer(_pin_%s, nil)\n", name)
	fmt.Fprintf(&b, "defer runtime.KeepAlive(%s)", name)
	return b.String()
}

// unpin data -> runtime.KeepAlive at this point
func (t *fwTranspiler) emitUnpin(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	if nameNode == nil {
		return t.text(n)
	}
	name := t.text(nameNode)
	t.needsRuntime = true

	return fmt.Sprintf("runtime.KeepAlive(%s)", name)
}

// unsafe cast(expr, TargetType) -> helper-backed reinterpretation.
func (t *fwTranspiler) emitUnsafeCast(n *gotreesitter.Node) string {
	expr := t.childByField(n, "expr")
	targetType := t.childByField(n, "target_type")
	if expr == nil || targetType == nil {
		return t.text(n)
	}
	t.needsUnsafe = true
	t.needsUnsafeCast = true

	return fmt.Sprintf("_fwUnsafeCast[%s](%s)", t.emit(targetType), t.emit(expr))
}

// mmap file "data.bin" as data []byte { body }
func (t *fwTranspiler) emitMmap(n *gotreesitter.Node) string {
	pathNode := t.childByField(n, "path")
	nameNode := t.childByField(n, "name")
	if pathNode == nil || nameNode == nil {
		return t.text(n)
	}
	t.needsOS = true
	t.needsSyscall = true

	pathStr := t.text(pathNode)
	name := t.text(nameNode)
	block := t.findBlock(n)

	var b strings.Builder
	fmt.Fprintf(&b, "_f, _ := os.Open(%s)\n", pathStr)
	b.WriteString("defer _f.Close()\n")
	b.WriteString("_fi, _ := _f.Stat()\n")
	fmt.Fprintf(&b, "%s, _ := syscall.Mmap(int(_f.Fd()), 0, int(_fi.Size()), syscall.PROT_READ, syscall.MAP_SHARED)\n", name)
	fmt.Fprintf(&b, "defer syscall.Munmap(%s)\n", name)
	b.WriteString(block)
	return b.String()
}

// packed struct Foo { ... } -> pass through with alignment comment
func (t *fwTranspiler) emitPacked(n *gotreesitter.Node) string {
	decl := t.childByField(n, "decl")
	if decl == nil {
		return t.text(n)
	}
	return "// packed: manual alignment required\n" + t.emit(decl)
}

// vectorize for v in items { body } -> for loop with vectorize hint comment
func (t *fwTranspiler) emitVectorize(n *gotreesitter.Node) string {
	varNode := t.childByField(n, "var")
	rangeNode := t.childByField(n, "range")
	if varNode == nil || rangeNode == nil {
		return t.text(n)
	}

	varName := t.text(varNode)
	block := t.findBlock(n)

	if t.nodeType(rangeNode) == "range_expression" {
		start := t.childByField(rangeNode, "start")
		end := t.childByField(rangeNode, "end")
		if start != nil && end != nil {
			return fmt.Sprintf("// vectorize: compiler hint\nfor %s := %s; %s < %s; %s++ %s",
				varName, t.emit(start), varName, t.emit(end), varName, block)
		}
	}

	return fmt.Sprintf("// vectorize: compiler hint\nfor _, %s := range %s %s", varName, t.emit(rangeNode), block)
}
