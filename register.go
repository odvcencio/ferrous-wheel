package ferrouswheel

import (
	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func init() {
	base := GoGrammar()
	ext := Grammar()
	grammars.RegisterExtension(grammars.ExtensionEntry{
		Name:       "ferrous-wheel",
		Extensions: []string{".fw"},
		Aliases:    []string{"fw", "ferrouswheel"},
		GenerateLanguage: func() (*gotreesitter.Language, error) {
			return GenerateLanguage(ext)
		},
		HighlightQuery: GenerateHighlightQueries(base, ext),
	})
}
