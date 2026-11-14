package azure_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/extract/azure"
)

// syntheticKey stands in for the resource key. Tests assert that it never
// reaches an error or a log line.
const syntheticKey = "synthetic-key-0123456789abcdef"

// clock is a manual clock: Sleep advances it at once and records the wait.
type clock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func newClock() *clock { return &clock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.slept = append(c.slept, d)
	return nil
}

type meter struct {
	mu    sync.Mutex
	pages int
}

func (m *meter) Count(_ context.Context, pages int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pages += pages
	return nil
}

// request is what the fake service saw, stamped with the manual clock.
type request struct {
	at          time.Time
	method      string
	path        string
	query       string
	key         string
	contentType string
	body        []byte
}

// service is a scripted fake of Document Intelligence. post and get answer
// the n-th request of their kind, from 0.
type service struct {
	t        *testing.T
	clock    *clock
	server   *httptest.Server
	mu       sync.Mutex
	requests []request
	posts    int
	gets     int
	post     func(w http.ResponseWriter, n int)
	get      func(w http.ResponseWriter, n int)
}

func newService(t *testing.T, c *clock) *service {
	t.Helper()
	s := &service{t: t, clock: c}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, request{
			at: c.Now(), method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
			key: r.Header.Get("Ocp-Apim-Subscription-Key"), contentType: r.Header.Get("Content-Type"), body: body,
		})
		var n int
		handler := s.post
		if r.Method == http.MethodPost {
			n = s.posts
			s.posts++
		} else {
			n = s.gets
			s.gets++
			handler = s.get
		}
		s.mu.Unlock()
		handler(w, n)
	}))
	t.Cleanup(s.server.Close)
	// By default the service accepts and finishes at the first poll.
	s.post = s.accept
	s.get = s.succeed
	return s
}

func (s *service) accept(w http.ResponseWriter, _ int) {
	w.Header().Set("Operation-Location", s.server.URL+"/documentintelligence/documentModels/prebuilt-receipt/analyzeResults/3b31320d-0000-4000-8000-000000000001?api-version=2024-11-30")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusAccepted)
}

func (s *service) succeed(w http.ResponseWriter, _ int) {
	data, err := os.ReadFile("testdata/receipt-2024-11-30.json")
	if err != nil {
		s.t.Errorf("read fixture: %v", err)
	}
	_, _ = w.Write(data)
}

func problem(w http.ResponseWriter, status int, code, inner string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":"synthetic","innererror":{"code":%q,"message":"synthetic"}}}`, code, inner)
}

func (s *service) seen(method string) []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []request
	for _, r := range s.requests {
		if r.method == method {
			out = append(out, r)
		}
	}
	return out
}

type fixture struct {
	clock   *clock
	service *service
	meter   *meter
	logs    *bytes.Buffer
	client  *azure.Client
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	c := newClock()
	s := newService(t, c)
	m := &meter{}
	var logs bytes.Buffer
	client, err := azure.New(azure.Options{
		Endpoint: s.server.URL, Key: syntheticKey, HTTP: s.server.Client(), Clock: c, Meter: m,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Timeout: time.Minute,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return fixture{clock: c, service: s, meter: m, logs: &logs, client: client}
}

func smallJPEG(t *testing.T) extract.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 32, 32)), nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return extract.Image{Data: buf.Bytes(), ContentType: "image/jpeg"}
}

func extractErr(t *testing.T, err error) *extract.Error {
	t.Helper()
	var e *extract.Error
	if !errors.As(err, &e) {
		t.Fatalf("error = %v, want an *extract.Error", err)
	}
	if strings.Contains(err.Error(), syntheticKey) {
		t.Fatalf("error exposes the key: %v", err)
	}
	return e
}

