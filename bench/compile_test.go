package bench

import (
	"os"
	"testing"

	ferrouswheel "m31labs.dev/ferrous-wheel"
)

func BenchmarkTranspileFibonacci(b *testing.B) {
	source, err := os.ReadFile("testdata/fibonacci.fw")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := ferrouswheel.TranspileWithOptions(source, ferrouswheel.TranspileOptions{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTranspileJson(b *testing.B) {
	source, err := os.ReadFile("testdata/json.fw")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := ferrouswheel.TranspileWithOptions(source, ferrouswheel.TranspileOptions{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTranspileEnum(b *testing.B) {
	source, err := os.ReadFile("testdata/enum.fw")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := ferrouswheel.TranspileWithOptions(source, ferrouswheel.TranspileOptions{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTranspileFanout(b *testing.B) {
	source, err := os.ReadFile("testdata/fanout.fw")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := ferrouswheel.TranspileWithOptions(source, ferrouswheel.TranspileOptions{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTranspileComprehension(b *testing.B) {
	source, err := os.ReadFile("testdata/comprehension.fw")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := ferrouswheel.TranspileWithOptions(source, ferrouswheel.TranspileOptions{})
		if err != nil {
			b.Fatal(err)
		}
	}
}
