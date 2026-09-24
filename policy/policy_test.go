package policy

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDeniedProcessCallUsesSourceLine(t *testing.T) {
	source := []byte("package main\nimport e \"os/exec\"\nfunc main() {\n    let command = e.Command(\"sh\")\n    command.Run()\n}\n")
	diagnostics, err := Check(source, "agent.fw", Config{DenyProcess: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Line != 4 || diagnostics[0].Kind != "process" ||
		!strings.Contains(diagnostics[0].Error(), "agent.fw:4:") ||
		!strings.Contains(diagnostics[0].Message, "e.Command") {
		t.Fatalf("wrong process diagnostic: %+v", diagnostics)
	}
}

func TestCheckImportAllowlistAndNetworkCalls(t *testing.T) {
	t.Run("import allowlist", func(t *testing.T) {
		source := []byte("package main\nimport (\n    \"fmt\"\n    \"os/exec\"\n)\nfunc main() { fmt.Println(\"ok\") }\n")
		diagnostics, err := Check(source, "agent.fw", Config{AllowedImports: []string{"fmt"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 1 || diagnostics[0].Kind != "import" || diagnostics[0].Line != 4 {
			t.Fatalf("wrong import diagnostic: %+v", diagnostics)
		}
	})
	t.Run("network call", func(t *testing.T) {
		source := []byte("package main\nimport h \"net/http\"\nfunc main() {\n    h.Get(\"https://example.test\")\n}\n")
		diagnostics, err := Check(source, "agent.fw", Config{DenyNetwork: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 1 || diagnostics[0].Kind != "network" || diagnostics[0].Line != 4 {
			t.Fatalf("wrong network diagnostic: %+v", diagnostics)
		}
	})
	t.Run("URL parsing stays available", func(t *testing.T) {
		source := []byte("package main\nimport \"net/url\"\nfunc main() { url.Parse(\"https://example.test\") }\n")
		diagnostics, err := Check(source, "agent.fw", Config{DenyNetwork: true})
		if err != nil || len(diagnostics) != 0 {
			t.Fatalf("URL parser denied: diagnostics=%+v err=%v", diagnostics, err)
		}
	})
}

func TestCheckFileRootsFailClosedOnDynamicPaths(t *testing.T) {
	root := t.TempDir()
	traversal := root + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside"
	source := []byte(fmt.Sprintf("package main\nimport \"os\"\nfunc main() {\n    os.ReadFile(%q)\n    os.ReadFile(%q)\n    let name = %q\n    os.WriteFile(name, nil, 0600)\n}\n",
		filepath.Join(root, "report"), traversal, filepath.Join(root, "other")))
	diagnostics, err := Check(source, "files.fw", Config{FileRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 2 || diagnostics[0].Line != 5 || diagnostics[1].Line != 7 ||
		diagnostics[0].Kind != "filesystem" || diagnostics[1].Kind != "filesystem" ||
		!strings.Contains(diagnostics[1].Message, "literal") {
		t.Fatalf("wrong filesystem diagnostics: %+v", diagnostics)
	}
}

func TestCheckRejectsIndirectFileCallAndDotImport(t *testing.T) {
	source := []byte("package main\nimport . \"os\"\nfunc main() {\n    let read = ReadFile\n    read(\"/outside\")\n}\n")
	diagnostics, err := Check(source, "files.fw", Config{FileRoots: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Kind != "import" || diagnostics[0].Line != 2 {
		t.Fatalf("dot import escaped policy: %+v", diagnostics)
	}
}

func TestParseConfigRejectsUnknownFieldsAndRelativeRoots(t *testing.T) {
	if _, err := ParseConfig([]byte(`{"denyProces":true}`)); err == nil {
		t.Fatal("accepted misspelled policy field")
	}
	if _, err := ParseConfig([]byte(`{"fileRoots":["relative"]}`)); err == nil {
		t.Fatal("accepted relative filesystem root")
	}
	config, err := ParseConfig([]byte(fmt.Sprintf(`{"allowedImports":[],"denyProcess":true,"fileRoots":[%q]}`, t.TempDir())))
	if err != nil || config.AllowedImports == nil || config.FileRoots == nil || !config.DenyProcess {
		t.Fatalf("lost explicit empty allowlist or policy fields: config=%+v err=%v", config, err)
	}
}

func TestParseConfigRejectsNullAndDuplicateRules(t *testing.T) {
	for _, data := range []string{
		`{"denyProcess":true,"denyProcess":false}`,
		`{"allowedImports":null}`,
		`{"fileRoots":null}`,
	} {
		if _, err := ParseConfig([]byte(data)); err == nil {
			t.Fatalf("accepted policy that could silently disable a rule: %s", data)
		}
	}
}

func TestCheckLegacyFileCallAndRawSystemImport(t *testing.T) {
	root := t.TempDir()
	source := []byte(fmt.Sprintf("package main\nimport io \"io/ioutil\"\nfunc main() { io.ReadFile(%q) }\n", filepath.Join(root, "..", "outside")))
	diagnostics, err := Check(source, "legacy.fw", Config{FileRoots: []string{root}})
	if err != nil || len(diagnostics) != 1 || diagnostics[0].Kind != "filesystem" || diagnostics[0].Line != 3 {
		t.Fatalf("legacy file call escaped roots: diagnostics=%+v err=%v", diagnostics, err)
	}
	source = []byte("package main\nimport \"syscall\"\nfunc main() { syscall.Getpid() }\n")
	diagnostics, err = Check(source, "raw.fw", Config{FileRoots: []string{root}})
	if err != nil || len(diagnostics) != 1 || diagnostics[0].Kind != "filesystem" || diagnostics[0].Line != 2 {
		t.Fatalf("raw system import escaped roots: diagnostics=%+v err=%v", diagnostics, err)
	}
	diagnostics, err = Check(source, "raw.fw", Config{DenyNetwork: true})
	if err != nil || len(diagnostics) != 1 || diagnostics[0].Kind != "network" || diagnostics[0].Line != 2 {
		t.Fatalf("raw system import escaped network rule: diagnostics=%+v err=%v", diagnostics, err)
	}
}

func TestCheckWithoutRestrictionsAllowsSource(t *testing.T) {
	source := []byte("package main\nimport \"os/exec\"\nfunc main() { exec.Command(\"sh\") }\n")
	diagnostics, err := Check(source, "agent.fw", Config{})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("zero policy restricted source: diagnostics=%+v err=%v", diagnostics, err)
	}
}

func TestCheckProcessEntrypointsAndImportFallback(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		line   int
	}{
		{"os.StartProcess", "package main\nimport \"os\"\nfunc main() { os.StartProcess(\"x\", nil, nil) }\n", 3},
		{"ops.Run", "package main\nimport \"m31labs.dev/ferrous-wheel/ops\"\nfunc main() { ops.Run(nil, ops.Spec{}) }\n", 3},
		{"indirect exec reference", "package main\nimport e \"os/exec\"\nfunc main() { let start = e.Command }\n", 3},
		{"raw syscall import", "package main\nimport \"syscall\"\nfunc main() { syscall.Getpid() }\n", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostics, err := Check([]byte(test.source), "agent.fw", Config{DenyProcess: true})
			if err != nil || len(diagnostics) != 1 || diagnostics[0].Kind != "process" || diagnostics[0].Line != test.line {
				t.Fatalf("process entrypoint escaped policy: diagnostics=%+v err=%v", diagnostics, err)
			}
		})
	}
}

func TestCheckFileRootChecksBothRenamePathsAndHelpers(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "input")
	outside := filepath.Join(root, "..", "outside")
	for _, test := range []struct {
		name       string
		importLine string
		call       string
	}{
		{"rename destination", `"os"`, fmt.Sprintf("os.Rename(%q, %q)", inside, outside)},
		{"filepath walk", `"path/filepath"`, fmt.Sprintf("filepath.Walk(%q, nil)", outside)},
		{"hostfs root", `"m31labs.dev/ferrous-wheel/hostfs"`, fmt.Sprintf("hostfs.Open(%q)", outside)},
		{"ops append", `"m31labs.dev/ferrous-wheel/ops"`, fmt.Sprintf("ops.AppendFile(%q, nil, 0600)", outside)},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("package main\nimport " + test.importLine + "\nfunc main() { " + test.call + " }\n")
			diagnostics, err := Check(source, "paths.fw", Config{FileRoots: []string{root}})
			if err != nil || len(diagnostics) != 1 || diagnostics[0].Kind != "filesystem" || diagnostics[0].Line != 3 {
				t.Fatalf("file helper escaped roots: diagnostics=%+v err=%v", diagnostics, err)
			}
		})
	}
}

func TestCheckRejectsBadSourceAndConfig(t *testing.T) {
	if _, err := Check(nil, "", Config{DenyProcess: true}); err == nil {
		t.Fatal("accepted missing source filename")
	}
	if _, err := Check([]byte("package main"), "file.fw", Config{FileRoots: []string{"relative"}}); err == nil {
		t.Fatal("accepted relative root in programmatic policy")
	}
	if _, err := Check([]byte("package main\nfunc main( {\n"), "broken.fw", Config{DenyProcess: true}); err == nil {
		t.Fatal("accepted malformed source")
	}
}

func ExampleCheck() {
	source := []byte("package main\nimport \"os/exec\"\nfunc main() {\n    exec.Command(\"sh\")\n}\n")
	diagnostics, err := Check(source, "agent.fw", Config{DenyProcess: true})
	if err != nil {
		panic(err)
	}
	fmt.Println(diagnostics[0])
	// Output: agent.fw:4:5: policy: process call exec.Command is denied
}