func TestExtract_SubmitsPollsAndMaps(t *testing.T) {
	f := newFixture(t)
	f.service.get = func(w http.ResponseWriter, n int) {
		if n < 2 {
			_, _ = w.Write([]byte(`{"status":"running"}`))
			return
		}
		f.service.succeed(w, n)
	}
	img := smallJPEG(t)

	got, err := f.client.Extract(t.Context(), img)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.Model != "prebuilt-receipt" || got.Result.StoreName.Value != "SYNTHETIC MARKET" || len(got.Result.Lines) != 4 || len(got.Raw) == 0 {
		t.Errorf("extraction = model %q, store %q, %d lines, %d raw bytes", got.Model, got.Result.StoreName.Value, len(got.Result.Lines), len(got.Raw))
	}
	posts := f.service.seen(http.MethodPost)
	if len(posts) != 1 {
		t.Fatalf("%d submissions, want 1", len(posts))
	}
	post := posts[0]
	if post.path != "/documentintelligence/documentModels/prebuilt-receipt:analyze" || post.query != "api-version=2024-11-30" ||
		post.key != syntheticKey || post.contentType != "image/jpeg" || !bytes.Equal(post.body, img.Data) {
		t.Errorf("submission = %s?%s, content type %q, %d bytes", post.path, post.query, post.contentType, len(post.body))
	}
	gets := f.service.seen(http.MethodGet)
	if len(gets) != 3 {
		t.Fatalf("%d polls, want 3", len(gets))
	}
	previous := post.at
	for i, get := range gets {
		if get.key != syntheticKey {
			t.Errorf("poll %d did not carry the key", i)
		}
		if gap := get.at.Sub(previous); gap < 2*time.Second {
			t.Errorf("poll %d came %v after the previous request, want at least 2s", i, gap)
		}
		previous = get.at
	}
	if f.meter.pages != 1 {
		t.Errorf("counted %d pages, want 1", f.meter.pages)
	}
}

func TestExtract_SpacesSubmissionsAtLeastOneSecondApart(t *testing.T) {
	f := newFixture(t)
	// Refused at once, so nothing but the limiter separates submissions.
	f.service.post = func(w http.ResponseWriter, _ int) {
		problem(w, http.StatusBadRequest, "InvalidRequest", "InvalidContent")
	}
	for range 3 {
		if _, err := f.client.Extract(t.Context(), smallJPEG(t)); err == nil {
			t.Fatal("extract succeeded against a refusing service")
		}
	}
	posts := f.service.seen(http.MethodPost)
	for i := 1; i < len(posts); i++ {
		if gap := posts[i].at.Sub(posts[i-1].at); gap < time.Second {
			t.Errorf("submission %d came %v after the previous one, want at least 1s", i, gap)
		}
	}
}

func TestExtract_HonoursRetryAfterOn429(t *testing.T) {
	f := newFixture(t)
	f.service.post = func(w http.ResponseWriter, n int) {
		if n == 0 {
			w.Header().Set("Retry-After", "7")
			problem(w, http.StatusTooManyRequests, "429", "")
			return
		}
		f.service.accept(w, n)
	}
	if _, err := f.client.Extract(t.Context(), smallJPEG(t)); err != nil {
		t.Fatalf("extract: %v", err)
	}
	posts := f.service.seen(http.MethodPost)
	if len(posts) != 2 {
		t.Fatalf("%d submissions, want 2", len(posts))
	}
	if gap := posts[1].at.Sub(posts[0].at); gap < 7*time.Second {
		t.Errorf("retried after %v, want at least the 7s Retry-After asked for", gap)
	}
	if f.meter.pages != 1 {
		t.Errorf("counted %d pages, want only the accepted submission", f.meter.pages)
	}
}

func TestExtract_RetriesServerErrorsWithExponentialBackoff(t *testing.T) {
	f := newFixture(t)
	f.service.post = func(w http.ResponseWriter, n int) {
		if n < 3 {
			problem(w, http.StatusServiceUnavailable, "ServiceUnavailable", "")
			return
		}
		f.service.accept(w, n)
	}
	if _, err := f.client.Extract(t.Context(), smallJPEG(t)); err != nil {
		t.Fatalf("extract: %v", err)
	}
	posts := f.service.seen(http.MethodPost)
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		if gap := posts[i+1].at.Sub(posts[i].at); gap < want {
			t.Errorf("retry %d after %v, want at least %v", i+1, gap, want)
		}
	}
}

