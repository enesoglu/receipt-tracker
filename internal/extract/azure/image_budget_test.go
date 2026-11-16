package azure_test

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/extract"
)

func TestExtract_RefusesOversizedDecodedImageBeforeSubmission(t *testing.T) {
	f := newFixture(t)
	data := widePNG(t)
	_, err := f.client.Extract(t.Context(), extract.Image{Data: data, ContentType: "image/png"})
	if err == nil {
		t.Fatal("49-megapixel 16-bit PNG was decoded and submitted instead of refused")
	}
	failure := extractErr(t, err)
	if !failure.Permanent || failure.Reason != "The image is too large to be read." {
		t.Fatalf("failure = %+v, want a permanent image-size failure", failure)
	}
	if len(f.service.seen(http.MethodPost)) != 0 || f.meter.pages != 0 {
		t.Fatal("oversized image reached the provider or consumed quota")
	}
}

// widePNG is a valid 49 MP, 16-bit RGBA image just above the OCR byte cap.
// Streaming constant rows avoids allocating its 392 MB decoded image in the
// fixture generator. A legal private ancillary chunk pads the encoded file.
func widePNG(t *testing.T) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	row := make([]byte, 7000*8+1)
	for x := range 7000 {
		binary.BigEndian.PutUint64(row[1+x*8:], 0x8001700160018001)
	}
	for range 7000 {
		if _, err := writer.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := []byte("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, payload []byte) {
		length := len(payload)
		if length > 4<<20 {
			t.Fatal("synthetic chunk exceeds its bound")
			return
		}
		data = binary.BigEndian.AppendUint32(data, uint32(length))
		start := len(data)
		data = append(data, kind...)
		data = append(data, payload...)
		data = binary.BigEndian.AppendUint32(data, crc32.ChecksumIEEE(data[start:]))
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header, 7000)
	binary.BigEndian.PutUint32(header[4:], 7000)
	header[8], header[9] = 16, 6
	chunk("IHDR", header)
	chunk("IDAT", compressed.Bytes())
	padding := (4 << 20) + 1024 - len(data) - 24
	if padding < 0 {
		t.Fatal("synthetic image is larger than expected")
	}
	chunk("npAd", make([]byte, padding))
	chunk("IEND", nil)
	return data
}
