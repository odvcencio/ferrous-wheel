package bench

import (
	"fmt"
)

//line enum.fw:3
type Tag struct {
	tag int
}

const (
	TagLow    = 0
	TagMedium = 1
	TagHigh   = 2
)

func Low() Tag    { return Tag{tag: 0} }
func Medium() Tag { return Tag{tag: 1} }
func High() Tag   { return Tag{tag: 2} }

//line enum.fw:9
func ClassifyFW(tag int) string {
	t := func() string {
		switch tag {
		case 0:
			return "low"
		case 1:
			return "medium"
		case 2:
			return "high"
		default:
			panic(fmt.Sprintf("non-exhaustive match: no arm matched value %v", tag))
		}
	}()
	return t
}
