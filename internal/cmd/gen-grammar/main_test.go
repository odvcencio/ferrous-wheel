package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicReplacesArtifactAndCleansUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grammar.bin")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte("new")); err != nil {
		t.Fatalf("replace artifact: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("artifact after replacement = %q, %v", data, err)
	}

	// A directory at the destination makes the final rename fail.
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(blocked, []byte("discard")); err == nil {
		t.Fatal("expected rename failure")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".grammar-*.tmp"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary files after failure = %v, %v", leftovers, err)
	}
}
