package ferrouswheel

import (
	"fmt"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// collectTypes parses FW/Go source and walks the CST to register all
// top-level declarations (functions, structs, enums, imports, impl blocks)
// into a TypeEnv.
func collectTypes(src []byte) (*TypeEnv, error) {
	lang, err := getFWLanguage()
	if err != nil {
		return nil, fmt.Errorf("generate language: %w", err)
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	root := tree.RootNode()
	env := NewTypeEnv()
	c := &collector{env: env, src: src, lang: lang}
	c.walkChildren(root)
	return env, nil
}

// collector walks the CST and populates a TypeEnv.
type collector struct {
	env  *TypeEnv
	src  []byte
	lang *gotreesitter.Language
}

func (c *collector) text(n *gotreesitter.Node) string {
	return string(c.src[n.StartByte():n.EndByte()])
}

func (c *collector) nodeType(n *gotreesitter.Node) string {
	return n.Type(c.lang)
}

func (c *collector) childByField(n *gotreesitter.Node, field string) *gotreesitter.Node {
	return n.ChildByFieldName(field, c.lang)
}

// walkChildren visits all named children of a node.
func (c *collector) walkChildren(n *gotreesitter.Node) {
	count := int(n.NamedChildCount())
	for i := 0; i < count; i++ {
		child := n.NamedChild(i)
		c.visitNode(child)
	}
	// Handle top-level type declarations that the FW grammar cannot parse
	// as proper type_declaration nodes. The pattern is:
	//   ERROR("type") + identifier("Name") + struct_type/type_identifier
	// at the root level.
	c.collectOrphanedTypeDecls(n)
}

// visitNode dispatches on node type and registers declarations.
func (c *collector) visitNode(n *gotreesitter.Node) {
	switch c.nodeType(n) {
	case "function_declaration":
		c.collectFunction(n)
	case "type_declaration":
		c.collectTypeDecl(n)
	case "enum_declaration":
		c.collectEnum(n)
	case "import_declaration":
		c.collectImport(n)
	case "impl_block":
		c.collectImpl(n)
	default:
		// Recurse into blocks, source_file, etc. to find nested declarations
		c.walkChildren(n)
	}
}

// collectOrphanedTypeDecls handles `type X struct {...}` at the top level.
// The FW grammar's _top_level_declaration doesn't include _declaration,
// so `type` declarations at file scope parse as ERROR nodes. We detect
// the pattern: ERROR("type") followed by identifier then struct_type.
func (c *collector) collectOrphanedTypeDecls(n *gotreesitter.Node) {
	count := int(n.NamedChildCount())
	for i := 0; i < count; i++ {
		child := n.NamedChild(i)
		// Look for an ERROR node whose text is "type"
		if c.nodeType(child) == "ERROR" && c.text(child) == "type" {
			// Next sibling should be an identifier (the type name)
			if i+1 >= count {
				continue
			}
			nameNode := n.NamedChild(i + 1)
			if c.nodeType(nameNode) != "identifier" {
				continue
			}
			name := c.text(nameNode)

			// The sibling after that should contain the struct_type
			if i+2 >= count {
				continue
			}
			typeNode := n.NamedChild(i + 2)
			structNode := c.findStructType(typeNode)
			if structNode != nil {
				fields := c.extractStructFields(structNode)
				c.env.RegisterStruct(name, &StructType{
					Name:       name,
					Fields:     fields,
					Comparable: true,
				})
			}
		}
	}
}

// findStructType searches a node and its children for a struct_type node.
func (c *collector) findStructType(n *gotreesitter.Node) *gotreesitter.Node {
	if c.nodeType(n) == "struct_type" {
		return n
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if found := c.findStructType(n.NamedChild(i)); found != nil {
			return found
		}
	}
	return nil
}

// collectFunction extracts function name, parameters, and return types.
//
// Grammar:
//
//	function_declaration: func name(parameters) result { body }
//	  - field "name": identifier
//	  - field "parameters": parameter_list
//	  - field "result": parameter_list | _simple_type | blank
func (c *collector) collectFunction(n *gotreesitter.Node) {
	nameNode := c.childByField(n, "name")
	if nameNode == nil {
		return
	}
	name := c.text(nameNode)

	params := c.extractParamTypes(c.childByField(n, "parameters"))
	results := c.extractResultTypes(c.childByField(n, "result"))

	c.env.RegisterFunc(name, &FuncType{
		Params:  params,
		Results: results,
	})
}

// extractParamTypes walks a parameter_list and extracts the types.
// Each child is a parameter_declaration with optional name(s) and a type field.
func (c *collector) extractParamTypes(paramList *gotreesitter.Node) []Type {
	if paramList == nil {
		return nil
	}
	var types []Type
	for i := 0; i < int(paramList.NamedChildCount()); i++ {
		child := paramList.NamedChild(i)
		nt := c.nodeType(child)
		switch nt {
		case "parameter_declaration":
			typeNode := c.childByField(child, "type")
			if typeNode == nil {
				continue
			}
			typeName := c.text(typeNode)
			// Count how many name identifiers are in this declaration.
			// Go allows "a, b int" which declares two params of the same type.
			nameCount := 0
			for j := 0; j < int(child.NamedChildCount()); j++ {
				gc := child.NamedChild(j)
				if c.nodeType(gc) == "identifier" {
					nameCount++
				}
			}
			if nameCount == 0 {
				// Unnamed parameter (just type): func(int, string)
				nameCount = 1
			}
			for k := 0; k < nameCount; k++ {
				types = append(types, parseTypeString(typeName))
			}
		case "variadic_parameter_declaration":
			typeNode := c.childByField(child, "type")
			if typeNode == nil {
				continue
			}
			typeName := c.text(typeNode)
			types = append(types, &SliceType{Elem: parseTypeString(typeName)})
		}
	}
	return types
}

// extractResultTypes extracts return types from the result field.
// The result can be a single type, a parameter_list (for multi-return), or nil.
func (c *collector) extractResultTypes(resultNode *gotreesitter.Node) []Type {
	if resultNode == nil {
		return nil
	}
	nt := c.nodeType(resultNode)
	switch nt {
	case "parameter_list":
		// Multi-return: (int, error)
		// Each child is a parameter_declaration or just a type
		var types []Type
		for i := 0; i < int(resultNode.NamedChildCount()); i++ {
			child := resultNode.NamedChild(i)
			childType := c.nodeType(child)
			if childType == "parameter_declaration" {
				typeNode := c.childByField(child, "type")
				if typeNode != nil {
					types = append(types, parseTypeString(c.text(typeNode)))
				}
			} else {
				// Bare type in result list
				types = append(types, parseTypeString(c.text(child)))
			}
		}
		return types
	default:
		// Single return type (e.g., "int", "string", "*Foo")
		return []Type{parseTypeString(c.text(resultNode))}
	}
}

// collectTypeDecl handles type declarations which may contain struct types.
//
// Grammar:
//
//	type_declaration: type (type_spec | type_alias | (...))
//	type_spec: name type_parameters type
func (c *collector) collectTypeDecl(n *gotreesitter.Node) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		nt := c.nodeType(child)
		if nt == "type_spec" {
			c.collectTypeSpec(child)
		}
	}
}

