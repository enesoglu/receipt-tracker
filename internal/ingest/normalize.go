package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"

	"github.com/gen2brain/h265/heic"
	"golang.org/x/image/draw"
)

// Converter turns an uploaded HEIC/HEIF image into the JPEG that the rest
// of the pipeline uses. It sits behind an interface so that every channel
// that calls [Service.Submit] gets the same conversion, and so that a young
// decoder can be replaced without touching the ingest flow.
type Converter interface {
	// Convert returns the JPEG for src. An error means the upload cannot be
	// converted: truncated, corrupt, too large, or a variant the decoder
	// does not support. Convert is called for one image at a time.
	Convert(ctx context.Context, src []byte) ([]byte, error)
}

// Conversion settings from ARCHITECTURE §5.1.
const (
	convertedQuality = 85
	convertedMaxSide = 3000
	// maxConvertPixels bounds the pixels a HEIC may decode to. It admits the
	// 24-megapixel photos of current phones and refuses larger ones, which
	// would not fit the memory budget of the app container.
	maxConvertPixels = 26_000_000
)

// HEICConverter decodes HEIC/HEIF with gen2brain/h265 (pure Go), applies
// the clean aperture and the irot/imir transforms, fits the image within
// 3000 pixels on its longest side and encodes it once as JPEG at quality 85.
// The JPEG carries no metadata: the Go encoder writes none.
type HEICConverter struct {
	logger *slog.Logger
	decode func([]byte) (image.Image, error)
}

// NewHEICConverter returns the production converter.
func NewHEICConverter(logger *slog.Logger) *HEICConverter {
	return &HEICConverter{logger: logger, decode: decodeHEIC}
}

func decodeHEIC(src []byte) (image.Image, error) {
	cfg, err := heic.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > maxConvertPixels {
		return nil, fmt.Errorf("image has %d x %d pixels", cfg.Width, cfg.Height)
	}
	// One decoding goroutine: a conversion should not take every core from
	// the request handlers, and memory stays proportional to one tile.
	return heic.Decode(bytes.NewReader(src), heic.Options{AutoRotate: true, Threads: 1, FrameSizeLimit: maxConvertPixels})
}

// Convert implements [Converter].
func (c *HEICConverter) Convert(ctx context.Context, src []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("convert image: %w", err)
	}
	img, err := c.safeDecode(src)
	if err != nil {
		return nil, fmt.Errorf("decode heic: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("convert image: %w", err)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, fit(img, convertedMaxSide), &jpeg.Options{Quality: convertedQuality}); err != nil {
		return nil, fmt.Errorf("encode converted image: %w", err)
	}
	return out.Bytes(), nil
}

// safeDecode turns a decoder panic into an error. A hostile or corrupt file
// must cost the user one per-file error, not the server. The recovery covers
// the decode call only, so a bug elsewhere still surfaces.
func (c *HEICConverter) safeDecode(src []byte) (img image.Image, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			c.logger.Error("heic decoder panicked", "panic", fmt.Sprint(recovered), "size_bytes", len(src))
			img, err = nil, errors.New("decoder panicked")
		}
	}()
	return c.decode(src)
}

// fit returns src scaled down to at most maxSide pixels on its longest side
// and flattened onto white, because JPEG has no alpha. Bilinear sampling is
// used because the x/image kernel scalers allocate a float buffer of
// destination width times source height, about 290 MB for a 12-megapixel
// photo. Shrinking by more than half is done in halving steps so that
// bilinear sampling never skips pixels.
func fit(src image.Image, maxSide int) *image.RGBA {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	longest := max(w, h)
	if longest > maxSide {
		for longest > 2*maxSide {
			w, h = max((w+1)/2, 1), max((h+1)/2, 1)
			src = scale(src, w, h)
			longest = max(w, h)
		}
		w, h = max(w*maxSide/longest, 1), max(h*maxSide/longest, 1)
	}
	return scale(src, w, h)
}

func scale(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if src.Bounds().Dx() == w && src.Bounds().Dy() == h {
		draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Over)
		return dst
	}
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}
