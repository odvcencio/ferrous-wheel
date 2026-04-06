package bench

import (
	"sync"
)

//line fanout.fw:3
func FanoutProcessFW(items []int) []int {
	n := len(items)
	results := make([]int, n)
	var _wg_workers sync.WaitGroup
	for _wi := 0; _wi < n; _wi++ {
		_wg_workers.Add(1)
		go func() {
			defer _wg_workers.Done()
			{
				results[_wi%n] = _wi * 2
			}
		}()
	}
	_wg_workers.Wait()
	return results
}