func (c *collector) collectTypeSpec(n *gotreesitter.Node) {
	nameNode := c.childByField(n, "name")
	typeNode := c.childByField(n, "type")
	if nameNode == nil || typeNode == nil {
		return
	}
	name := c.text(nameNode)

	if c.nodeType(typeNode) == "struct_type" {
		fields := c.extractStructFields(typeNode)
		c.env.RegisterStruct(name, &StructType{
			Name:       name,
			Fields:     fields,
			Comparable: true, // default; refined later by deeper analysis
		})
	}
	// Other type specs (interfaces, aliases) can be added later
}

// extractStructFields walks a struct_type's field_declaration_list.
func (c *collector) extractStructFields(structNode *gotreesitter.Node) map[string]Type {
	fields := make(map[string]Type)
	// struct_type -> "struct" field_declaration_list
	// field_declaration_list -> { field_declaration* }
	for i := 0; i < int(structNode.NamedChildCount()); i++ {
		child := structNode.NamedChild(i)
		if c.nodeType(child) == "field_declaration_list" {
			c.extractFieldsFromList(child, fields)
			break
		}
	}
	return fields
}

func (c *collector) extractFieldsFromList(listNode *gotreesitter.Node, fields map[string]Type) {
	for i := 0; i < int(listNode.NamedChildCount()); i++ {
		child := listNode.NamedChild(i)
		if c.nodeType(child) == "field_declaration" {
			c.extractField(child, fields)
		}
	}
}

