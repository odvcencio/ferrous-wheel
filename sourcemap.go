package ferrouswheel

import (
	"fmt"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// emitLineDirective returns a //line directive that maps generated Go code
// back to the original .fw source file location.
func (t *fwTranspiler) emitLineDirective(n *gotreesitter.Node) string {
	if t.sourceFile == "" || n == nil {
		return ""
	}
	row := n.StartPoint().Row + 1
	return fmt.Sprintf("//line %s:%d\n", t.sourceFile, row)
}
