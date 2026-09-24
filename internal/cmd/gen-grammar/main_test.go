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

func TestWriteArtifactPairRestoresBlobWhenManifestWriteFails(t *testing.T) {
	dir := t.TempDir()
	blobPath := filepath.Join(dir, "grammar.bin")
	manifestPath := filepath.Join(dir, "grammar_artifact_manifest.go")
	if err := os.WriteFile(blobPath, []byte("old blob"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory at the manifest path forces the second replacement to fail.
	if err := os.Mkdir(manifestPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactPair(blobPath, []byte("new blob"), manifestPath, []byte("new manifest")); err == nil {
		t.Fatal("expected manifest replacement failure")
	}
	data, err := os.ReadFile(blobPath)
	if err != nil || string(data) != "old blob" {
		t.Fatalf("blob after failed pair update = %q, %v", data, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".grammar-*.tmp"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary files after failed pair update = %v, %v", leftovers, err)
	}
}
