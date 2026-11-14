package httpapi_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/ingest"
	"github.com/enesoglu/receipt-tracker/internal/storage/local"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

type uploadServer struct {
	t      *testing.T
	server *httpapi.Server
	call   func(user uuid.UUID, body io.Reader, contentType string) *httptest.ResponseRecorder
	alice  uuid.UUID
	bob    uuid.UUID
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
	server := httpapi.New(":0", logger, http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{Uploads: service})
	s := uploadServer{t: t, server: server, alice: alice, bob: bob}
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
