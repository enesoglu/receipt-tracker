package ingest_test

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"testing"

	"github.com/gen2brain/h265/heic"
	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/ingest"
)

func newConverter() *ingest.HEICConverter {
	return ingest.NewHEICConverter(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func decodeJPEG(t testing.TB, data []byte) image.Image {
	t.Helper()
	if !bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("output does not start with a JPEG signature: % x", data[:min(len(data), 4)])
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode output JPEG: %v", err)
	}
	return img
}

func TestHEICConverter_DecodesFixturesUpright(t *testing.T) {
	upright := [4]string{"red", "green", "blue", "white"}
	tests := []struct {
		fixture string
		want    layout
	}{
		// Made with the decoder's own encoder.
		{"sample.heic", layout{128, 64, upright}},
		{"sample-main10.heic", layout{128, 64, upright}},
		// Made with libheif's heif-enc, an independent encoder.
		{"main10-heifenc.heic", layout{128, 64, upright}},
		{"grid-heifenc.heic", layout{256, 128, upright}},
		// Rotated a quarter turn clockwise: the bottom-left quadrant ends up
		// top-left.
		{"rotated-heifenc.heic", layout{64, 128, [4]string{"blue", "red", "white", "green"}}},
		// A grid rotated three quarter turns clockwise, as phones store
		// portrait photos.
		{"grid-rotated-heifenc.heic", layout{128, 256, [4]string{"green", "white", "red", "blue"}}},
		// Mirrored left to right.
		{"mirrored-heifenc.heic", layout{128, 64, [4]string{"green", "red", "white", "blue"}}},
	}
	converter := newConverter()
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			out, err := converter.Convert(t.Context(), readFixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if diff := cmp.Diff(tt.want, layoutOf(decodeJPEG(t, out))); diff != "" {
				t.Errorf("decoded image mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHEICConverter_RefusesTruncatedFile(t *testing.T) {
	data := readFixture(t, "sample.heic")
	for _, cut := range []int{len(data) / 2, len(data) - 1, 40, 0} {
		if _, err := newConverter().Convert(t.Context(), data[:cut]); err == nil {
			t.Errorf("converting the first %d of %d bytes succeeded, want an error", cut, len(data))
		}
	}
}

func TestHEICConverter_FitsLongestSideWithin3000Pixels(t *testing.T) {
	var src bytes.Buffer
	if err := heic.Encode(&src, to8Bit(quadrantImage(3200, 160)), heic.EncodeOptions{Quality: 50}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := newConverter().Convert(t.Context(), src.Bytes())
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	want := layout{3000, 150, [4]string{"red", "green", "blue", "white"}}
	if diff := cmp.Diff(want, layoutOf(decodeJPEG(t, out))); diff != "" {
		t.Errorf("decoded image mismatch (-want +got):\n%s", diff)
	}
}

func TestHEICConverter_WritesNoMetadata(t *testing.T) {
	// A synthetic Exif payload: a little-endian TIFF header with no entries.
	exif := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	var src bytes.Buffer
	if err := heic.Encode(&src, to8Bit(quadrantImage(64, 64)), heic.EncodeOptions{Quality: 80, Exif: exif}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := newConverter().Convert(t.Context(), src.Bytes())
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// Walk the segments up to the image data: none may be an APPn segment,
	// which is where JFIF, Exif, XMP and ICC data would live.
	for i := 2; i+4 <= len(out); {
		if out[i] != 0xff {
			t.Fatalf("no marker at offset %d", i)
		}
		marker := out[i+1]
		if marker >= 0xe0 && marker <= 0xef {
			t.Fatalf("output has an APP%d segment", marker-0xe0)
		}
		if marker == 0xda { // start of scan
			return
		}
		i += 2 + int(out[i+2])<<8 + int(out[i+3])
	}
	t.Fatal("output has no image data")
}

func TestHEICConverter_StopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := newConverter().Convert(ctx, readFixture(t, "sample.heic")); err == nil {
		t.Fatal("convert with a canceled context succeeded")
	}
}

// FuzzConvertHEIC feeds mutated fixtures to the converter. Whatever the
// input, Convert must return, with a JPEG or an error, and never panic.
func FuzzConvertHEIC(f *testing.F) {
	testdata := os.DirFS("testdata")
	fixtures, err := fs.Glob(testdata, "*.heic")
	if err != nil {
		f.Fatalf("list fixtures: %v", err)
	}
	for _, name := range fixtures {
		data, err := fs.ReadFile(testdata, name)
		if err != nil {
			f.Fatalf("read fixture: %v", err)
		}
		f.Add(data)
	}
	converter := newConverter()
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := converter.Convert(t.Context(), data)
		if err != nil {
			return
		}
		if _, err := jpeg.DecodeConfig(bytes.NewReader(out)); err != nil {
			t.Errorf("converter returned output that is not a JPEG: %v", err)
		}
	})
}
