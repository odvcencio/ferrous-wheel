package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type runRequest struct {
	Code string `json:"code"`
}

type rateLimiter struct {
	mu      sync.Mutex
	clients map[string]time.Time
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{clients: make(map[string]time.Time)}
	// Evict stale entries every 5 minutes.
	go func() {
		for range time.Tick(5 * time.Minute) {
			rl.mu.Lock()
			for ip, last := range rl.clients {
				if time.Since(last) > time.Minute {
					delete(rl.clients, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	last, ok := rl.clients[ip]
	if ok && time.Since(last) < 6*time.Second {
		return false
	}
	rl.clients[ip] = time.Now()
	return true
}

var limiter = newRateLimiter()

func handleRun(w http.ResponseWriter, r *http.Request) {
	origin := os.Getenv("CORS_ORIGIN")
	if origin == "" {
		origin = "https://odvcencio.github.io"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	ip := r.RemoteAddr
	if !limiter.allow(ip) {
		http.Error(w, "rate limited", 429)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), 400)
		return
	}
	var req runRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), 400)
		return
	}

	result, err := runSandbox(req.Code)
	if err != nil {
		http.Error(w, "sandbox: "+err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	http.HandleFunc("/api/run", handleRun)
	log.Printf("playground backend listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