func (c *collector) extractField(fieldNode *gotreesitter.Node, fields map[string]Type) {
	typeNode := c.childByField(fieldNode, "type")
	if typeNode == nil {
		return
	}
	typeName := c.text(typeNode)

	// Collect all field names (there can be multiple: "x, y float64")
	var names []string
	for i := 0; i < int(fieldNode.NamedChildCount()); i++ {
		child := fieldNode.NamedChild(i)
		if c.nodeType(child) == "field_identifier" {
			names = append(names, c.text(child))
		}
	}

	for _, name := range names {
		fields[name] = parseTypeString(typeName)
	}
}

// collectEnum extracts enum name and variant information.
//
// Grammar:
//
//	enum_declaration: enum name { variant, variant, ... }
//	enum_variant: name | name(type, type, ...)
func (c *collector) collectEnum(n *gotreesitter.Node) {
	nameNode := c.childByField(n, "name")
	if nameNode == nil {
		return
	}
	name := c.text(nameNode)

	variants := make(map[string][]Type)
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if c.nodeType(child) == "enum_variant" {
			vName, vTypes := c.extractVariant(child)
			if vName != "" {
				variants[vName] = vTypes
			}
		}
	}

	c.env.RegisterEnum(name, &EnumType{
		Name:     name,
		Variants: variants,
	})
}

func (c *collector) extractVariant(n *gotreesitter.Node) (string, []Type) {
	variantName := ""
	nameNode := c.childByField(n, "name")
	if nameNode != nil {
		variantName = c.text(nameNode)
	}

	// Payload types are any named children that are NOT the name identifier
	var types []Type
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if c.nodeType(child) != "identifier" {
			types = append(types, parseTypeString(c.text(child)))
		}
	}
	return variantName, types
}

// collectImport records import paths for later resolution.
//
// Grammar:
//
//	import_declaration: import (import_spec | import_spec_list)
//	import_spec: name? path
func (c *collector) collectImport(n *gotreesitter.Node) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		nt := c.nodeType(child)
		switch nt {
		case "import_spec":
			c.collectImportSpec(child)
		case "import_spec_list":
			// Walk children of the list
			for j := 0; j < int(child.NamedChildCount()); j++ {
				spec := child.NamedChild(j)
				if c.nodeType(spec) == "import_spec" {
					c.collectImportSpec(spec)
				}
			}
		}
	}
}

func (c *collector) collectImportSpec(n *gotreesitter.Node) {
	pathNode := c.childByField(n, "path")
	if pathNode == nil {
		return
	}
	// Path is a string literal like "fmt" -- strip quotes
	path := c.text(pathNode)
	path = strings.Trim(path, `"`)

	// Determine the local name: explicit alias or last path component
	alias := ""
	nameNode := c.childByField(n, "name")
	if nameNode != nil {
		alias = c.text(nameNode)
	} else {
		parts := strings.Split(path, "/")
		alias = parts[len(parts)-1]
	}

	c.env.imports[alias] = ImportScope{
		Funcs: make(map[string]*FuncType),
		Types: make(map[string]Type),
		Vars:  make(map[string]Type),
	}
}

// collectImpl extracts the receiver type and registers methods.
//
// Grammar:
//
//	impl_block: impl type { block }
func (c *collector) collectImpl(n *gotreesitter.Node) {
	typeNode := c.childByField(n, "type")
	if typeNode == nil {
		return
	}
	receiverType := c.text(typeNode)

	// Walk the block to find function_declarations
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if c.nodeType(child) == "block" {
			c.collectImplMethods(child, receiverType)
			break
		}
	}
}

