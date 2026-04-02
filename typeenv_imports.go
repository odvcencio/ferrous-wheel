package ferrouswheel

import (
	"fmt"
	"go/types"
	"os"
	"path/filepath"

	"golang.org/x/tools/go/packages"
)

// LoadImports loads type information for the given import paths.
// moduleDir is the directory containing go.mod (empty for stdlib-only).
func (e *TypeEnv) LoadImports(paths []string, moduleDir string) error {
	if len(paths) == 0 {
		return nil
	}

	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedName,
		Dir:  moduleDir,
	}

	pkgs, err := packages.Load(cfg, paths...)
	if err != nil {
		return fmt.Errorf("load packages: %w", err)
	}

	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return fmt.Errorf("package %s: %v", pkg.PkgPath, pkg.Errors[0])
		}
		if pkg.Types == nil {
			continue
		}
		scope := buildImportScope(pkg.Types)
		e.imports[pkg.Name] = scope
		if _, ok := e.importPathMap[pkg.Name]; !ok {
			e.importPathMap[pkg.Name] = pkg.PkgPath
		}
		for alias, path := range e.importPathMap {
			if path == pkg.PkgPath {
				if alias == pkg.Name {
					e.imports[alias] = scope
					continue
				}
				e.imports[alias] = importScopeWithAlias(scope, alias)
			}
		}
	}
	e.bindNamedTypes()
	return nil
}

func importScopeWithAlias(scope ImportScope, alias string) ImportScope {
	aliased := ImportScope{
		Funcs: make(map[string]*FuncType, len(scope.Funcs)),
		Types: make(map[string]Type, len(scope.Types)),
		Vars:  make(map[string]Type, len(scope.Vars)),
	}
	for name, fn := range scope.Funcs {
		aliased.Funcs[name] = importTypeWithAlias(fn, alias).(*FuncType)
	}
	for name, typ := range scope.Types {
		aliased.Types[name] = importTypeWithAlias(typ, alias)
	}
	for name, typ := range scope.Vars {
		aliased.Vars[name] = importTypeWithAlias(typ, alias)
	}
	return aliased
}

func importTypeWithAlias(typ Type, alias string) Type {
	switch t := typ.(type) {
	case nil, Primitive, *UntypedConstType, *UnresolvedType, *TypeParamType:
		return typ
	case *PointerType:
		return &PointerType{Elem: importTypeWithAlias(t.Elem, alias)}
	case *SliceType:
		return &SliceType{Elem: importTypeWithAlias(t.Elem, alias)}
	case *MapType:
		return &MapType{
			Key:   importTypeWithAlias(t.Key, alias),
			Value: importTypeWithAlias(t.Value, alias),
		}
	case *ChanType:
		return &ChanType{Elem: importTypeWithAlias(t.Elem, alias), Dir: t.Dir}
	case *FuncType:
		params := make([]Type, 0, len(t.Params))
		for _, param := range t.Params {
			params = append(params, importTypeWithAlias(param, alias))
		}
		results := make([]Type, 0, len(t.Results))
		for _, result := range t.Results {
			results = append(results, importTypeWithAlias(result, alias))
		}
		return &FuncType{Params: params, Results: results}
	case *TupleType:
		elems := make([]Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, importTypeWithAlias(elem, alias))
		}
		return &TupleType{Elems: elems}
	case *StructType:
		fields := make(map[string]Type, len(t.Fields))
		for name, fieldType := range t.Fields {
			fields[name] = importTypeWithAlias(fieldType, alias)
		}
		return &StructType{Name: t.Name, Fields: fields, Comparable: t.Comparable}
	case *InterfaceType:
		methods := make(map[string]*FuncType, len(t.Methods))
		for name, method := range t.Methods {
			methods[name] = importTypeWithAlias(method, alias).(*FuncType)
		}
		return &InterfaceType{Name: t.Name, Methods: methods}
	case *EnumType:
		variants := make(map[string][]Type, len(t.Variants))
		for name, payloads := range t.Variants {
			copied := make([]Type, 0, len(payloads))
			for _, payload := range payloads {
				copied = append(copied, importTypeWithAlias(payload, alias))
			}
			variants[name] = copied
		}
		return &EnumType{Name: t.Name, Variants: variants}
	case *GenericType:
		args := make([]Type, 0, len(t.TypeParams))
		for _, arg := range t.TypeParams {
			args = append(args, importTypeWithAlias(arg, alias))
		}
		return &GenericType{Name: t.Name, TypeParams: args}
	case *NamedType:
		pkg := t.Pkg
		if pkg != "" {
			pkg = alias
		}
		clone := &NamedType{Pkg: pkg, Name: t.Name}
		if t.Underlying != nil {
			clone.Underlying = importTypeWithAlias(t.Underlying, alias)
		}
		return clone
	default:
		return typ
	}
}

func buildImportScope(pkg *types.Package) ImportScope {
	scope := ImportScope{
		Funcs: make(map[string]*FuncType),
		Types: make(map[string]Type),
		Vars:  make(map[string]Type),
	}
	for _, name := range pkg.Scope().Names() {
		obj := pkg.Scope().Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.Func:
			sig := o.Type().(*types.Signature)
			scope.Funcs[name] = fromGoSignature(sig)
		case *types.TypeName:
			scope.Types[name] = fromGoType(o.Type())
		case *types.Var:
			scope.Vars[name] = fromGoType(o.Type())
		}
	}
	return scope
}

// LookupImportedFunc resolves a function from a loaded import.
func (e *TypeEnv) LookupImportedFunc(pkg, name string) (*FuncType, error) {
	imp, ok := e.imports[pkg]
	if !ok {
		return nil, fmt.Errorf("package %s not loaded", pkg)
	}
	fn, ok := imp.Funcs[name]
	if !ok {
		return nil, fmt.Errorf("%s.%s not found", pkg, name)
	}
	return fn, nil
}

