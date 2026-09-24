package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writePolicyFixture(t *testing.T) (root, app, policyPath string) {
	t.Helper()
	root = t.TempDir()
	writeContractFile(t, root, "go.mod", "module example.com/policyfixture\n\ngo 1.25.0\n")
	writeContractFile(t, root, "app/main.fw", "package main\nimport \"example.com/policyfixture/lib\"\nfunc main() { lib.Run() }\n")
	writeContractFile(t, root, "lib/worker.fw", "package lib\nimport e \"os/exec\"\nfunc Run() { e.Command(\"true\") }\n")
	policyPath = writeContractFile(t, root, "agent-policy.json", `{"denyProcess":true}`)
	return root, filepath.Join(root, "app"), policyPath
}

func TestPolicyDeniesReachableSourceBeforeRunBuildPackage(t *testing.T) {
	root, app, policyPath := writePolicyFixture(t)
	buildOutput := filepath.Join(root, "build-output")
	packageOutput := filepath.Join(root, "package-output")
	zeroHash := strings.Repeat("0", 64)
	commands := []struct {
		name string
		args []string
	}{
		{"run", []string{"ferrous-wheel", "run", "--policy", policyPath, app}},
		{"build", []string{"ferrous-wheel", "build", "--policy", policyPath, app, "-o", buildOutput}},
		{"package", []string{"ferrous-wheel", "package", "--compiler-sha256", zeroHash,
			"--go-version", "go1.25.1", "--go-sha256", zeroHash, "--target", "linux/amd64",
			"--out", packageOutput, "--policy", policyPath, app}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := runCLI(command.args, &stderr); code != 1 {
				t.Fatalf("denied command exited %d: %s", code, stderr.String())
			}
			if !strings.HasPrefix(stderr.String(), "lib/worker.fw:3:") ||
				!strings.Contains(stderr.String(), "process call e.Command is denied") {
				t.Fatalf("missing source diagnostic: %q", stderr.String())
			}
			if _, err := os.Lstat(filepath.Join(root, ".ferrous-wheel-build")); !os.IsNotExist(err) {
				t.Fatalf("policy denial created staging: %v", err)
			}
			for _, output := range []string{buildOutput, packageOutput} {
				if _, err := os.Lstat(output); !os.IsNotExist(err) {
					t.Fatalf("policy denial created %s: %v", output, err)
				}
			}
		})
	}
}

func TestPolicyKeepsSingleFileInputAndRunArguments(t *testing.T) {
	root := t.TempDir()
	writeContractFile(t, root, "go.mod", "module example.com/singlepolicy\n\ngo 1.25.0\n")
	source := writeContractFile(t, root, "safe.fw", "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Printf(\"%s|%s\", os.Args[1], os.Args[2]) }\n")
	writeContractFile(t, root, "blocked.fw", "package main\nimport \"os/exec\"\nfunc main() { exec.Command(\"true\") }\n")
	policyPath := writeContractFile(t, root, "policy.json", `{"denyProcess":true}`)
	var code int
	stdout, stderr, err := captureOutput(t, func() error {
		code = runCLI([]string{"ferrous-wheel", "run", "--policy", policyPath, source,
			"--", "two words", "--policy"}, os.Stderr)
		return nil
	})
	if err != nil || code != 0 || stdout != "two words|--policy" {
		t.Fatalf("single-file run: code=%d stdout=%q stderr=%q err=%v", code, stdout, stderr, err)
	}
	output := filepath.Join(root, "safe-bin")
	if runtime.GOOS == "windows" {
		output += ".exe"
	}
	var buildStderr bytes.Buffer
	if code := runCLI([]string{"ferrous-wheel", "build", source, "--policy", policyPath, "-o", output}, &buildStderr); code != 0 {
		t.Fatalf("single-file build exited %d: %s", code, buildStderr.String())
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("single-file build omitted binary: %v", err)
	}
}