func (c *collector) collectImplMethods(block *gotreesitter.Node, receiverType string) {
	// Inside impl blocks, the FW grammar parses functions as func_literal
	// inside expression_statement nodes. We need to walk the tree to find them.
	c.walkForImplMethods(block, receiverType)
}

// walkForImplMethods recursively finds func_literal nodes inside an impl block
// and registers them as methods.
func (c *collector) walkForImplMethods(n *gotreesitter.Node, receiverType string) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		nt := c.nodeType(child)

		if nt == "function_declaration" {
			// If the grammar does produce a function_declaration, handle it
			nameNode := c.childByField(child, "name")
			if nameNode == nil {
				continue
			}
			name := c.text(nameNode)
			params := c.extractParamTypes(c.childByField(child, "parameters"))
			results := c.extractResultTypes(c.childByField(child, "result"))
			c.env.RegisterFunc(receiverType+"."+name, &FuncType{
				Params:  params,
				Results: results,
			})
		} else if nt == "func_literal" {
			// Inside impl blocks, "func getX() float64 { ... }" parses as a
			// func_literal. The method name appears in the source text between
			// "func " and the parameter_list, but is not a named child.
			c.collectFuncLiteralAsMethod(child, receiverType)
		} else {
			// Recurse into statement_list, expression_statement, etc.
			c.walkForImplMethods(child, receiverType)
		}
	}
}

// collectFuncLiteralAsMethod extracts a method from a func_literal node
// found inside an impl block. The method name is between "func " and "("
// in the source text.
func (c *collector) collectFuncLiteralAsMethod(n *gotreesitter.Node, receiverType string) {
	text := c.text(n)
	if !strings.HasPrefix(text, "func ") {
		return
	}

	// Extract method name: text between "func " and first "("
	rest := text[5:] // skip "func "
	parenIdx := strings.Index(rest, "(")
	if parenIdx <= 0 {
		return
	}
	name := strings.TrimSpace(rest[:parenIdx])
	if name == "" {
		return
	}

	// Extract parameter types from parameter_list child
	var params []Type
	var results []Type
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		nt := c.nodeType(child)
		if nt == "parameter_list" {
			params = c.extractParamTypes(child)
		} else if nt == "type_identifier" || nt == "pointer_type" ||
			nt == "slice_type" || nt == "map_type" || nt == "qualified_type" {
			// Single return type appears as a type node after the parameter_list
			results = []Type{parseTypeString(c.text(child))}
		}
	}

	c.env.RegisterFunc(receiverType+"."+name, &FuncType{
		Params:  params,
		Results: results,
	})
}

// parseTypeString converts a type name string from the CST into a Type.
// Handles common patterns: primitives, pointers, slices, maps.
func parseTypeString(s string) Type {
	s = strings.TrimSpace(s)
	if s == "" {
		return Primitive("any")
	}

	// Pointer: *T
	if strings.HasPrefix(s, "*") {
		return &PointerType{Elem: parseTypeString(s[1:])}
	}

	// Slice: []T
	if strings.HasPrefix(s, "[]") {
		return &SliceType{Elem: parseTypeString(s[2:])}
	}

	// Map: map[K]V
	if strings.HasPrefix(s, "map[") {
		rest := s[4:]
		depth := 1
		idx := 0
		for idx < len(rest) && depth > 0 {
			if rest[idx] == '[' {
				depth++
			} else if rest[idx] == ']' {
				depth--
			}
			idx++
		}
		if depth == 0 {
			key := rest[:idx-1]
			val := rest[idx:]
			return &MapType{
				Key:   parseTypeString(key),
				Value: parseTypeString(val),
			}
		}
	}

	// Chan types
	if strings.HasPrefix(s, "chan<- ") {
		return &ChanType{Elem: parseTypeString(s[6:]), Dir: ChanSend}
	}
	if strings.HasPrefix(s, "<-chan ") {
		return &ChanType{Elem: parseTypeString(s[6:]), Dir: ChanRecv}
	}
	if strings.HasPrefix(s, "chan ") {
		return &ChanType{Elem: parseTypeString(s[4:]), Dir: ChanBidi}
	}

	// Everything else is a primitive or named type
	return Primitive(s)
}
