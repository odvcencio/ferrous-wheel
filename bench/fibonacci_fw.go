package bench

//line fibonacci.fw:3
func FibonacciFW(n int) int {
	if n <= 1 {
		return n
	}
	a := 0
	b := 1
	for i := 2; i < (n+1); i++ {
		next := a + b
		a = b
		b = next
		_ = i
	}
	return b
}
