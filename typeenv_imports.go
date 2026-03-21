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
		e.imports[pkg.Name] = buildImportScope(pkg.Types)
	}
	return nil
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

// fromGoType converts a go/types.Type to our Type interface.
// CRITICAL: Must check for *types.Named BEFORE calling .Underlying()
// to preserve named type info (e.g., os.File stays as NamedType).
func fromGoType(t types.Type) Type {
	// Preserve named type information before unwrapping
	if named, ok := t.(*types.Named); ok {
		pkg := ""
		if named.Obj().Pkg() != nil {
			pkg = named.Obj().Pkg().Name()
		}
		return &NamedType{Pkg: pkg, Name: named.Obj().Name(), Underlying: fromGoType(named.Underlying())}
	}
	switch t := t.(type) {
	case *types.Basic:
		return Primitive(t.Name())
	case *types.Pointer:
		return &PointerType{Elem: fromGoType(t.Elem())}
	case *types.Slice:
		return &SliceType{Elem: fromGoType(t.Elem())}
	case *types.Map:
		return &MapType{Key: fromGoType(t.Key()), Value: fromGoType(t.Elem())}
	case *types.Chan:
		dir := ChanBidi
		switch t.Dir() {
		case types.RecvOnly:
			dir = ChanRecv
		case types.SendOnly:
			dir = ChanSend
		}
		return &ChanType{Elem: fromGoType(t.Elem()), Dir: dir}
	case *types.Signature:
		return fromGoSignature(t)
	case *types.Struct:
		fields := make(map[string]Type, t.NumFields())
		for i := range t.NumFields() {
			f := t.Field(i)
			fields[f.Name()] = fromGoType(f.Type())
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
	params := make([]Type, 0, sig.Params().Len())
	for i := range sig.Params().Len() {
		params = append(params, fromGoType(sig.Params().At(i).Type()))
	}
	results := make([]Type, 0, sig.Results().Len())
	for i := range sig.Results().Len() {
		results = append(results, fromGoType(sig.Results().At(i).Type()))
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