func TestExtract_TooManyServerErrorsIsRetryableForTheJob(t *testing.T) {
	f := newFixture(t)
	f.service.post = func(w http.ResponseWriter, _ int) {
		problem(w, http.StatusInternalServerError, "InternalServerError", "")
	}
	_, err := f.client.Extract(t.Context(), smallJPEG(t))
	if e := extractErr(t, err); e.Permanent {
		t.Errorf("error = %v, want retryable", err)
	}
	if got := len(f.service.seen(http.MethodPost)); got != 5 {
		t.Errorf("%d submissions, want 5", got)
	}
	if f.meter.pages != 0 {
		t.Errorf("counted %d pages for refused submissions", f.meter.pages)
	}
}

func TestExtract_ClientErrorIsPermanentWithServiceCode(t *testing.T) {
	f := newFixture(t)
	f.service.post = func(w http.ResponseWriter, _ int) {
		problem(w, http.StatusBadRequest, "InvalidRequest", "InvalidContent")
	}
	_, err := f.client.Extract(t.Context(), smallJPEG(t))
	e := extractErr(t, err)
	if !e.Permanent || e.Code != "InvalidContent" || e.Reason == "" {
		t.Errorf("error = %+v, want permanent with code InvalidContent", e)
	}
	if got := len(f.service.seen(http.MethodPost)); got != 1 {
		t.Errorf("%d submissions, want 1", got)
	}
}

func TestExtract_FailedAnalysisIsPermanent(t *testing.T) {
	f := newFixture(t)
	f.service.get = func(w http.ResponseWriter, _ int) {
		_, _ = w.Write([]byte(`{"status":"failed","error":{"code":"InvalidRequest","message":"synthetic","innererror":{"code":"InvalidContent"}}}`))
	}
	_, err := f.client.Extract(t.Context(), smallJPEG(t))
	if e := extractErr(t, err); !e.Permanent || e.Code != "InvalidContent" {
		t.Errorf("error = %+v, want permanent with code InvalidContent", e)
	}
	if f.meter.pages != 1 {
		t.Errorf("counted %d pages, want 1: the submission was accepted", f.meter.pages)
	}
}

func TestExtract_PollHonoursRetryAfterAndRetriesThrottling(t *testing.T) {
	f := newFixture(t)
	f.service.get = func(w http.ResponseWriter, n int) {
		switch n {
		case 0:
			w.Header().Set("Retry-After", "5")
			_, _ = w.Write([]byte(`{"status":"running"}`))
		case 1:
			w.Header().Set("Retry-After", "3")
			problem(w, http.StatusTooManyRequests, "429", "")
		default:
			f.service.succeed(w, n)
		}
	}
	if _, err := f.client.Extract(t.Context(), smallJPEG(t)); err != nil {
		t.Fatalf("extract: %v", err)
	}
	gets := f.service.seen(http.MethodGet)
	if gap := gets[1].at.Sub(gets[0].at); gap < 5*time.Second {
		t.Errorf("second poll after %v, want the 5s Retry-After", gap)
	}
	if gap := gets[2].at.Sub(gets[1].at); gap < 3*time.Second {
		t.Errorf("poll after a 429 came after %v, want at least 3s", gap)
	}
}

func TestExtract_TimesOutRetryably(t *testing.T) {
	f := newFixture(t)
	f.service.get = func(w http.ResponseWriter, _ int) { _, _ = w.Write([]byte(`{"status":"running"}`)) }
	_, err := f.client.Extract(t.Context(), smallJPEG(t))
	if e := extractErr(t, err); e.Permanent {
		t.Errorf("error = %v, want retryable", err)
	}
	if elapsed := f.clock.Now().Sub(newClock().Now()); elapsed > time.Minute {
		t.Errorf("waited %v, more than the 1 minute timeout", elapsed)
	}
}

