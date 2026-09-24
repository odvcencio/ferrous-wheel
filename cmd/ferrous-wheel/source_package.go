package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	gobuild "go/build"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	ferrouswheel "m31labs.dev/ferrous-wheel"
)

const maxSourcePackageFiles = 4096

type fwPackageFile struct {
	path     string
	relative string
	source   []byte
}

type fwSourcePackage struct {
	dir        string
	moduleRoot string
	modulePath string
	files      []fwPackageFile
}

type sourceImport struct {
	path string
	file string
	line int
}

// discoverFWPackage follows local imports from the input directory. Each
// directory is one Go package. Files and imports are visited in name order.
func discoverFWPackage(inputDir string, target gobuild.Context) (*fwSourcePackage, error) {
	abs, err := filepath.Abs(inputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve package directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("inspect package directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("package input is not a directory: %s", inputDir)
	}
	pkg := &fwSourcePackage{dir: abs}
	pkg.moduleRoot = findParentGoMod(pkg.dir)
	if pkg.moduleRoot != "" {
		data, err := os.ReadFile(filepath.Join(pkg.moduleRoot, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("read go.mod: %w", err)
		}
		pkg.modulePath = modfile.ModulePath(data)
		if pkg.modulePath == "" {
			return nil, fmt.Errorf("go.mod has no module path: %s", pkg.moduleRoot)
		}
	} else {
		pkg.moduleRoot = pkg.dir
	}
	realRoot, err := filepath.EvalSymlinks(pkg.moduleRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve module root: %w", err)
	}
	state := make(map[string]uint8)
	var stack []string
	var visit func(string) error
	visit = func(dir string) error {
		if state[dir] == 2 {
			return nil
		}
		if len(state) >= maxSourcePackageFiles {
			return fmt.Errorf("package graph exceeds %d directories", maxSourcePackageFiles)
		}
		state[dir] = 1
		stack = append(stack, dir)
		defer func() {
			stack = stack[:len(stack)-1]
			state[dir] = 2
		}()

		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return fmt.Errorf("resolve package directory %s: %w", dir, err)
		}
		if !pathWithin(realRoot, realDir) {
			return fmt.Errorf("package directory escapes module root: %s", dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read package directory %s: %w", dir, err)
		}
		var imports []sourceImport
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, "_test.fw") {
				continue
			}
			isFW := strings.HasSuffix(name, ".fw")
			if !isFW && !strings.HasSuffix(name, ".go") {
				continue
			}
			if isFW && entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("package source is a symlink: %s", filepath.Join(dir, name))
			}
			if !isFW {
				matched, err := target.MatchFile(dir, name)
				if err != nil {
					return fmt.Errorf("match Go source %s: %w", name, err)
				}
				if !matched {
					continue
				}
			}
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read source %s: %w", path, err)
			}
			if isFW {
				if len(pkg.files) == maxSourcePackageFiles {
					return fmt.Errorf("package graph exceeds %d .fw files", maxSourcePackageFiles)
				}
				relative, err := filepath.Rel(pkg.moduleRoot, path)
				if err != nil {
					return fmt.Errorf("resolve source path %s: %w", path, err)
				}
				pkg.files = append(pkg.files, fwPackageFile{path: path, relative: filepath.ToSlash(relative), source: data})
			}
			found, err := sourceImports(path, data)
			if err != nil {
				return err
			}
			imports = append(imports, found...)
		}
		sort.Slice(imports, func(i, j int) bool {
			if imports[i].path == imports[j].path {
				if imports[i].file == imports[j].file {
					return imports[i].line < imports[j].line
				}
				return imports[i].file < imports[j].file
			}
			return imports[i].path < imports[j].path
		})
		for _, imp := range imports {
			child, ok := pkg.localImportDir(imp.path)
			if !ok {
				continue
			}
			if state[child] == 1 {
				start := 0
				for stack[start] != child {
					start++
				}
				cycle := make([]string, 0, len(stack)-start+1)
				for _, item := range append(append([]string(nil), stack[start:]...), child) {
					cycle = append(cycle, pkg.importName(item))
				}
				rel, _ := filepath.Rel(pkg.moduleRoot, imp.file)
				return fmt.Errorf("%s:%d: import cycle: %s", filepath.ToSlash(rel), imp.line, strings.Join(cycle, " -> "))
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(pkg.dir); err != nil {
		return nil, err
	}
	sort.Slice(pkg.files, func(i, j int) bool { return pkg.files[i].relative < pkg.files[j].relative })
	foundEntryFile := false
	for _, file := range pkg.files {
		if filepath.Dir(file.path) == pkg.dir {
			foundEntryFile = true
			break
		}
	}
	if !foundEntryFile {
		return nil, fmt.Errorf("package directory has no .fw files: %s", inputDir)
	}
	return pkg, nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (pkg *fwSourcePackage) localImportDir(importPath string) (string, bool) {
	if pkg.modulePath == "" {
		return "", false
	}
	if importPath == pkg.modulePath {
		return pkg.moduleRoot, true
	}
	if !strings.HasPrefix(importPath, pkg.modulePath+"/") {
		return "", false
	}
	rel := strings.TrimPrefix(importPath, pkg.modulePath+"/")
	if rel == "" || filepath.IsAbs(filepath.FromSlash(rel)) || !pathWithin(".", filepath.FromSlash(rel)) {
		return "", false
	}
	return filepath.Join(pkg.moduleRoot, filepath.FromSlash(rel)), true
}

func (pkg *fwSourcePackage) importName(dir string) string {
	if pkg.modulePath == "" {
		return dir
	}
	rel, err := filepath.Rel(pkg.moduleRoot, dir)
	if err != nil || rel == "." {
		return pkg.modulePath
	}
	return pkg.modulePath + "/" + filepath.ToSlash(rel)
}

func sourceImports(path string, source []byte) ([]sourceImport, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("read imports in %s: %w", path, err)
	}
	imports := make([]sourceImport, 0, len(file.Imports))
	for _, item := range file.Imports {
		importPath, err := strconv.Unquote(item.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid import path: %w", fset.Position(item.Pos()), err)
		}
		imports = append(imports, sourceImport{path: importPath, file: path, line: fset.Position(item.Pos()).Line})
	}
	return imports, nil
}

type stagedFWPackage struct {
	*fwSourcePackage
	stageDir    string
	buildDir    string
	overlayPath string
	cleanup     func()
}

func stageCLIInput(path string, target gobuild.Context) (*stagedFWPackage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect source: %w", err)
	}
	if info.IsDir() {
		return stageFWPackage(path, target)
	}
	goCode, warnings, err := transpileFile(path)
	if err != nil {
		return nil, err
	}
	printWarnings(os.Stderr, warnings)
	tmpDir, cleanup, err := writeTempProject(goCode, path)
	if err != nil {
		return nil, err
	}
	return &stagedFWPackage{stageDir: tmpDir, buildDir: tmpDir, cleanup: cleanup}, nil
}

