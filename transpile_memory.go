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
	b.WriteString("if _arenaSize < 0 {\n\tpanic(\"arena size must be >= 0\")\n}\n")
	fmt.Fprintf(&b, "_arena_%s := make([]byte, 0, _arenaSize)\n", name)
	fmt.Fprintf(&b, "_arenaAlloc_%s := func(size int) unsafe.Pointer {\n", name)
	b.WriteString("\tif size < 0 {\n\t\tpanic(\"arena allocation size must be >= 0\")\n\t}\n")
	b.WriteString("\tif size == 0 {\n\t\treturn nil\n\t}\n")
	fmt.Fprintf(&b, "\toff := len(_arena_%s)\n", name)
	fmt.Fprintf(&b, "\tremaining := cap(_arena_%s) - off\n", name)
	fmt.Fprintf(&b, "\tif size > remaining {\n\t\tpanic(%q)\n\t}\n", "arena "+name+" out of memory")
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
	typeNode := t.childByField(n, "type")
	if pathNode == nil || nameNode == nil || typeNode == nil {
		return t.text(n)
	}
	t.needsOS = true
	t.needsSyscall = true

	pathStr := t.text(pathNode)
	name := t.text(nameNode)
	block := t.findBlock(n)
	writable := t.childByField(n, "writable") != nil

	var b strings.Builder
	if writable {
		fmt.Fprintf(&b, "_f, _fwMmapErr := os.OpenFile(%s, os.O_RDWR, 0)\n", pathStr)
	} else {
		fmt.Fprintf(&b, "_f, _fwMmapErr := os.Open(%s)\n", pathStr)
	}
	b.WriteString("if _fwMmapErr != nil {\n\tpanic(_fwMmapErr)\n}\n")
	b.WriteString("defer _f.Close()\n")
	b.WriteString("_fi, _fwMmapErr := _f.Stat()\n")
	b.WriteString("if _fwMmapErr != nil {\n\tpanic(_fwMmapErr)\n}\n")
	b.WriteString("_fwMmapSize := _fi.Size()\n")
	b.WriteString("if _fwMmapSize < 0 {\n\tpanic(\"mmap file size must be non-negative\")\n}\n")
	b.WriteString("if _fwMmapSize > int64(int(^uint(0)>>1)) {\n\tpanic(\"mmap file too large\")\n}\n")
	fmt.Fprintf(&b, "var %s []byte\n", name)
	b.WriteString("if _fwMmapSize > 0 {\n")
	if writable {
		fmt.Fprintf(&b, "\t%s, _fwMmapErr = syscall.Mmap(int(_f.Fd()), 0, int(_fwMmapSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)\n", name)
	} else {
		fmt.Fprintf(&b, "\t%s, _fwMmapErr = syscall.Mmap(int(_f.Fd()), 0, int(_fwMmapSize), syscall.PROT_READ, syscall.MAP_SHARED)\n", name)
	}
	b.WriteString("\tif _fwMmapErr != nil {\n\t\tpanic(_fwMmapErr)\n\t}\n")
	fmt.Fprintf(&b, "\tdefer func() {\n\t\tif _fwMmapErr := syscall.Munmap(%s); _fwMmapErr != nil {\n\t\t\tpanic(_fwMmapErr)\n\t\t}\n\t}()\n", name)
	b.WriteString("}\n")
	b.WriteString(block)
	return b.String()
}

// packed struct Foo { ... } -> pass through with alignment comment
func (t *fwTranspiler) emitPacked(n *gotreesitter.Node) string {
	decl := t.childByField(n, "decl")
	if decl == nil {
		return t.text(n)
	}
	out := "// packed: manual alignment required\n" + t.emit(decl)
	if n.Parent() == nil || n.Parent().Type(t.lang) != "source_file" {
		return out
	}
	name, size, ok := t.packedStructInfo(decl)
	if !ok {
		return out
	}
	t.needsUnsafe = true
	t.needsFmt = true
	return out + fmt.Sprintf(`
func init() {
	if sz := unsafe.Sizeof(%s{}); sz != %d {
		panic(fmt.Sprintf("packed struct %s: expected size %d, got %%d - check field alignment", sz))
	}
}
`, name, size, name, size)
}

func (t *fwTranspiler) packedStructInfo(decl *gotreesitter.Node) (string, int, bool) {
	if decl == nil {
		return "", 0, false
	}
	switch decl.Type(t.lang) {
	case "type_declaration":
		for i := 0; i < int(decl.NamedChildCount()); i++ {
			child := decl.NamedChild(i)
			if child != nil && child.Type(t.lang) == "type_spec" {
				return t.packedStructInfo(child)
			}
		}
	case "type_spec":
		nameNode := t.childByField(decl, "name")
		typeNode := t.childByField(decl, "type")
		if nameNode == nil || typeNode == nil || typeNode.Type(t.lang) != "struct_type" {
			return "", 0, false
		}
		size, ok := t.packedStructSize(typeNode)
		if !ok {
			return "", 0, false
		}
		return t.text(nameNode), size, true
	}
	return "", 0, false
}

func (t *fwTranspiler) packedStructSize(structNode *gotreesitter.Node) (int, bool) {
	if structNode == nil {
		return 0, false
	}
	total := 0
	for i := 0; i < int(structNode.NamedChildCount()); i++ {
		child := structNode.NamedChild(i)
		if child == nil || child.Type(t.lang) != "field_declaration_list" {
			continue
		}
		for j := 0; j < int(child.NamedChildCount()); j++ {
			field := child.NamedChild(j)
			if field == nil || field.Type(t.lang) != "field_declaration" {
				continue
			}
			typeNode := t.childByField(field, "type")
			fieldSize, ok := packedFieldSize(t.text(typeNode))
			if !ok {
				return 0, false
			}
			count := 0
			for k := 0; k < int(field.NamedChildCount()); k++ {
				if named := field.NamedChild(k); named != nil && named.Type(t.lang) == "field_identifier" {
					count++
				}
			}
			if count == 0 {
				return 0, false
			}
			total += fieldSize * count
		}
		return total, true
	}
	return 0, false
}

func packedFieldSize(typeName string) (int, bool) {
	switch strings.TrimSpace(typeName) {
	case "bool", "byte", "int8", "uint8":
		return 1, true
	case "int16", "uint16":
		return 2, true
	case "float32", "int32", "rune", "uint32":
		return 4, true
	case "float64", "int64", "uint64":
		return 8, true
	default:
		return 0, false
	}
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
