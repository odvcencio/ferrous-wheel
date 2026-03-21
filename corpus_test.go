package ferrouswheel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCorpusTranspiles(t *testing.T) {
	files, err := filepath.Glob("testdata/corpus/*.fw")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no corpus files found")
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			goCode, err := Transpile(src)
			if err != nil {
				t.Fatalf("transpile error: %v", err)
			}
			if goCode == "" {
				t.Fatal("empty output")
			}
			// Check no reflect import (allowing reflect in user imports)
			if strings.Contains(goCode, `"reflect"`) {
				t.Errorf("output imports reflect:\n%s", goCode)
			}
		})
	}
}
