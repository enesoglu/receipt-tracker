package ingest_test

import (
	"bytes"
	"image"
	"image/color"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gen2brain/h265/heic"
)

// BenchmarkConvertHEIC12MP converts a 12-megapixel (4032x3024) HEIC, the
// size of a phone photo, and reports the peak heap in use while it runs.
// The app container starts with a 512 MB limit (ARCHITECTURE §7); one
// conversion runs at a time and must stay well below half of that:
//
//	go test ./internal/ingest -run '^$' -bench ConvertHEIC12MP -benchtime 3x
func BenchmarkConvertHEIC12MP(b *testing.B) {
	const w, h = 4032, 3024
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			// Texture rather than flat colour, so the bitstream is as dense
			// as a photo's.
			v := uint8((x*7 + y*13 + (x*y)%251) % 256)
			src.SetNRGBA(x, y, color.NRGBA{R: v, G: uint8(x), B: uint8(y), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := heic.Encode(&encoded, src, heic.EncodeOptions{Quality: 80}); err != nil {
		b.Fatalf("encode: %v", err)
	}
	data := encoded.Bytes()
	src = nil
	converter := newConverter()
	runtime.GC()

	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var stats runtime.MemStats
		for {
			runtime.ReadMemStats(&stats)
			if stats.HeapInuse > peak.Load() {
				peak.Store(stats.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	b.ResetTimer()
	for b.Loop() {
		if _, err := converter.Convert(b.Context(), data); err != nil {
			b.Fatalf("convert: %v", err)
		}
	}
	b.StopTimer()
	close(stop)
	<-done
	b.ReportMetric(float64(len(data))/1e6, "input-MB")
	b.ReportMetric(float64(peak.Load())/1e6, "peak-heap-MB")
}
