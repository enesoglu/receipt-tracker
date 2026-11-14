package ingest

import (
	"bytes"
	"encoding/binary"
)

// The content types an upload may have. Everything after ingest sees only
// JPEG or PNG; HEIC and HEIF are converted to JPEG (ARCHITECTURE §5.1).
const (
	typeJPEG = "image/jpeg"
	typePNG  = "image/png"
	typeHEIC = "image/heic"
	typeHEIF = "image/heif"
)

// sniffLength is how many leading bytes sniff needs at most.
const sniffLength = 512

// brand is what an ftyp brand says about a HEIF file.
type brand int

const (
	brandOther brand = iota
	// brandHEVC: images coded with HEVC (ISO/IEC 23008-12 Annex B), still
	// images and sequences.
	brandHEVC
	// brandAV1: AVIF, which also lists the generic brands.
	brandAV1
	// brandGeneric: a structural HEIF brand that says nothing of the codec.
	brandGeneric
)

func classify(b []byte) brand {
	switch string(b) {
	case "heic", "heix", "heim", "heis", "hevc", "hevx", "hevm", "hevs":
		return brandHEVC
	case "avif", "avis":
		return brandAV1
	case "mif1", "msf1", "mif2", "miaf":
		return brandGeneric
	default:
		return brandOther
	}
}

// sniff returns the content type of an upload from its first bytes, or ""
// when it is not a JPEG, PNG or HEIC/HEIF image. The declared type and the
// file name are never consulted: both come from the client.
func sniff(head []byte) string {
	head = head[:min(len(head), sniffLength)]
	switch {
	case bytes.HasPrefix(head, []byte{0xff, 0xd8, 0xff}):
		return typeJPEG
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return typePNG
	}
	return sniffHEIF(head)
}

// sniffHEIF reads the leading ftyp box: size, "ftyp", major brand, minor
// version, then compatible brands to the end of the box.
func sniffHEIF(head []byte) string {
	if len(head) < 16 || !bytes.Equal(head[4:8], []byte("ftyp")) {
		return ""
	}
	size := int(binary.BigEndian.Uint32(head[0:4]))
	if size < 16 || size%4 != 0 || size > len(head) {
		return ""
	}
	brands := [][]byte{head[8:12]}
	for offset := 16; offset+4 <= size; offset += 4 {
		brands = append(brands, head[offset:offset+4])
	}
	generic := false
	for _, b := range brands {
		switch classify(b) {
		case brandHEVC:
			return typeHEIC
		case brandAV1:
			return ""
		case brandGeneric:
			generic = true
		case brandOther:
		}
	}
	if generic {
		return typeHEIF
	}
	return ""
}
