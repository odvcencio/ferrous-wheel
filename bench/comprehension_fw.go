package bench

//line comprehension.fw:3
func ComprehensionFW(items []int) []interface{} {
	return func() []interface{} {
		var _result []interface{}
		for _, x := range items {
			if x > 0 {
				_result = append(_result, x*2)
			}
		}
		return _result
	}()
}
