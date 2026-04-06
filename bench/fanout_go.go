package bench

import "sync"

// FanoutProcessGo is the hand-written Go equivalent of FanoutProcessFW.
// Uses WaitGroup + goroutines matching the transpiler's fan-out pattern.
func FanoutProcessGo(items []int) []int {
	n := len(items)
	results := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i // capture loop variable
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i%n] = i * 2
		}()
	}
	wg.Wait()
	return results
}
