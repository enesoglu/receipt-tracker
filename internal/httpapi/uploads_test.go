package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/ingest"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/storage/local"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

type uploadServer struct {
	t        *testing.T
	pool     *pgxpool.Pool
	server   *httpapi.Server
	sessions *scs.SessionManager
	call     func(user uuid.UUID, body io.Reader, contentType string) *httptest.ResponseRecorder
	alice    uuid.UUID
	bob      uuid.UUID
}

func newUploadServer(t *testing.T) uploadServer {
	t.Helper()
	pool := dbtest.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	newUser := func(email string) (*auth.Service, uuid.UUID) {
		cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.com", "bob@example.com"}, DevLoginEmail: email}
		users := auth.New(pool, cfg, nil, time.Now)
		return users, testutil.NewUser(t, users).ID
	}
	users, alice := newUser("alice@example.com")
	_, bob := newUser("bob@example.com")
	sessions, stop := auth.NewSessions(pool, false)
	t.Cleanup(stop)
	store, err := local.New(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("open local store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := ingest.New(pool, store, "local", ingest.NewHEICConverter(logger), notifier{}, logger, time.Now)
	// The receipts service reads the same store, so the image and its
	// original are served from where ingest wrote them.
	receiptService := receipts.New(pool, time.Now, receipts.Options{Images: store})
	server := httpapi.New(":0", logger, http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{Uploads: service}, nil, nil, nil, nil, nil, receiptService)
	s := uploadServer{t: t, pool: pool, server: server, sessions: sessions, alice: alice, bob: bob}
	s.call = func(user uuid.UUID, body io.Reader, contentType string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/receipts", body)
		if user != uuid.Nil {
			request = testutil.Request(t, sessions, user, http.MethodPost, "/api/v1/receipts", body)
		}
		request.Header.Set("Content-Type", contentType)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	return s
}

type notifier struct{}

func (notifier) Notify() {}

type part struct {
	field, name string
	content     []byte
}

func multipartBody(t *testing.T, parts ...part) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, p := range parts {
		w, err := form.CreateFormFile(p.field, p.name)
		if err != nil {
			t.Fatalf("create part: %v", err)
		}
		if _, err := w.Write(p.content); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	return &body, form.FormDataContentType()
}

func smallJPEG(t *testing.T, seed byte) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = seed + byte(i)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// smallPNG returns a small distinct PNG.
func smallPNG(t *testing.T, seed byte) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = seed + byte(i)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// get makes an authenticated GET request to the upload server.
func (s uploadServer) get(user uuid.UUID, target string) *httptest.ResponseRecorder {
	request := testutil.Request(s.t, s.sessions, user, http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	s.server.Handler().ServeHTTP(response, request)
	return response
}

// imageOf loads the image row of one of Alice's receipts: its id and the
// original columns, which must be set. A request that dropped the original
// fails the scan and the test reports it.
func (s uploadServer) imageOf(t *testing.T, receiptID uuid.UUID) (uuid.UUID, string, int64) {
	t.Helper()
	var imageID uuid.UUID
	var contentType string
	var size int64
	err := s.pool.QueryRow(t.Context(),
		"SELECT id, original_content_type, original_size_bytes FROM receipt_images WHERE receipt_id = $1 AND user_id = $2",
		receiptID, s.alice).Scan(&imageID, &contentType, &size)
	if err != nil {
		t.Fatalf("load image row: %v", err)
	}
	return imageID, contentType, size
}

func TestUploadReceipts_PairsOriginalWithFileInEitherPartOrder(t *testing.T) {
	s := newUploadServer(t)
	camera, err := os.ReadFile("../ingest/testdata/sample.heic")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	orders := []struct {
		name  string
		parts []part
	}{
		{"original first", []part{{"original", "camera.heic", camera}, {"files", "scan.jpg", smallJPEG(t, 21)}}},
		{"original last", []part{{"files", "scan.jpg", smallJPEG(t, 22)}, {"original", "camera.heic", camera}}},
		{"original earlier, unrelated part between", []part{
			{"original", "camera.heic", camera},
			{"note", "note.txt", []byte("an unrelated field")},
			{"files", "scan.jpg", smallJPEG(t, 23)},
		}},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			body, contentType := multipartBody(t, order.parts...)
			got := decodeUpload(t, s.call(s.alice, body, contentType))
			if len(got.Results) != 1 || got.Results[0].Outcome != httpapi.UploadOutcomeCreated {
				t.Fatalf("results = %+v, want one created receipt", got.Results)
			}
			receiptID := *got.Results[0].ReceiptId

			imageID, originalType, originalSize := s.imageOf(t, receiptID)
			if originalType != "image/heic" || originalSize != int64(len(camera)) {
				t.Errorf("original columns = %s %d, want the untouched camera photo", originalType, originalSize)
			}
			response := s.get(s.alice, fmt.Sprintf("/api/v1/receipts/%s/images/%s?variant=original", receiptID, imageID))
			if response.Code != http.StatusOK {
				t.Fatalf("GET variant=original = %d, want 200; body: %s", response.Code, response.Body)
			}
			if got := response.Body.Bytes(); !bytes.Equal(got, camera) {
				t.Errorf("served original differs from the camera photo (%d bytes)", len(got))
			}
			if got := response.Header().Get("Content-Type"); got != "image/heic" {
				t.Errorf("content type = %q, want image/heic", got)
			}
		})
	}
}

func TestUploadReceipts_StoresJPEGAndPNGOriginals(t *testing.T) {
	s := newUploadServer(t)
	tests := []struct {
		name     string
		scan     []byte
		fileName string
		original []byte
		kind     string
	}{
		{"jpeg original", smallJPEG(t, 41), "camera.jpg", smallJPEG(t, 42), "image/jpeg"},
		{"png original", smallJPEG(t, 43), "camera.png", smallPNG(t, 7), "image/png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, contentType := multipartBody(t,
				part{"files", "scan.jpg", tt.scan},
				part{"original", tt.fileName, tt.original},
			)
			got := decodeUpload(t, s.call(s.alice, body, contentType))
			if len(got.Results) != 1 || got.Results[0].Outcome != httpapi.UploadOutcomeCreated {
				t.Fatalf("results = %+v, want one created receipt", got.Results)
			}
			receiptID := *got.Results[0].ReceiptId
			imageID, originalType, originalSize := s.imageOf(t, receiptID)
			if originalType != tt.kind || originalSize != int64(len(tt.original)) {
				t.Fatalf("original columns = %s %d, want the untouched %s", originalType, originalSize, tt.kind)
			}
			response := s.get(s.alice, fmt.Sprintf("/api/v1/receipts/%s/images/%s?variant=original", receiptID, imageID))
			if response.Code != http.StatusOK {
				t.Fatalf("GET variant=original = %d, want 200; body: %s", response.Code, response.Body)
			}
			if got := response.Body.Bytes(); !bytes.Equal(got, tt.original) {
				t.Errorf("served original differs from the uploaded bytes (%d bytes)", len(got))
			}
			if got := response.Header().Get("Content-Type"); got != tt.kind {
				t.Errorf("content type = %q, want %q", got, tt.kind)
			}
			// The scan stays its own object: the image route serves it,
			// untouched by the original that shares its extension.
			response = s.get(s.alice, fmt.Sprintf("/api/v1/receipts/%s/images/%s", receiptID, imageID))
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), tt.scan) {
				t.Errorf("GET image = %d, want the scan bytes untouched", response.Code)
			}
		})
	}
}

