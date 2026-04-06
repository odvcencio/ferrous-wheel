package bench

import (
	"encoding/json"
	"testing"
)

// --- Fibonacci ---

func BenchmarkFibonacciFW(b *testing.B) {
	for i := 0; i < b.N; i++ {
		FibonacciFW(30)
	}
}

func BenchmarkFibonacciGo(b *testing.B) {
	for i := 0; i < b.N; i++ {
		FibonacciGo(30)
	}
}

// --- JSON roundtrip ---

func BenchmarkJsonRoundtripFW(b *testing.B) {
	data, _ := json.Marshal(Record{ID: 1, Name: "bench", Value: 3.14})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		JsonRoundtripFW(data)
	}
}

func BenchmarkJsonRoundtripGo(b *testing.B) {
	data, _ := json.Marshal(Record{ID: 1, Name: "bench", Value: 3.14})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		JsonRoundtripGo(data)
	}
}

// --- Enum + Match ---

func BenchmarkClassifyFW(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j := 0; j < 1000; j++ {
			ClassifyFW(j % 3)
		}
	}
}

func BenchmarkClassifyGo(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j := 0; j < 1000; j++ {
			ClassifyGo(j % 3)
		}
	}
}

// --- Fan Out ---

func BenchmarkFanoutProcessFW(b *testing.B) {
	items := make([]int, 1000)
	for i := range items {
		items[i] = i
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FanoutProcessFW(items)
	}
}

func BenchmarkFanoutProcessGo(b *testing.B) {
	items := make([]int, 1000)
	for i := range items {
		items[i] = i
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FanoutProcessGo(items)
	}
}

// --- List Comprehension ---

func BenchmarkComprehensionFW(b *testing.B) {
	items := make([]int, 10000)
	for i := range items {
		items[i] = i - 5000
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComprehensionFW(items)
	}
}

func BenchmarkComprehensionGo(b *testing.B) {
	items := make([]int, 10000)
	for i := range items {
		items[i] = i - 5000
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComprehensionGo(items)
	}
}