// stageFWPackage stores generated Go outside the source tree. The Go overlay
// makes files appear beside their .fw sources during package compilation.
func stageFWPackage(inputDir string, target gobuild.Context) (_ *stagedFWPackage, returnErr error) {
	pkg, err := discoverFWPackage(inputDir, target)
	if err != nil {
		return nil, err
	}
	stageRoot := ""
	if pkg.modulePath != "" {
		stageRoot = filepath.Join(pkg.moduleRoot, ".ferrous-wheel-build")
		if err := os.MkdirAll(stageRoot, 0o755); err != nil {
			return nil, fmt.Errorf("create stage root: %w", err)
		}
	}
	stageDir, err := os.MkdirTemp(stageRoot, "fwrun-*")
	if err != nil {
		return nil, fmt.Errorf("create stage directory: %w", err)
	}
	staged := &stagedFWPackage{fwSourcePackage: pkg, stageDir: stageDir, buildDir: pkg.dir}
	staged.cleanup = func() {
		if err := os.RemoveAll(stageDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: remove staging directory: %v\n", err)
		}
	}
	defer func() {
		if returnErr != nil {
			staged.cleanup()
		}
	}()
	replacements := make(map[string]string, len(pkg.files))
	backingDir := stageDir
	if pkg.modulePath == "" {
		staged.buildDir = stageDir
		backingDir = filepath.Join(stageDir, ".backing")
		if err := os.Mkdir(backingDir, 0o700); err != nil {
			return nil, fmt.Errorf("create generated source directory: %w", err)
		}
		if err := os.WriteFile(filepath.Join(stageDir, "go.mod"), []byte("module fwrun\n\ngo 1.25.0\n"), 0o644); err != nil {
			return nil, fmt.Errorf("write temporary go.mod: %w", err)
		}
		if err := overlayStandaloneAssets(pkg.dir, stageDir, replacements); err != nil {
			return nil, err
		}
	}
	resultDefined, optionDefined := false, false
	for i, file := range pkg.files {
		virtual := file.path + ".go"
		if pkg.modulePath == "" {
			virtual = filepath.Join(stageDir, filepath.Base(file.path)+".go")
		}
		if _, err := os.Lstat(virtual); err == nil {
			return nil, fmt.Errorf("%s: generated Go path already exists: %s", file.path, virtual)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect generated Go path %s: %w", virtual, err)
		}
		goCode, warnings, err := transpileSourceWithOptions(file.path, file.source, ferrouswheel.TranspileOptions{
			OmitResultType: resultDefined,
			OmitOptionType: optionDefined,
		})
		if err != nil {
			return nil, err
		}
		result, option, err := packageGenericTypes(goCode)
		if err != nil {
			return nil, fmt.Errorf("inspect generated Go for %s: %w", file.path, err)
		}
		resultDefined = resultDefined || result
		optionDefined = optionDefined || option
		goCode = namespaceFWHelpers(goCode, i)
		printWarnings(os.Stderr, warnings)
		backing := filepath.Join(backingDir, fmt.Sprintf("source-%04d.go", i))
		if err := os.WriteFile(backing, []byte(goCode), 0o644); err != nil {
			return nil, fmt.Errorf("write generated Go for %s: %w", file.path, err)
		}
		replacements[virtual] = backing
	}
	data, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replacements})
	if err != nil {
		return nil, fmt.Errorf("encode Go overlay: %w", err)
	}
	staged.overlayPath = filepath.Join(stageDir, "overlay.json")
	if err := os.WriteFile(staged.overlayPath, data, 0o600); err != nil {
		return nil, fmt.Errorf("write Go overlay: %w", err)
	}
	return staged, nil
}