func TestUploadReceipts_RefusesOriginalWithMoreThanOneFile(t *testing.T) {
	s := newUploadServer(t)
	camera, a, b := smallJPEG(t, 9), smallJPEG(t, 1), smallJPEG(t, 2)
	orders := [][]part{
		{{"original", "camera.jpg", camera}, {"files", "a.jpg", a}, {"files", "b.jpg", b}},
		{{"files", "a.jpg", a}, {"original", "camera.jpg", camera}, {"files", "b.jpg", b}},
		{{"files", "a.jpg", a}, {"files", "b.jpg", b}, {"original", "camera.jpg", camera}},
	}
	for i, parts := range orders {
		t.Run(fmt.Sprintf("order %d", i+1), func(t *testing.T) {
			body, contentType := multipartBody(t, parts...)
			response := s.call(s.alice, body, contentType)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", response.Code, response.Body)
			}
			if problem := decodeProblem(t, response); problem.Errors == nil || (*problem.Errors)[0].Field != "original" {
				t.Errorf("problem = %+v, want an error on original", problem)
			}
			// The whole request is refused before anything is committed.
			var count int
			if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM receipts WHERE user_id = $1", s.alice).Scan(&count); err != nil {
				t.Fatalf("count receipts: %v", err)
			}
			if count != 0 {
				t.Fatalf("receipts created = %d, want 0", count)
			}
		})
	}
}