func TestPackagePolicyHashInManifest(t *testing.T) {
	root := t.TempDir()
	source := writeFWFile(t, root, "program.fw", "package main\nfunc main() {}\n")
	policyData := []byte(`{"denyProcess":true}`)
	policyPath := filepath.Join(root, "policy.json")
	if err := os.WriteFile(policyPath, policyData, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "package")
	args := packageArgs(t, output, source)
	args = append(args[:len(args)-1], "--policy", policyPath, args[len(args)-1])
	var stderr bytes.Buffer
	if code := runCLI(args, &stderr); code != 0 {
		t.Fatalf("package exited %d: %s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		PolicySHA256 string `json:"policySHA256"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PolicySHA256 != testSHA256(t, policyPath) {
		t.Fatalf("manifest omitted policy hash: %s", data)
	}
}

func TestDirectoryPackageRecordsPolicyHash(t *testing.T) {
	if (runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows") ||
		(runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("package target is unsupported")
	}
	root, app, policyPath := writePolicyFixture(t)
	writeContractFile(t, root, "lib/worker.fw", "package lib\nfunc Run() {}\n")
	output := filepath.Join(root, "package-output")
	args := packageArgs(t, output, app)
	args[9] = runtime.GOOS + "/" + runtime.GOARCH
	args = append(args[:len(args)-1], "--policy", policyPath, args[len(args)-1])
	var stderr bytes.Buffer
	if code := runCLI(args, &stderr); code != 0 {
		t.Fatalf("directory package exited %d: %s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SchemaVersion int    `json:"schemaVersion"`
		PolicySHA256  string `json:"policySHA256"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 2 || manifest.PolicySHA256 != testSHA256(t, policyPath) {
		t.Fatalf("directory manifest omitted policy hash: %s", data)
	}
}

func TestPolicyConfigAndCrossTargetRootsFailBeforeOutput(t *testing.T) {
	root := t.TempDir()
	source := writeContractFile(t, root, "main.fw", "package main\nfunc main() {}\n")
	invalid := writeContractFile(t, root, "invalid.json", `{"denyProcess":true,"denyProcess":false}`)
	output := filepath.Join(root, "bad-build")
	var stderr bytes.Buffer
	if code := runCLI([]string{"ferrous-wheel", "build", "--policy", invalid, source, "-o", output}, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "duplicate rule") {
		t.Fatalf("invalid policy: code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("invalid policy created output: %v", err)
	}
	rootPolicy := writeContractFile(t, root, "roots.json", fmt.Sprintf(`{"fileRoots":[%q]}`, root))
	target := "windows/amd64"
	if runtime.GOOS == "windows" {
		target = "linux/amd64"
	}
	packageOutput := filepath.Join(root, "cross-package")
	zeroHash := strings.Repeat("0", 64)
	args := []string{"ferrous-wheel", "package", "--compiler-sha256", zeroHash, "--go-version", "go1.25.1",
		"--go-sha256", zeroHash, "--target", target, "--out", packageOutput, "--policy", rootPolicy, source}
	stderr.Reset()
	if code := runCLI(args, &stderr); code != 1 || !strings.Contains(stderr.String(), "cross-target file roots") {
		t.Fatalf("cross-target roots: code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Lstat(packageOutput); !os.IsNotExist(err) {
		t.Fatalf("cross-target policy created output: %v", err)
	}
}

func TestPolicyFlagCannotSilentlyOverrideAnEarlierPolicy(t *testing.T) {
	for _, command := range []string{"run", "build", "package"} {
		t.Run(command, func(t *testing.T) {
			var stderr bytes.Buffer
			args := []string{"ferrous-wheel", command, "--policy", "strict.json", "--policy", "loose.json", "script.fw"}
			if code := runCLI(args, &stderr); code != 1 || !strings.Contains(stderr.String(), "duplicate --policy") {
				t.Fatalf("duplicate policy: code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestPackagePolicyChecksSourcesReachableOnlyOnSecondTarget(t *testing.T) {
	root := t.TempDir()
	writeContractFile(t, root, "go.mod", "module example.com/targetpolicy\n\ngo 1.25.0\n")
	app := filepath.Join(root, "app")
	writeContractFile(t, root, "app/main.fw", "package main\nfunc main() {}\n")
	writeContractFile(t, root, "app/windows.go", "//go:build windows\n\npackage main\nimport \"example.com/targetpolicy/lib\"\nvar _ = lib.Run\n")
	writeContractFile(t, root, "lib/worker.fw", "package lib\nimport e \"os/exec\"\nfunc Run() { e.Command(\"true\") }\n")
	policyPath := writeContractFile(t, root, "policy.json", `{"denyProcess":true}`)
	output := filepath.Join(root, "package-output")
	zeroHash := strings.Repeat("0", 64)
	args := []string{"ferrous-wheel", "package", "--compiler-sha256", zeroHash, "--go-version", "go1.25.1",
		"--go-sha256", zeroHash, "--target", "linux/amd64", "--target", "windows/amd64",
		"--out", output, "--policy", policyPath, app}
	var stderr bytes.Buffer
	if code := runCLI(args, &stderr); code != 1 || !strings.HasPrefix(stderr.String(), "lib/worker.fw:3:") {
		t.Fatalf("target-specific source escaped policy: code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("policy denial created package output: %v", err)
	}
}
