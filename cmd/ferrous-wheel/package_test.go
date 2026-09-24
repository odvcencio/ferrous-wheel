package main

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func testSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func packageArgs(t *testing.T, output, source string) []string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	return []string{"ferrous-wheel", "package", "--compiler-sha256", testSHA256(t, executable),
		"--go-version", runtime.Version(), "--go-sha256", testSHA256(t, goBinary),
		"--target", "linux/amd64", "--out", output, source}
}

func TestPackageReproducibleStaticArtifactAndManifest(t *testing.T) {
	root := t.TempDir()
	source := []byte("package main\n\nfunc main() {}\n")
	if err := os.MkdirAll(filepath.Join(root, "first"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "second"), 0700); err != nil {
		t.Fatal(err)
	}
	firstSource := writeFWFile(t, filepath.Join(root, "first"), "program.fw", string(source))
	secondSource := writeFWFile(t, filepath.Join(root, "second"), "program.fw", string(source))
	firstOut, secondOut := filepath.Join(root, "first-package"), filepath.Join(root, "second-package")
	for i, pair := range [][2]string{{firstOut, firstSource}, {secondOut, secondSource}} {
		args := packageArgs(t, pair[0], pair[1])
		args = append(args[:len(args)-1], "--target", "linux/arm64", args[len(args)-1])
		if i == 1 {
			args[9], args[13] = args[13], args[9]
		}
		if code := runCLI(args, io.Discard); code != 0 {
			t.Fatalf("package %s exited %d", pair[1], code)
		}
	}
	for _, target := range []string{"amd64", "arm64"} {
		firstBinary := filepath.Join(firstOut, "program-linux-"+target)
		secondBinary := filepath.Join(secondOut, "program-linux-"+target)
		if testSHA256(t, firstBinary) != testSHA256(t, secondBinary) {
			t.Fatalf("%s binary changed with source directory", target)
		}
		file, err := elf.Open(firstBinary)
		if err != nil {
			t.Fatal(err)
		}
		for _, program := range file.Progs {
			if program.Type == elf.PT_INTERP {
				t.Fatalf("%s Linux artifact has a dynamic interpreter", target)
			}
		}
		file.Close()
	}
	firstManifest, err := os.ReadFile(filepath.Join(firstOut, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	secondManifest, err := os.ReadFile(filepath.Join(secondOut, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(firstManifest) != string(secondManifest) {
		t.Fatalf("manifest changed with source directory:\n%s\n%s", firstManifest, secondManifest)
	}
	var manifest struct {
		SchemaVersion  int    `json:"schemaVersion"`
		CompilerSHA256 string `json:"compilerSHA256"`
		GoVersion      string `json:"goVersion"`
		SourceSHA256   string `json:"sourceSHA256"`
		Artifacts      []struct {
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
			Static bool   `json:"static"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(firstManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	if manifest.SchemaVersion != 1 || manifest.CompilerSHA256 == "" || manifest.GoVersion != runtime.Version() ||
		manifest.SourceSHA256 != hex.EncodeToString(sum[:]) || len(manifest.Artifacts) != 2 ||
		manifest.Artifacts[0].File != "program-linux-amd64" || manifest.Artifacts[1].File != "program-linux-arm64" {
		t.Fatalf("incorrect manifest: %+v", manifest)
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.SHA256 != testSHA256(t, filepath.Join(firstOut, artifact.File)) || !artifact.Static {
			t.Fatalf("wrong artifact checksum or static flag: %+v", artifact)
		}
	}
}

func TestPackageRejectsPinsAndExistingOutput(t *testing.T) {
	root := t.TempDir()
	source := writeFWFile(t, root, "program.fw", "package main\nfunc main() {}\n")
	badCompilerOut := filepath.Join(root, "bad-compiler")
	wrongCompiler := packageArgs(t, badCompilerOut, source)
	wrongCompiler[3] = "0000000000000000000000000000000000000000000000000000000000000000"
	if code := runCLI(wrongCompiler, io.Discard); code == 0 {
		t.Fatal("accepted wrong compiler digest")
	}
	if _, err := os.Stat(badCompilerOut); !os.IsNotExist(err) {
		t.Fatalf("created output with wrong compiler pin: %v", err)
	}
	badGoOut := filepath.Join(root, "bad-go")
	wrongGo := packageArgs(t, badGoOut, source)
	wrongGo[5] = "go0.0.0"
	if code := runCLI(wrongGo, io.Discard); code == 0 {
		t.Fatal("accepted wrong Go version")
	}
	if _, err := os.Stat(badGoOut); !os.IsNotExist(err) {
		t.Fatalf("created output with wrong Go pin: %v", err)
	}
	badGoHashOut := filepath.Join(root, "bad-go-hash")
	wrongGoHash := packageArgs(t, badGoHashOut, source)
	wrongGoHash[7] = "0000000000000000000000000000000000000000000000000000000000000000"
	if code := runCLI(wrongGoHash, io.Discard); code == 0 {
		t.Fatal("accepted wrong Go executable digest")
	}
	if _, err := os.Stat(badGoHashOut); !os.IsNotExist(err) {
		t.Fatalf("created output with wrong Go executable pin: %v", err)
	}
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0700); err != nil {
		t.Fatal(err)
	}
	args := packageArgs(t, existing, source)
	if code := runCLI(args, io.Discard); code == 0 {
		t.Fatal("overwrote existing package directory")
	}
	if entries, err := os.ReadDir(existing); err != nil || len(entries) != 0 {
		t.Fatalf("changed existing output: entries=%v err=%v", entries, err)
	}
}

func TestPackageRecordsModuleHashAndCleansFailedBuild(t *testing.T) {
	root := t.TempDir()
	module := []byte("module example.com/packagefixture\n\ngo 1.25.0\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), module, 0600); err != nil {
		t.Fatal(err)
	}
	scripts := filepath.Join(root, "scripts")
	if err := os.Mkdir(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	source := writeFWFile(t, scripts, "program.fw", "package main\nfunc main() {}\n")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, source)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "package")
	if code := runCLI(packageArgs(t, output, relative), io.Discard); code != 0 {
		t.Fatalf("package relative source exited %d", code)
	}
	data, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ModuleSHA256 string `json:"moduleSHA256"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(module)
	if manifest.ModuleSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("wrong module hash: %q", manifest.ModuleSHA256)
	}
	bad := writeFWFile(t, scripts, "broken.fw", "package main\nfunc main() { missingName() }\n")
	badOutput := filepath.Join(root, "failed-package")
	if code := runCLI(packageArgs(t, badOutput, bad), io.Discard); code == 0 {
		t.Fatal("accepted Go compile failure")
	}
	if _, err := os.Lstat(badOutput); !os.IsNotExist(err) {
		t.Fatalf("failed build left output directory: %v", err)
	}
	stageRoot := filepath.Join(root, ".ferrous-wheel-build")
	if entries, err := os.ReadDir(stageRoot); err != nil || len(entries) != 0 {
		t.Fatalf("failed build left Go staging files: entries=%v err=%v", entries, err)
	}
}

func TestPackageModuleImportIsReproducibleAcrossSourceDirectories(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first-package"), filepath.Join(root, "second-package")
	for i, output := range []string{first, second} {
		moduleRoot := filepath.Join(root, []string{"first", "second"}[i])
		if err := os.MkdirAll(filepath.Join(moduleRoot, "helper"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(moduleRoot, "go.mod"), []byte("module example.com/packagefixture\n\ngo 1.25.0\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(moduleRoot, "helper", "helper.go"), []byte("package helper\nfunc Print() {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		scripts := filepath.Join(moduleRoot, "scripts")
		if err := os.MkdirAll(scripts, 0700); err != nil {
			t.Fatal(err)
		}
		source := writeFWFile(t, scripts, "program.fw", "package main\nimport \"example.com/packagefixture/helper\"\nfunc main() { helper.Print() }\n")
		if code := runCLI(packageArgs(t, output, source), io.Discard); code != 0 {
			t.Fatalf("package with local module import exited %d", code)
		}
	}
	for _, file := range []string{"program-linux-amd64", "manifest.json"} {
		if testSHA256(t, filepath.Join(first, file)) != testSHA256(t, filepath.Join(second, file)) {
			t.Fatalf("%s changed across source directories", file)
		}
	}
}
