package bench

// ComprehensionGo is the hand-written Go equivalent of ComprehensionFW.
// Returns []interface{} to match the transpiler output signature.
func ComprehensionGo(items []int) []interface{} {
	var result []interface{}
	for _, x := range items {
		if x > 0 {
			result = append(result, x*2)
		}
	}
	return result
}