// LookupImportedType resolves a type from a loaded import.
func (e *TypeEnv) LookupImportedType(pkg, name string) (Type, error) {
	imp, ok := e.imports[pkg]
	if !ok {
		return nil, fmt.Errorf("package %s not loaded", pkg)
	}
	typ, ok := imp.Types[name]
	if !ok {
		return nil, fmt.Errorf("%s.%s not found", pkg, name)
	}
	return typ, nil
}

// LookupImportedVar resolves a package-level variable or const from a loaded import.
func (e *TypeEnv) LookupImportedVar(pkg, name string) (Type, error) {
	imp, ok := e.imports[pkg]
	if !ok {
		return nil, fmt.Errorf("package %s not loaded", pkg)
	}
	typ, ok := imp.Vars[name]
	if !ok {
		return nil, fmt.Errorf("%s.%s not found", pkg, name)
	}
	return typ, nil
}

// fromGoType converts a go/types.Type to our Type interface.
// CRITICAL: Must check for *types.Named BEFORE calling .Underlying()
// to preserve named type info (e.g., os.File stays as NamedType).
func fromGoType(t types.Type) Type {
	return fromGoTypeWith(t, make(map[types.Type]bool))
}

func fromGoTypeWith(t types.Type, seen map[types.Type]bool) Type {
	if basic, ok := t.(*types.Basic); ok {
		return Primitive(basic.Name())
	}

	// Preserve named type information before unwrapping
	if named, ok := t.(*types.Named); ok {
		if seen[t] {
			pkg := ""
			if named.Obj().Pkg() != nil {
				pkg = named.Obj().Pkg().Name()
			}
			return &NamedType{Pkg: pkg, Name: named.Obj().Name()}
		}
		seen[t] = true
		pkg := ""
		if named.Obj().Pkg() != nil {
			pkg = named.Obj().Pkg().Name()
		}
		return &NamedType{Pkg: pkg, Name: named.Obj().Name(), Underlying: fromGoTypeWith(named.Underlying(), seen)}
	}
	switch t := t.(type) {
	case *types.Pointer:
		return &PointerType{Elem: fromGoTypeWith(t.Elem(), seen)}
	case *types.Slice:
		return &SliceType{Elem: fromGoTypeWith(t.Elem(), seen)}
	case *types.Map:
		return &MapType{Key: fromGoTypeWith(t.Key(), seen), Value: fromGoTypeWith(t.Elem(), seen)}
	case *types.Chan:
		dir := ChanBidi
		switch t.Dir() {
		case types.RecvOnly:
			dir = ChanRecv
		case types.SendOnly:
			dir = ChanSend
		}
		return &ChanType{Elem: fromGoTypeWith(t.Elem(), seen), Dir: dir}
	case *types.Signature:
		return fromGoSignatureWith(t, seen)
	case *types.Struct:
		fields := make(map[string]Type, t.NumFields())
		for i := range t.NumFields() {
			f := t.Field(i)
			fields[f.Name()] = fromGoTypeWith(f.Type(), seen)
		}
		return &StructType{Name: "", Fields: fields, Comparable: types.Comparable(t)}
	case *types.Interface:
		return &InterfaceType{Name: ""}
	default:
		return Primitive("any")
	}
}

// fromGoSignature converts a go/types.Signature to FuncType.
func fromGoSignature(sig *types.Signature) *FuncType {
	return fromGoSignatureWith(sig, make(map[types.Type]bool))
}

func fromGoSignatureWith(sig *types.Signature, seen map[types.Type]bool) *FuncType {
	params := make([]Type, 0, sig.Params().Len())
	for i := range sig.Params().Len() {
		params = append(params, fromGoTypeWith(sig.Params().At(i).Type(), seen))
	}
	results := make([]Type, 0, sig.Results().Len())
	for i := range sig.Results().Len() {
		results = append(results, fromGoTypeWith(sig.Results().At(i).Type(), seen))
	}
	return &FuncType{Params: params, Results: results}
}

// toGoType converts our Type to go/types.Type for interface satisfaction checks.
func toGoType(t Type, pkg *types.Package) types.Type {
	switch t := t.(type) {
	case Primitive:
		return types.Typ[basicKindFromName(string(t))]
	case *PointerType:
		return types.NewPointer(toGoType(t.Elem, pkg))
	case *SliceType:
		return types.NewSlice(toGoType(t.Elem, pkg))
	case *NamedType:
		return toGoType(t.Underlying, pkg)
	default:
		return types.Typ[types.Invalid]
	}
}

func basicKindFromName(name string) types.BasicKind {
	switch name {
	case "int":
		return types.Int
	case "int8":
		return types.Int8
	case "int16":
		return types.Int16
	case "int32":
		return types.Int32
	case "int64":
		return types.Int64
	case "uint":
		return types.Uint
	case "uint8":
		return types.Uint8
	case "uint16":
		return types.Uint16
	case "uint32":
		return types.Uint32
	case "uint64":
		return types.Uint64
	case "float32":
		return types.Float32
	case "float64":
		return types.Float64
	case "string":
		return types.String
	case "bool":
		return types.Bool
	case "byte":
		return types.Byte
	case "rune":
		return types.Rune
	case "uintptr":
		return types.Uintptr
	default:
		return types.Invalid
	}
}

// findModuleDir walks parent directories looking for go.mod.
// Returns empty string if no module found.
func findModuleDir(fwFilePath string) string {
	dir := filepath.Dir(fwFilePath)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// FindModuleDir walks parent directories looking for go.mod.
func FindModuleDir(fwFilePath string) string {
	return findModuleDir(fwFilePath)
}
