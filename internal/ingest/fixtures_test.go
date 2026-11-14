package ingest_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/gen2brain/h265/heic"
)

// The fixtures are synthetic: four flat quadrants, top-left red, top-right
// green, bottom-left blue and bottom-right white, so that a decoded image
// shows both its size and its orientation. testdata/README.md says how each
// file was made.
func quadrantImage(w, h int) *image.NRGBA64 {
	colors := [4]color.NRGBA64{
		{R: 0xffff, A: 0xffff},
		{G: 0xc000, A: 0xffff},
		{B: 0xffff, A: 0xffff},
		{R: 0xffff, G: 0xffff, B: 0xffff, A: 0xffff},
	}
	img := image.NewNRGBA64(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			i := 0
			if x >= w/2 {
				i++
			}
			if y >= h/2 {
				i += 2
			}
			img.SetNRGBA64(x, y, colors[i])
		}
	}
	return img
}

func to8Bit(src image.Image) *image.NRGBA {
	dst := image.NewNRGBA(src.Bounds())
	for y := range src.Bounds().Dy() {
		for x := range src.Bounds().Dx() {
			dst.Set(x, y, src.At(x, y))
		}
	}
	return dst
}

// colorName names the quadrant colour closest to the pixel at (x, y).
func colorName(img image.Image, x, y int) string {
	r, g, b, _ := img.At(x, y).RGBA()
	r, g, b = r>>8, g>>8, b>>8
	switch {
	case r > 200 && g > 200 && b > 200:
		return "white"
	case r > 180 && g < 80 && b < 80:
		return "red"
	case g > 140 && r < 80 && b < 80:
		return "green"
	case b > 180 && r < 80 && g < 80:
		return "blue"
	default:
		return fmt.Sprintf("rgb(%d,%d,%d)", r, g, b)
	}
}

// layout describes a decoded fixture: its size and the colour at the centre
// of each quadrant, top-left, top-right, bottom-left, bottom-right.
type layout struct {
	Width, Height int
	Quadrants     [4]string
}

func layoutOf(img image.Image) layout {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	return layout{Width: w, Height: h, Quadrants: [4]string{
		colorName(img, b.Min.X+w/4, b.Min.Y+h/4), colorName(img, b.Min.X+3*w/4, b.Min.Y+h/4),
		colorName(img, b.Min.X+w/4, b.Min.Y+3*h/4), colorName(img, b.Min.X+3*w/4, b.Min.Y+3*h/4),
	}}
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// TestGenerateFixtures rewrites the source images and the fixtures made with
// the decoder's own encoder. It runs only when asked:
//
//	INGEST_UPDATE_FIXTURES=1 go test ./internal/ingest -run TestGenerateFixtures
//
// The heif-enc fixtures are made from the same sources outside the
// repository, as testdata/README.md describes.
func TestGenerateFixtures(t *testing.T) {
	if os.Getenv("INGEST_UPDATE_FIXTURES") != "1" {
		t.Skip("set INGEST_UPDATE_FIXTURES=1 to regenerate the fixtures")
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join("testdata", name), data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	encodePNG := func(img image.Image) []byte {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatalf("encode png: %v", err)
		}
		return buf.Bytes()
	}
	encodeHEIC := func(img image.Image, opts heic.EncodeOptions) []byte {
		var buf bytes.Buffer
		if err := heic.Encode(&buf, img, opts); err != nil {
			t.Fatalf("encode heic: %v", err)
		}
		return buf.Bytes()
	}

	small, large := quadrantImage(128, 64), quadrantImage(256, 128)
	write("quadrants-128x64.png", encodePNG(to8Bit(small)))
	write("quadrants-256x128.png", encodePNG(to8Bit(large)))
	write("quadrants16-128x64.png", encodePNG(small))
	write("sample.heic", encodeHEIC(to8Bit(small), heic.EncodeOptions{Quality: 90}))
	write("sample-main10.heic", encodeHEIC(small, heic.EncodeOptions{Quality: 90, BitDepth: 10}))
}