func TestExtract_RefusesOperationLocationOnAnotherHost(t *testing.T) {
	f := newFixture(t)
	f.service.post = func(w http.ResponseWriter, _ int) {
		w.Header().Set("Operation-Location", "https://attacker.example/analyzeResults/1")
		w.WriteHeader(http.StatusAccepted)
	}
	_, err := f.client.Extract(t.Context(), smallJPEG(t))
	_ = extractErr(t, err)
	if got := len(f.service.seen(http.MethodGet)); got != 0 {
		t.Errorf("%d polls, want none", got)
	}
}

func TestExtract_RefusesImagesOtherThanJPEGOrPNG(t *testing.T) {
	f := newFixture(t)
	_, err := f.client.Extract(t.Context(), extract.Image{Data: []byte("ftypheic"), ContentType: "image/heic"})
	if e := extractErr(t, err); !e.Permanent {
		t.Errorf("error = %v, want permanent", err)
	}
	if len(f.service.seen(http.MethodPost)) != 0 {
		t.Error("submitted an image the adapter must not receive")
	}
}

// noisePNG returns a PNG of random pixels, which hardly compresses, of
// roughly the given size.
func noisePNG(t *testing.T, bytes int) []byte {
	t.Helper()
	side := 1
	for side*side*3 < bytes {
		side += 16
	}
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	if _, err := rand.Read(img.Pix); err != nil {
		t.Fatalf("fill image: %v", err)
	}
	var buf strings.Builder
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return []byte(buf.String())
}

func TestExtract_SendsLargeImageUnderFourMegabytes(t *testing.T) {
	f := newFixture(t)
	data := noisePNG(t, 6<<20)
	if len(data) < 6<<20 {
		t.Fatalf("test image has %d bytes, want at least 6 MB", len(data))
	}
	original := bytes.Clone(data)

	if _, err := f.client.Extract(t.Context(), extract.Image{Data: data, ContentType: "image/png"}); err != nil {
		t.Fatalf("extract: %v", err)
	}
	post := f.service.seen(http.MethodPost)[0]
	if len(post.body) >= 4<<20 || post.contentType != "image/jpeg" {
		t.Errorf("sent %d bytes as %q, want a JPEG under 4 MB", len(post.body), post.contentType)
	}
	if _, err := jpeg.Decode(bytes.NewReader(post.body)); err != nil {
		t.Errorf("sent bytes are not a JPEG: %v", err)
	}
	if !bytes.Equal(data, original) {
		t.Error("the caller's image was changed")
	}
}

func TestExtract_KeyNeverInErrorsOrLogs(t *testing.T) {
	scenarios := map[string]func(s *service){
		"unauthorized": func(s *service) {
			s.post = func(w http.ResponseWriter, _ int) { problem(w, http.StatusUnauthorized, "401", "") }
		},
		"server error": func(s *service) {
			s.post = func(w http.ResponseWriter, _ int) { problem(w, http.StatusBadGateway, "BadGateway", "") }
		},
		"connection refused": func(s *service) { s.server.Close() },
		"failed analysis": func(s *service) {
			s.get = func(w http.ResponseWriter, _ int) { _, _ = w.Write([]byte(`{"status":"failed"}`)) }
		},
	}
	for name, setup := range scenarios {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			setup(f.service)
			_, err := f.client.Extract(t.Context(), smallJPEG(t))
			_ = extractErr(t, err)
			if strings.Contains(f.logs.String(), syntheticKey) {
				t.Errorf("logs expose the key: %s", f.logs.String())
			}
		})
	}
}

func TestNew_RefusesIncompleteOptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for name, opts := range map[string]azure.Options{
		"no endpoint": {Key: syntheticKey, Meter: &meter{}, Logger: logger},
		"no key":      {Endpoint: "https://example.cognitiveservices.azure.com", Meter: &meter{}, Logger: logger},
		"no meter":    {Endpoint: "https://example.cognitiveservices.azure.com", Key: syntheticKey, Logger: logger},
	} {
		if _, err := azure.New(opts); err == nil {
			t.Errorf("%s: New succeeded", name)
		}
	}
}
