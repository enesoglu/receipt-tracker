package ingest_test

import (
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"testing"

	"github.com/gen2brain/h265/heic"
)

// TestGenerateResourceFixtures is opt-in setup for task resources. The patterned
// pixels are synthetic and deliberately less compressible than flat fixtures.
func TestGenerateResourceFixtures(t *testing.T) {
	directory := os.Getenv("INGEST_RESOURCE_FIXTURE_DIR")
	if directory == "" {
		t.Skip("task resources supplies a disposable fixture directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	img := image.NewNRGBA(image.Rect(0, 0, 4032, 3024))
	for y := range 3024 {
		for x := range 4032 {
			value := uint8((x*7 + y*13 + (x*y)%251) % 256)
			img.SetNRGBA(x, y, color.NRGBA{R: value, G: uint8(x % 256), B: uint8(y % 256), A: 255})
		}
	}
	for _, fixture := range []struct {
		name   string
		encode func(io.Writer) error
	}{
		{"synthetic.jpg", func(w io.Writer) error { return jpeg.Encode(w, img, &jpeg.Options{Quality: 85}) }},
		{"synthetic.heic", func(w io.Writer) error { return heic.Encode(w, img, heic.EncodeOptions{Quality: 80}) }},
	} {
		file, err := root.OpenFile(fixture.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := fixture.encode(file)
		closeErr := file.Close()
		if encodeErr != nil || closeErr != nil {
			t.Fatalf("encode %s: %v; close: %v", fixture.name, encodeErr, closeErr)
		}
	}
}
