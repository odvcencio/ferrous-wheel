// Package register adds ferrous-wheel to the gotreesitter grammars registry so
// the .fw extension is discoverable by registry consumers (editors, gts).
// Blank-import it to opt in:
//
//	import _ "m31labs.dev/ferrous-wheel/register"
//
// It lives in a subpackage so the ferrous-wheel core (transpile, format, the
// grammar DSL) stays free of the gotreesitter grammars registry (~200 grammars,
// ~22MB). Only code that wants .fw registered pays for it.
package register

import (
	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	ferrouswheel "m31labs.dev/ferrous-wheel"
)

func init() {
	base := ferrouswheel.GoGrammar()
	ext := ferrouswheel.Grammar()
	grammars.RegisterExtension(grammars.ExtensionEntry{
		Name:       "ferrous-wheel",
		Extensions: []string{".fw"},
		Aliases:    []string{"fw", "ferrouswheel"},
		GenerateLanguage: func() (*gotreesitter.Language, error) {
			return ferrouswheel.GenerateLanguage(ext)
		},
		HighlightQuery: ferrouswheel.GenerateHighlightQueries(base, ext),
	})
}
