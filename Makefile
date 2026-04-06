.PHONY: wasm bench

wasm:
	GOOS=js GOARCH=wasm go build -o playground/frontend/fw.wasm ./playground/wasm/
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" playground/frontend/wasm_exec.js

bench:
	go test -bench=. -benchmem -count=5 ./bench/... | tee bench/results.txt
