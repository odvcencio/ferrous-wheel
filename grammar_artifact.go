package ferrouswheel

import (
	"crypto/sha256"
	_ "embed"
	"fmt"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

//go:generate go run ./internal/cmd/gen-grammar

//go:embed grammar.bin
var fwGrammarBlob []byte

func loadVerifiedFWLanguage(blob []byte) (*gotreesitter.Language, error) {
	sum := fmt.Sprintf("%x", sha256.Sum256(blob))
	if sum != fwGrammarBlobSHA256 {
		return nil, fmt.Errorf("embedded grammar checksum mismatch: got %s, want %s; run go generate .", sum, fwGrammarBlobSHA256)
	}
	lang, err := gotreesitter.LoadLanguage(blob)
	if err != nil {
		return nil, fmt.Errorf("load embedded grammar: %w", err)
	}
	if lang.Name != "ferrous_wheel" || !lang.CompatibleWithRuntime() {
		return nil, fmt.Errorf("embedded grammar is incompatible: name %q, ABI %d", lang.Name, lang.Version())
	}
	return lang, nil
}