func packageGenericTypes(goCode string) (result, option bool, err error) {
	file, err := parser.ParseFile(token.NewFileSet(), "generated.go", goCode, parser.SkipObjectResolution)
	if err != nil {
		return false, false, err
	}
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range group.Specs {
			typ, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			switch typ.Name.Name {
			case "Result":
				result = true
			case "Option":
				option = true
			}
		}
	}
	return result, option, nil
}

func namespaceFWHelpers(goCode string, fileIndex int) string {
	source := []byte(goCode)
	fset := token.NewFileSet()
	file := fset.AddFile("generated.go", -1, len(source))
	var scan scanner.Scanner
	scan.Init(file, source, nil, 0)
	prefix := fmt.Sprintf("_fwFile%04d", fileIndex)
	var result strings.Builder
	last := 0
	for {
		pos, kind, name := scan.Scan()
		if kind == token.EOF {
			break
		}
		if kind != token.IDENT || !strings.HasPrefix(name, "_fw") {
			continue
		}
		start := file.Offset(pos)
		result.Write(source[last:start])
		result.WriteString(prefix)
		result.WriteString(name[len("_fw"):])
		last = start + len(name)
	}
	result.Write(source[last:])
	return result.String()
}

func overlayStandaloneAssets(sourceDir, stageDir string, replacements map[string]string) error {
	count := 0
	return filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourceDir {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasPrefix(name, ".") || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || strings.HasSuffix(name, ".fw") {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		count++
		if count > maxSourcePackageFiles {
			return fmt.Errorf("standalone package exceeds %d adjacent files", maxSourcePackageFiles)
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		replacements[filepath.Join(stageDir, rel)] = path
		return nil
	})
}

func sourceManifest(sourceHashes map[string]string) ([]packageSourceFile, string) {
	paths := make([]string, 0, len(sourceHashes))
	for path := range sourceHashes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make([]packageSourceFile, 0, len(paths))
	hash := sha256.New()
	for _, path := range paths {
		digest := sourceHashes[path]
		files = append(files, packageSourceFile{Path: path, SHA256: digest})
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write([]byte(digest))
	}
	return files, hex.EncodeToString(hash.Sum(nil))
}
