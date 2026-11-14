package azure

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // Stored receipt images are JPEG or PNG.
	"math"

	"golang.org/x/image/draw"
)

const (
	// maxShrinkPixels refuses images whose decoded pixels would not fit
	// the memory budget of the app container.
	maxShrinkPixels = 50_000_000
	shrinkQuality   = 85
)

// shrink returns a JPEG of data that is smaller than limit bytes, for one
// request only: the stored image is never changed. It keeps everything in
// memory, so no temporary file can be left behind.
func shrink(data []byte, limit int) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read image size: %w", err)
	}
	if cfg.Width*cfg.Height > maxShrinkPixels {
		return nil, fmt.Errorf("image has %d x %d pixels", cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	// JPEG bytes grow roughly with the pixel count, so the first guess
	// scales the area by the size ratio, with a margin.
	scale := min(1.0, math.Sqrt(0.85*float64(limit)/float64(len(data))))
	bounds := src.Bounds()
	for range 8 {
		w, h := max(int(float64(bounds.Dx())*scale), 1), max(int(float64(bounds.Dy())*scale), 1)
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Src, nil)
		var out bytes.Buffer
		if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: shrinkQuality}); err != nil {
			return nil, fmt.Errorf("encode image: %w", err)
		}
		if out.Len() < limit {
			return out.Bytes(), nil
		}
		scale *= 0.8
	}
	return nil, errors.New("image stays above the size limit")
}