func TestUploadReceipts_RefusesOriginalWithoutFiles(t *testing.T) {
	s := newUploadServer(t)
	body, contentType := multipartBody(t, part{"original", "camera.jpg", smallJPEG(t, 9)})
	response := s.call(s.alice, body, contentType)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", response.Code, response.Body)
	}
	if problem := decodeProblem(t, response); problem.Errors == nil || (*problem.Errors)[0].Field != "files" {
		t.Errorf("problem = %+v, want an error on files", problem)
	}
}

func TestUploadReceipts_RefusesInvalidOriginals(t *testing.T) {
	s := newUploadServer(t)
	tests := []struct {
		name     string
		original []byte
	}{
		{"oversized", append([]byte{0xff, 0xd8, 0xff}, make([]byte, ingest.MaxFileBytes)...)},
		{"empty", nil},
		{"not a photo", []byte("milk, bread")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, contentType := multipartBody(t,
				part{"files", "scan.jpg", smallJPEG(t, 3)},
				part{"original", "camera.jpg", tt.original},
			)
			response := s.call(s.alice, body, contentType)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", response.Code, response.Body)
			}
			if problem := decodeProblem(t, response); problem.Errors == nil || (*problem.Errors)[0].Field != "original" {
				t.Errorf("problem = %+v, want an error on original", problem)
			}
		})
	}
}

func TestUploadReceipts_RefusesTwoOriginals(t *testing.T) {
	s := newUploadServer(t)
	camera, scan := smallJPEG(t, 9), smallJPEG(t, 4)
	orders := [][]part{
		{{"original", "camera.jpg", camera}, {"files", "scan.jpg", scan}, {"original", "camera-2.jpg", camera}},
		{{"files", "scan.jpg", scan}, {"original", "camera.jpg", camera}, {"original", "camera-2.jpg", camera}},
		{{"original", "camera.jpg", camera}, {"original", "camera-2.jpg", camera}, {"files", "scan.jpg", scan}},
	}
	for i, parts := range orders {
		t.Run(fmt.Sprintf("order %d", i+1), func(t *testing.T) {
			body, contentType := multipartBody(t, parts...)
			response := s.call(s.alice, body, contentType)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", response.Code, response.Body)
			}
			if problem := decodeProblem(t, response); problem.Errors == nil || (*problem.Errors)[0].Field != "original" {
				t.Errorf("problem = %+v, want an error on original", problem)
			}
		})
	}
}

func TestUploadReceipts_RefusesSeparatedOriginal(t *testing.T) {
	s := newUploadServer(t)
	body, contentType := multipartBody(t,
		part{"files", "scan.jpg", smallJPEG(t, 5)},
		part{"note", "note.txt", []byte("an unrelated field")},
		part{"original", "camera.jpg", smallJPEG(t, 9)},
	)
	response := s.call(s.alice, body, contentType)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", response.Code, response.Body)
	}
	if problem := decodeProblem(t, response); problem.Errors == nil || (*problem.Errors)[0].Field != "original" {
		t.Errorf("problem = %+v, want an error on original", problem)
	}
}

func TestUploadReceipts_HEICFileWithExplicitOriginalStoresTheExplicitOne(t *testing.T) {
	s := newUploadServer(t)
	heic, err := os.ReadFile("../ingest/testdata/sample.heic")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	camera, err := os.ReadFile("../ingest/testdata/sample-main10.heic")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	body, contentType := multipartBody(t,
		part{"files", "IMG_0001.HEIC", heic},
		part{"original", "camera.heic", camera},
	)
	got := decodeUpload(t, s.call(s.alice, body, contentType))
	if len(got.Results) != 1 || got.Results[0].Outcome != httpapi.UploadOutcomeCreated {
		t.Fatalf("results = %+v, want one created receipt", got.Results)
	}
	receiptID := *got.Results[0].ReceiptId
	imageID, originalType, originalSize := s.imageOf(t, receiptID)

	// The converted JPEG is the image; the explicit original is what the
	// original columns hold and variant=original serves.
	response := s.get(s.alice, fmt.Sprintf("/api/v1/receipts/%s/images/%s", receiptID, imageID))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("GET image = %d %q, want the converted JPEG", response.Code, response.Header().Get("Content-Type"))
	}
	if originalType != "image/heic" || originalSize != int64(len(camera)) {
		t.Errorf("original columns = %s %d, want the explicit original", originalType, originalSize)
	}
	response = s.get(s.alice, fmt.Sprintf("/api/v1/receipts/%s/images/%s?variant=original", receiptID, imageID))
	if response.Code != http.StatusOK {
		t.Fatalf("GET variant=original = %d, want 200; body: %s", response.Code, response.Body)
	}
	if got := response.Body.Bytes(); !bytes.Equal(got, camera) {
		t.Errorf("served original differs from the explicit camera photo (%d bytes)", len(got))
	}
}

