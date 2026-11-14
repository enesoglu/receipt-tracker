package ingest

import (
	"bytes"
	"image"
	"log/slog"
	"strings"
	"testing"
)

func TestHEICConverter_DecoderPanicBecomesErrorLoggedOnce(t *testing.T) {
	var logs bytes.Buffer
	converter := NewHEICConverter(slog.New(slog.NewTextHandler(&logs, nil)))
	converter.decode = func([]byte) (image.Image, error) {
		// The kind of bug a hostile file finds in a decoder.
		panic("runtime error: index out of range [3] with length 0")
	}
	if _, err := converter.Convert(t.Context(), []byte("not really a photo")); err == nil {
		t.Fatal("convert succeeded although the decoder panicked")
	}
	if got := strings.Count(logs.String(), "heic decoder panicked"); got != 1 {
		t.Errorf("logged the panic %d times, want once:\n%s", got, logs.String())
	}
	if strings.Contains(logs.String(), "not really a photo") {
		t.Error("the log contains the uploaded bytes")
	}
}

func TestSniff(t *testing.T) {
	ftyp := func(major string, compatible ...string) []byte {
		box := []byte{0, 0, 0, 16, 'f', 't', 'y', 'p'}
		box = append(box, major...)
		box = append(box, 0, 0, 0, 0)
		for _, brand := range compatible {
			box = append(box, brand...)
			box[3] += 4
		}
		return append(box, "rest of the file"...)
	}
	tests := []struct {
		name string
		head []byte
		want string
	}{
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10}, typeJPEG},
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"), typePNG},
		{"heic major brand", ftyp("heic", "mif1", "heic"), typeHEIC},
		{"heic in compatible brands", ftyp("mif1", "mif1", "heic"), typeHEIC},
		{"ten-bit heix", ftyp("heix", "mif1"), typeHEIC},
		{"image sequence", ftyp("msf1", "msf1", "hevc"), typeHEIC},
		{"generic heif", ftyp("mif1", "mif1", "miaf"), typeHEIF},
		{"avif", ftyp("avif", "mif1", "miaf"), ""},
		{"avif listed after generic brands", ftyp("mif1", "miaf", "avif"), ""},
		{"mp4 video", ftyp("isom", "isom", "mp41"), ""},
		{"ftyp size beyond the data", append([]byte{0, 0, 1, 0}, []byte("ftypheic\x00\x00\x00\x00")...), ""},
		{"ftyp size not a multiple of four", append([]byte{0, 0, 0, 17}, []byte("ftypheic\x00\x00\x00\x00mif1x")...), ""},
		{"gif", []byte("GIF89a"), ""},
		{"pdf", []byte("%PDF-1.7"), ""},
		{"text", []byte("hello"), ""},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sniff(tt.head); got != tt.want {
				t.Errorf("sniff = %q, want %q", got, tt.want)
			}
		})
	}
}
