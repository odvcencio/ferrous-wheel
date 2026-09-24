package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/odvcencio/gotreesitter/grammargen"
	ferrouswheel "m31labs.dev/ferrous-wheel"
)

func main() {
	check := flag.Bool("check", false, "fail if the tracked grammar artifact is stale")
	flag.Parse()

	blob, err := grammargen.Generate(ferrouswheel.Grammar())
	if err != nil {
		fatal("generate grammar: %v", err)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(blob))
	manifest := []byte(fmt.Sprintf(`package ferrouswheel

// The grammar generator updates this checksum with grammar.bin.
const fwGrammarBlobSHA256 = %q
`, sum))

	if *check {
		checkCurrent("grammar.bin", blob)
		checkCurrent("grammar_artifact_manifest.go", manifest)
		return
	}
	if err := writeArtifactPair("grammar.bin", blob, "grammar_artifact_manifest.go", manifest); err != nil {
		fatal("update grammar artifacts: %v", err)
	}
}

// writeArtifactPair restores the old blob if the manifest replacement fails.
// The loader also checks the checksum, so a process killed between replacements
// fails closed until the generator or source control restores a matching pair.
func writeArtifactPair(blobPath string, blob []byte, manifestPath string, manifest []byte) error {
	oldBlob, readErr := os.ReadFile(blobPath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read old %s: %w", blobPath, readErr)
	}
	if err := writeAtomic(blobPath, blob); err != nil {
		return fmt.Errorf("write %s: %w", blobPath, err)
	}
	if err := writeAtomic(manifestPath, manifest); err != nil {
		var restoreErr error
		if errors.Is(readErr, os.ErrNotExist) {
			restoreErr = os.Remove(blobPath)
		} else {
			restoreErr = writeAtomic(blobPath, oldBlob)
		}
		if restoreErr != nil {
			return errors.Join(fmt.Errorf("write %s: %w", manifestPath, err), fmt.Errorf("restore %s: %w", blobPath, restoreErr))
		}
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	return nil
}

func checkCurrent(path string, want []byte) {
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		fatal("%s is stale; run go generate .", path)
	}
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".grammar-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
