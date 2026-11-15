package mcpserver

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SDK v1.8 provides session routing, 404 for forgotten sessions and JSON
// request/response, but its standalone GET stream has only an initial comment.
// RPC KeepAlive sends pings, not periodic SSE bytes. Keep only GET streams alive
// here; every tool call uses the SDK's independent JSON response mode.
func keepalive(next http.Handler, options Options) http.Handler {
	interval := options.KeepAliveInterval
	if interval <= 0 {
		interval = 25 * time.Second
	}
	ticker := options.NewTicker
	if ticker == nil {
		ticker = func(d time.Duration) (<-chan time.Time, func()) { t := time.NewTicker(d); return t.C, t.Stop }
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stream := &streamWriter{ResponseWriter: w, ready: make(chan struct{})}
		done := make(chan struct{})
		go func() {
			defer close(done)
			select {
			case <-ctx.Done():
				return
			case <-stream.ready:
			}
			ticks, stop := ticker(interval)
			defer stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticks:
					stream.mu.Lock()
					if !stream.finished {
						_, err := stream.ResponseWriter.Write([]byte(": keepalive\n\n"))
						if err != nil {
							cancel()
						} else {
							_ = http.NewResponseController(stream.ResponseWriter).Flush()
						}
					}
					stream.mu.Unlock()
				}
			}
		}()
		defer func() { stream.mu.Lock(); stream.finished = true; stream.mu.Unlock(); cancel(); <-done }()
		next.ServeHTTP(stream, r.WithContext(ctx))
	})
}

type streamWriter struct {
	http.ResponseWriter
	mu                sync.Mutex
	ready             chan struct{}
	started, finished bool
}

func (w *streamWriter) start(status int) {
	if w.started {
		return
	}
	w.started = true
	if status == http.StatusOK && strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		// The server's normal write deadline must not cut a healthy long-lived GET.
		_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Time{})
		w.Header().Set("X-Accel-Buffering", "no")
		w.ResponseWriter.WriteHeader(status)
		close(w.ready)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *streamWriter) WriteHeader(status int) { w.mu.Lock(); defer w.mu.Unlock(); w.start(status) }
func (w *streamWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.start(200)
	return w.ResponseWriter.Write(data)
}

func (w *streamWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.start(200)
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *streamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