func TestUploadReceipts_DeduplicatesOnTheFileOnly(t *testing.T) {
	s := newUploadServer(t)
	camera, err := os.ReadFile("../ingest/testdata/sample.heic")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	scan, other := smallJPEG(t, 6), smallJPEG(t, 7)
	upload := func(parts ...part) httpapi.UploadResponse {
		body, contentType := multipartBody(t, parts...)
		return decodeUpload(t, s.call(s.alice, body, contentType))
	}

	first := upload(part{"files", "scan.jpg", scan}, part{"original", "camera.heic", camera})
	again := upload(part{"files", "scan.jpg", scan})
	withOtherOriginal := upload(part{"files", "scan.jpg", scan}, part{"original", "other.jpg", smallJPEG(t, 44)})
	reusedOriginal := upload(part{"files", "other.jpg", other}, part{"original", "camera.heic", camera})

	if first.Results[0].Outcome != httpapi.UploadOutcomeCreated {
		t.Fatalf("first = %+v, want created", first.Results[0])
	}
	// A re-sent identical scan is still a duplicate, whatever original
	// arrives beside it.
	if again.Results[0].Outcome != httpapi.UploadOutcomeDuplicate || *again.Results[0].ReceiptId != *first.Results[0].ReceiptId {
		t.Errorf("same scan without original = %+v, want a duplicate of the first", again.Results[0])
	}
	if withOtherOriginal.Results[0].Outcome != httpapi.UploadOutcomeDuplicate || *withOtherOriginal.Results[0].ReceiptId != *first.Results[0].ReceiptId {
		t.Errorf("same scan with another original = %+v, want a duplicate of the first", withOtherOriginal.Results[0])
	}
	// The original's bytes never take part in duplicate detection.
	if reusedOriginal.Results[0].Outcome != httpapi.UploadOutcomeCreated || *reusedOriginal.Results[0].ReceiptId == *first.Results[0].ReceiptId {
		t.Errorf("new scan with a reused original = %+v, want its own receipt", reusedOriginal.Results[0])
	}
	_, originalType, originalSize := s.imageOf(t, *reusedOriginal.Results[0].ReceiptId)
	if originalType != "image/heic" || originalSize != int64(len(camera)) {
		t.Errorf("original columns = %s %d, want the reused camera photo stored", originalType, originalSize)
	}
}

func TestUploadReceipts_UserBGetsNoImageOrOriginalOfUserA(t *testing.T) {
	s := newUploadServer(t)
	camera, err := os.ReadFile("../ingest/testdata/sample.heic")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	scan := smallJPEG(t, 8)
	body, contentType := multipartBody(t, part{"files", "scan.jpg", scan}, part{"original", "camera.heic", camera})
	alice := decodeUpload(t, s.call(s.alice, body, contentType))
	receiptID := *alice.Results[0].ReceiptId
	imageID, _, _ := s.imageOf(t, receiptID)

	for _, suffix := range []string{"", "?variant=original"} {
		response := s.get(s.bob, fmt.Sprintf("/api/v1/receipts/%s/images/%s%s", receiptID, imageID, suffix))
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %q as Bob = %d, want 404", suffix, response.Code)
		}
		if got := response.Body.Bytes(); bytes.Equal(got, scan) || bytes.Equal(got, camera) {
			t.Errorf("GET %q as Bob leaked image bytes", suffix)
		}
	}
	// Alice still gets her image and its original, untouched.
	for _, tc := range []struct {
		suffix string
		want   []byte
	}{
		{"", scan},
		{"?variant=original", camera},
	} {
		response := s.get(s.alice, fmt.Sprintf("/api/v1/receipts/%s/images/%s%s", receiptID, imageID, tc.suffix))
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), tc.want) {
			t.Errorf("GET %q as Alice = %d, want her untouched bytes", tc.suffix, response.Code)
		}
	}
}

func decodeUpload(t *testing.T, response *httptest.ResponseRecorder) httpapi.UploadResponse {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", response.Code, response.Body)
	}
	// The body must be what the contract describes.
	var document any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	schema := loadContract(t).Components.Schemas["UploadResponse"].Value
	if err := schema.VisitJSON(document); err != nil {
		t.Fatalf("body does not match UploadResponse: %v\n%s", err, response.Body)
	}
	var decoded httpapi.UploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return decoded
}

type resultSummary struct {
	Index    int
	FileName string
	Outcome  httpapi.UploadOutcome
	HasID    bool
	Code     httpapi.UploadErrorCode
}

