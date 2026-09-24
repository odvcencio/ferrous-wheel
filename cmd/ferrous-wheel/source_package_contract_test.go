package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeContractFile(t *testing.T, root, name, contents string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func multiFileContract(t *testing.T) (root, source, appDir string) {
	t.Helper()
	root = t.TempDir()
	writeContractFile(t, root, "go.mod", "module example.com/fwcontract\n\ngo 1.25.0\n")
	source = writeContractFile(t, root, "app/main.fw", `package main
import (
	"embed"
	"fmt"
	"os"
	"example.com/fwcontract/lib"
)
//go:embed message.txt
var embedded embed.FS
func main() {
	asset, err := embedded.ReadFile("message.txt")
	if err != nil { panic(err) }
	runtimeAsset, err := os.ReadFile("runtime.txt")
	if err != nil { panic(err) }
	fmt.Printf("%s|%s|%s|%s\n", lib.Message(), helper(), string(asset), string(runtimeAsset))
}
`)
	writeContractFile(t, root, "app/helpers.fw", "package main\nfunc helper() string { let word = true ? \"main\" : \"other\"; return word + \"-helper\" }\n")
	writeContractFile(t, root, "app/message.txt", "embedded-asset")
	writeContractFile(t, root, "app/runtime.txt", "runtime-asset")
	writeContractFile(t, root, "lib/lib.fw", "package lib\nfunc Message() string { let word = part(); return word + \"-library\" }\n")
	writeContractFile(t, root, "lib/helpers.fw", "package lib\nfunc part() string { return \"shared\" }\n")
	return root, source, filepath.Join(root, "app")
}

func TestMultiFilePackageRunBuildAndAssets(t *testing.T) {
	root, _, appDir := multiFileContract(t)
	want := "shared-library|main-helper|embedded-asset|runtime-asset\n"
	out, stderr, err := captureOutput(t, func() error { return runScript(appDir, appDir, nil) })
	if err != nil || out != want {
		t.Fatalf("run package: output %q, stderr %q, error %v", out, stderr, err)
	}
	bin := filepath.Join(t.TempDir(), "multi")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	_, stderr, err = captureOutput(t, func() error { return build(appDir, bin) })
	if err != nil {
		t.Fatalf("build package: %v\n%s", err, stderr)
	}
	cmd := exec.Command(bin)
	cmd.Dir = appDir
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != want {
		t.Fatalf("built package: output %q, error %v", output, err)
	}
	stages, err := filepath.Glob(filepath.Join(root, ".ferrous-wheel-build*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("staging after run/build: %v, %v", stages, err)
	}
}

func TestDirectoryModeUsesNearestModuleDespiteCallerWorkspace(t *testing.T) {
	root, _, appDir := multiFileContract(t)
	t.Setenv("GOWORK", filepath.Join(root, "missing.work"))
	output, stderr, err := captureOutput(t, func() error { return runScript(appDir, appDir, nil) })
	if err != nil || output != "shared-library|main-helper|embedded-asset|runtime-asset\n" {
		t.Fatalf("directory run with caller workspace: output %q, stderr %q, error %v", output, stderr, err)
	}
	bin := filepath.Join(t.TempDir(), "tool")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	_, stderr, err = captureOutput(t, func() error { return build(appDir, bin) })
	if err != nil {
		t.Fatalf("directory build with caller workspace: stderr %q, error %v", stderr, err)
	}
}

func TestMultiFilePackageManifestHashesAllSources(t *testing.T) {
	if (runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows") ||
		(runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("package targets support Linux, macOS, and Windows")
	}
	root, _, appDir := multiFileContract(t)
	outputDir := filepath.Join(t.TempDir(), "package")
	args := packageArgs(t, outputDir, appDir)
	args[9] = runtime.GOOS + "/" + runtime.GOARCH
	if code := runCLI(args, os.Stderr); code != 0 {
		t.Fatalf("package exited %d", code)
	}
	data, err := os.ReadFile(filepath.Join(outputDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SourceFiles []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"sourceFiles"`
		PackageSHA256 string `json:"packageSHA256"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.SourceFiles) != 4 || manifest.PackageSHA256 == "" {
		t.Fatalf("manifest omitted package sources: %s", data)
	}
	wantPaths := []string{"app/helpers.fw", "app/main.fw", "lib/helpers.fw", "lib/lib.fw"}
	for i, file := range manifest.SourceFiles {
		if file.Path != wantPaths[i] {
			t.Fatalf("source order = %q, want %q", file.Path, wantPaths[i])
		}
		if filepath.IsAbs(file.Path) || file.SHA256 != testSHA256(t, filepath.Join(root, filepath.FromSlash(file.Path))) {
			t.Fatalf("unstable source manifest entry: %+v", file)
		}
	}
	name := "app-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	cmd := exec.Command(filepath.Join(outputDir, name))
	cmd.Dir = appDir
	programOutput, err := cmd.CombinedOutput()
	if err != nil || string(programOutput) != "shared-library|main-helper|embedded-asset|runtime-asset\n" {
		t.Fatalf("packaged program: output %q, error %v", programOutput, err)
	}
	_, _, secondApp := multiFileContract(t)
	secondOutput := filepath.Join(t.TempDir(), "package")
	secondArgs := packageArgs(t, secondOutput, secondApp)
	secondArgs[9] = runtime.GOOS + "/" + runtime.GOARCH
	if code := runCLI(secondArgs, os.Stderr); code != 0 {
		t.Fatalf("second package exited %d", code)
	}
	for _, file := range []string{name, "manifest.json"} {
		if testSHA256(t, filepath.Join(outputDir, file)) != testSHA256(t, filepath.Join(secondOutput, file)) {
			t.Fatalf("%s changed with source directory", file)
		}
	}
	writeContractFile(t, filepath.Dir(secondApp), "lib/helpers.fw", "package lib\nfunc part() string { return \"changed\" }\n")
	changedOutput := filepath.Join(t.TempDir(), "package")
	changedArgs := packageArgs(t, changedOutput, secondApp)
	changedArgs[9] = runtime.GOOS + "/" + runtime.GOARCH
	if code := runCLI(changedArgs, os.Stderr); code != 0 {
		t.Fatalf("changed package exited %d", code)
	}
	changedData, err := os.ReadFile(filepath.Join(changedOutput, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var changed struct {
		PackageSHA256 string `json:"packageSHA256"`
	}
	if err := json.Unmarshal(changedData, &changed); err != nil {
		t.Fatal(err)
	}
	if changed.PackageSHA256 == manifest.PackageSHA256 ||
		testSHA256(t, filepath.Join(changedOutput, name)) == testSHA256(t, filepath.Join(secondOutput, name)) {
		t.Fatal("imported .fw helper changed without changing the package hash and binary")
	}
}

func TestMultiFileImportCycleReportsSourceLine(t *testing.T) {
	root := t.TempDir()
	writeContractFile(t, root, "go.mod", "module example.com/fwcycle\n\ngo 1.25.0\n")
	writeContractFile(t, root, "a/main.fw", "package a\nimport \"example.com/fwcycle/b\"\nfunc A() { b.B() }\n")
	writeContractFile(t, root, "b/main.fw", "package b\nimport \"example.com/fwcycle/a\"\nfunc B() { a.A() }\n")
	_, stderr, err := captureOutput(t, func() error { return build(filepath.Join(root, "a"), filepath.Join(root, "out")) })
	if err == nil || !strings.Contains(err.Error(), "import cycle") || !strings.Contains(err.Error(), "main.fw:2") {
		t.Fatalf("cycle diagnostic = %v, stderr = %q", err, stderr)
	}
}

func TestSingleFileBuildDoesNotIncludeSiblingScripts(t *testing.T) {
	root := t.TempDir()
	writeContractFile(t, root, "go.mod", "module example.com/singlescript\n\ngo 1.25.0\n")
	first := writeContractFile(t, root, "first.fw", "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"first\") }\n")
	writeContractFile(t, root, "second.fw", "package main\nfunc main() { panic(\"second\") }\n")
	output, stderr, err := captureOutput(t, func() error { return run(first) })
	if err != nil || output != "first\n" {
		t.Fatalf("single-file run = %q, stderr %q, error %v", output, stderr, err)
	}
}

func TestDirectoryModeRejectsGeneratedGoCollision(t *testing.T) {
	root := t.TempDir()
	writeContractFile(t, root, "go.mod", "module example.com/collision\n\ngo 1.25.0\n")
	writeContractFile(t, root, "app/main.fw", "package main\nfunc main() {}\n")
	writeContractFile(t, root, "app/main.fw.go", "package main\nfunc main() {}\n")
	_, stderr, err := captureOutput(t, func() error { return build(filepath.Join(root, "app"), filepath.Join(root, "out")) })
	if err == nil || !strings.Contains(err.Error(), "main.fw") || !strings.Contains(err.Error(), "main.fw.go") || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("collision diagnostic = %v, stderr = %q", err, stderr)
	}
	stages, err := filepath.Glob(filepath.Join(root, ".ferrous-wheel-build*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("staging after rejected package = %v, %v", stages, err)
	}
}

func TestDirectoryModeWithoutGoModEmbedsAdjacentAsset(t *testing.T) {
	root := t.TempDir()
	writeContractFile(t, root, "main.fw", `package main
import ("embed"; "fmt")
//go:embed message.txt
var embedded embed.FS
func main() {
	data, err := embedded.ReadFile("message.txt")
	if err != nil { panic(err) }
	fmt.Print(helper(), string(data))
}
`)
	writeContractFile(t, root, "helper.fw", "package main\nfunc helper() string { return \"standalone:\" }\n")
	writeContractFile(t, root, "message.txt", "adjacent")
	output, stderr, err := captureOutput(t, func() error { return runScript(root, "", nil) })
	if err != nil || output != "standalone:adjacent" {
		t.Fatalf("standalone package: output %q, stderr %q, error %v", output, stderr, err)
	}
}

func TestDirectoryModeSharesGeneratedSupportAcrossFiles(t *testing.T) {
	root := t.TempDir()
	writeContractFile(t, root, "go.mod", "module example.com/generatedsupport\n\ngo 1.25.0\n")
	appDir := filepath.Join(root, "app")
	writeContractFile(t, root, "app/main.fw", `package main
import "fmt"
func first() Result[int] {
	breaker "first" { _ = 1 }
	return Ok[int](3)
}
func main() { fmt.Println(first().Unwrap() + second().Unwrap()) }
`)
	writeContractFile(t, root, "app/second.fw", `package main
func second() Result[int] {
	breaker "second" { _ = 2 }
	return Ok[int](4)
}
`)
	output, stderr, err := captureOutput(t, func() error { return runScript(appDir, "", nil) })
	if err != nil || output != "7\n" {
		t.Fatalf("generated support across files: output %q, stderr %q, error %v", output, stderr, err)
	}
}

func TestHelperNamespacingPreservesStringsAndComments(t *testing.T) {
	source := "package main\n// _fwWorker is a comment\nvar _fwWorker = \"_fwWorker\"\nfunc main() { println(_fwWorker) }\n"
	got := namespaceFWHelpers(source, 2)
	if !strings.Contains(got, "var _fwFile0002Worker = \"_fwWorker\"") ||
		!strings.Contains(got, "println(_fwFile0002Worker)") ||
		!strings.Contains(got, "// _fwWorker is a comment") {
		t.Fatalf("namespacing changed a literal or missed a helper: %s", got)
	}
}