func summarizeUpload(response httpapi.UploadResponse) []resultSummary {
	out := []resultSummary{}
	for _, r := range response.Results {
		s := resultSummary{Index: r.Index, FileName: r.FileName, Outcome: r.Outcome, HasID: r.ReceiptId != nil}
		if r.Error != nil {
			s.Code = r.Error.Code
		}
		out = append(out, s)
	}
	return out
}

func TestUploadReceipts_ReportsEachFile(t *testing.T) {
	s := newUploadServer(t)
	heic, err := os.ReadFile("../ingest/testdata/sample.heic")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	photo := smallJPEG(t, 1)
	body, contentType := multipartBody(t,
		part{"files", "IMG_0001.JPG", photo},
		part{"files", "notes.txt", []byte("milk, bread")},
		part{"files", "IMG_0002.HEIC", heic},
		part{"files", "IMG_0001 copy.JPG", photo},
	)

	got := decodeUpload(t, s.call(s.alice, body, contentType))

	want := []resultSummary{
		{0, "IMG_0001.JPG", httpapi.UploadOutcomeCreated, true, ""},
		{1, "notes.txt", httpapi.UploadOutcomeRejected, false, httpapi.UploadErrorCodeUnsupportedType},
		{2, "IMG_0002.HEIC", httpapi.UploadOutcomeCreated, true, ""},
		{3, "IMG_0001 copy.JPG", httpapi.UploadOutcomeDuplicate, true, ""},
	}
	if diff := cmp.Diff(want, summarizeUpload(got)); diff != "" {
		t.Errorf("results mismatch (-want +got):\n%s", diff)
	}
	if *got.Results[3].ReceiptId != *got.Results[0].ReceiptId {
		t.Errorf("duplicate names %v, want %v", *got.Results[3].ReceiptId, *got.Results[0].ReceiptId)
	}
	if got.Results[1].Error.Message == "" {
		t.Error("rejected file has no message")
	}
}

func TestUploadReceipts_UserBGetsOwnReceiptForUserAsPhoto(t *testing.T) {
	s := newUploadServer(t)
	photo := smallJPEG(t, 7)
	body, contentType := multipartBody(t, part{"files", "a.jpg", photo})
	alice := decodeUpload(t, s.call(s.alice, body, contentType))

	body, contentType = multipartBody(t, part{"files", "b.jpg", photo})
	bob := decodeUpload(t, s.call(s.bob, body, contentType))

	if bob.Results[0].Outcome != httpapi.UploadOutcomeCreated || *bob.Results[0].ReceiptId == *alice.Results[0].ReceiptId {
		t.Errorf("Bob's upload of Alice's photo = %+v, want a new receipt of his own and no trace of hers", bob.Results[0])
	}
}

func TestUploadReceipts_RefusesRequestWithoutFiles(t *testing.T) {
	s := newUploadServer(t)
	body, contentType := multipartBody(t, part{"note", "note.txt", []byte("no photos here")})
	response := s.call(s.alice, body, contentType)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", response.Code, response.Body)
	}
	if problem := decodeProblem(t, response); problem.Errors == nil || (*problem.Errors)[0].Field != "files" {
		t.Errorf("problem = %+v, want an error on files", problem)
	}
}

func TestUploadReceipts_RefusesMoreThanTwentyFiles(t *testing.T) {
	s := newUploadServer(t)
	parts := make([]part, 21)
	for i := range parts {
		parts[i] = part{"files", "photo.jpg", smallJPEG(t, byte(i))}
	}
	body, contentType := multipartBody(t, parts...)
	response := s.call(s.alice, body, contentType)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", response.Code, response.Body)
	}
	// The over-limit request must not have committed any receipt.
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM receipts WHERE user_id = $1", s.alice).Scan(&count); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if count != 0 {
		t.Fatalf("receipts created = %d, want 0", count)
	}
}

func TestUploadReceipts_RefusesBrokenMultipartBody(t *testing.T) {
	s := newUploadServer(t)
	body := bytes.NewBufferString("--boundary\r\nContent-Disposition: form-data; name=\"files\"; filename=\"a.jpg\"\r\n\r\n\xff\xd8\xff")
	response := s.call(s.alice, body, "multipart/form-data; boundary=boundary")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", response.Code, response.Body)
	}
	decodeProblem(t, response)
}

func TestUploadReceipts_RequiresSession(t *testing.T) {
	s := newUploadServer(t)
	body, contentType := multipartBody(t, part{"files", "a.jpg", smallJPEG(t, 1)})
	if response := s.call(uuid.Nil, body, contentType); response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", response.Code)
	}
}
