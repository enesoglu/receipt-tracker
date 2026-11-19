//go:build e2e

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
)

// TestE2E_RealServerWorkflowAndContract uses a compiled production entry point,
// real PostgreSQL and disk storage. After database creation, all setup and
// assertions use HTTP. Restarting with a second development identity signs in
// both users without introducing a test-only authentication route or SQL seed.
func TestE2E_RealServerWorkflowAndContract(t *testing.T) {
	pool := dbtest.NewEmpty(t)
	databaseURL := pool.Config().ConnConfig.ConnString()
	pool.Close()
	doc, err := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	s := &e2eServer{t: t, base: "http://" + address, router: router, visited: map[string]bool{}}
	// Resolve the contract's relative server URL against this listener.
	doc.Servers[0].URL = s.base + doc.Servers[0].URL
	images := t.TempDir()
	stop := s.start(address, databaseURL, images, "alice@example.com")
	alice := s.signIn()
	var owner httpapi.User
	s.json(alice, "GET", "/me", nil, 200, &owner)
	stop()
	stop = s.start(address, databaseURL, images, "bob@example.com")
	defer stop()
	bob := s.signIn()
	var other httpapi.User
	s.json(bob, "GET", "/me", nil, 200, &other)
	if owner.Id == other.Id {
		t.Fatal("development sign-ins returned the same identity")
	}

	s.json(alice, "PUT", "/settings", map[string]string{"defaultCurrency": "TRY", "defaultReceiptTitle": "E2E shopping"}, 200, nil)
	var category httpapi.Category
	s.json(alice, "POST", "/categories", map[string]string{"name": "E2E category"}, 201, &category)
	s.json(alice, "PUT", "/categories/"+category.Id.String(), map[string]string{"name": "E2E groceries"}, 200, nil)

	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	for i, data := range syntheticPhotos(t) {
		part, err := form.CreateFormFile("files", fmt.Sprintf("synthetic-%d.png", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	var uploaded httpapi.UploadResponse
	s.decode(s.call(alice, "POST", "/receipts", &upload, form.FormDataContentType(), 200), &uploaded)
	if len(uploaded.Results) != 3 {
		t.Fatalf("upload results = %+v", uploaded.Results)
	}
	var ids []string
	for _, result := range uploaded.Results {
		if result.Outcome != "created" || result.ReceiptId == nil {
			t.Fatalf("upload was not accepted: %+v", result)
		}
		ids = append(ids, result.ReceiptId.String())
	}
	s.waitProcessed(alice, ids)

	var flags httpapi.ReviewFlagPage
	s.json(alice, "GET", "/review-flags", nil, 200, &flags)
	if flags.TotalOpen == 0 {
		t.Fatal("synthetic batch did not exercise review")
	}
	for range 50 {
		s.json(alice, "GET", "/review-flags", nil, 200, &flags)
		if flags.TotalOpen == 0 {
			break
		}
		var flag struct{ ID, Kind string }
		encoded, err := json.Marshal(flags.Items[0].Flag)
		if err != nil {
			t.Fatal(err)
		}
		s.decode(encoded, &flag)
		action := "accept"
		switch flag.Kind {
		case "low_confidence", "new_store":
			action = "confirm"
		case "uncertain_match", "code_conflict":
			action = "different_product"
		}
		s.json(bob, "POST", "/review-flags/"+flag.ID+"/resolve", map[string]string{"action": action}, 404, nil)
		s.json(alice, "POST", "/review-flags/"+flag.ID+"/resolve", map[string]string{"action": action}, 200, nil)
	}
	s.json(alice, "GET", "/review-flags", nil, 200, &flags)
	if flags.TotalOpen != 0 {
		t.Fatalf("review did not finish: %d flags", flags.TotalOpen)
	}

	// Give the missing-date sample a date, and retain a populated report for A.
	for _, id := range ids {
		s.json(alice, "PUT", "/receipts/"+id, map[string]string{"title": "E2E shopping", "currency": "TRY", "storeName": "E2E market", "purchasedOn": "2026-11-03", "total": "120.00"}, 200, nil)
	}
	var receipt httpapi.Receipt
	s.json(alice, "GET", "/receipts/"+ids[0], nil, 200, &receipt)
	var pipeline struct {
		Items []json.RawMessage `json:"items"`
	}
	s.json(alice, "GET", "/pipeline?ids="+ids[0], nil, 200, &pipeline)
	if len(pipeline.Items) != 1 {
		t.Fatalf("pipeline items for own receipt = %d, want 1", len(pipeline.Items))
	}
	// Another user's ids are unknown to bob: ignored, never revealed.
	s.json(bob, "GET", "/pipeline?ids="+ids[0], nil, 200, &pipeline)
	if len(pipeline.Items) != 0 {
		t.Fatalf("pipeline items for another user's receipt = %d, want 0", len(pipeline.Items))
	}
	if len(receipt.Images) != 1 || len(receipt.LineItems) == 0 {
		t.Fatalf("processed receipt missing images or lines: %+v", receipt)
	}
	imagePath := "/receipts/" + ids[0] + "/images/" + receipt.Images[0].Id.String()
	s.call(alice, "GET", imagePath, nil, "", 200)
	s.call(bob, "GET", imagePath, nil, "", 404)
	linePath := "/receipts/" + ids[0] + "/line-items"
	line := map[string]string{"name": "E2E rice", "quantity": "1", "unitPrice": "10.00", "lineTotal": "10.00"}
	s.json(alice, "POST", linePath, line, 201, &receipt)
	lineID := receipt.LineItems[len(receipt.LineItems)-1].Id.String()
	s.json(alice, "PUT", linePath+"/"+lineID, line, 200, nil)
	s.json(alice, "DELETE", linePath+"/"+lineID, nil, 200, nil)

	// The suggestion model is unconfigured here: the reads stay honest, and a
	// decision on a suggestion that does not exist is still contract-shaped.
	var suggestions httpapi.ReceiptSuggestions
	s.json(alice, "GET", "/receipts/"+ids[0]+"/suggestions", nil, 200, &suggestions)
	if len(suggestions.Items) != 0 {
		t.Fatalf("unconfigured deployment listed suggestions: %+v", suggestions.Items)
	}
	s.json(bob, "GET", "/receipts/"+ids[0]+"/suggestions", nil, 404, nil)
	var suggestionStatus httpapi.SuggestionsStatus
	s.json(alice, "GET", "/suggestions/status", nil, 200, &suggestionStatus)
	if suggestionStatus.Available || suggestionStatus.Offline {
		t.Fatalf("unconfigured suggestion status = %+v", suggestionStatus)
	}
	const unknownSuggestion = "0193a1b2-0000-7000-8000-000000000901"
	s.json(alice, "POST", "/receipts/"+ids[0]+"/suggestions/"+unknownSuggestion+"/accept", nil, 404, nil)
	s.json(alice, "POST", "/receipts/"+ids[0]+"/suggestions/"+unknownSuggestion+"/ignore", nil, 404, nil)

	var products httpapi.ProductPage
	s.json(alice, "GET", "/products", nil, 200, &products)
	if len(products.Items) < 2 {
		t.Fatal("batch did not create two products to merge")
	}
	target, source := products.Items[0].Id.String(), products.Items[1].Id.String()
	s.json(alice, "PUT", "/products/"+target, map[string]string{"canonicalName": "E2E product", "categoryId": category.Id.String()}, 200, nil)
	s.json(alice, "POST", "/products/"+target+"/merge", map[string]string{"sourceProductId": source}, 200, nil)
	var product httpapi.Product
	s.json(alice, "GET", "/products/"+target, nil, 200, &product)
	if len(product.Aliases) < 2 {
		t.Fatal("merged product did not retain both aliases")
	}
	s.json(alice, "POST", "/products/"+target+"/split", map[string]any{"aliasIds": []string{product.Aliases[0].Id.String()}, "canonicalName": "E2E split product"}, 201, nil)
	var history httpapi.PriceHistory
	s.json(alice, "GET", "/products/"+target+"/price-history", nil, 200, &history)
	if len(history.Points) == 0 {
		t.Fatal("price history is empty after merge and split")
	}
	var spending httpapi.MonthlySpending
	reportPath := "/reports/monthly-spending?from=2026-11&to=2026-11"
	s.json(alice, "GET", reportPath, nil, 200, &spending)
	if len(spending.Months) != 1 || len(spending.Months[0].Totals) != 1 || spending.Months[0].Totals[0].Amount != "360.00" {
		t.Fatalf("monthly spending = %+v, want TRY 360.00", spending)
	}
	var stores httpapi.StorePage
	s.json(alice, "GET", "/stores", nil, 200, &stores)
	s.json(alice, "PUT", "/stores/"+stores.Items[0].Id.String(), map[string]string{"name": "E2E renamed market"}, 200, nil)

	// Repeat all data reads with B's real session. Lists may contain B's own
	// seeded categories/settings, but no A resource id or private name.
	private := []string{owner.Id.String(), category.Id.String(), target, source, "E2E"}
	private = append(private, ids...)
	for _, path := range []string{"/receipts", "/receipts?ids=" + strings.Join(ids, ","), "/review-flags", "/products", "/stores", "/categories", "/settings", "/usage", "/storage", reportPath, "/reports/month-breakdown?month=2026-11&groupBy=category", "/reports/month-breakdown?month=2026-11&groupBy=store"} {
		s.json(alice, "GET", path, nil, 200, nil)
		body := s.call(bob, "GET", path, nil, "", 200)
		for _, value := range private {
			if bytes.Contains(body, []byte(value)) {
				t.Fatalf("B read A's data at %s: %s", path, body)
			}
		}
	}
	s.json(bob, "GET", reportPath, nil, 200, &spending)
	if diff := cmp.Diff([]httpapi.CurrencyTotal{}, spending.Months[0].Totals); diff != "" {
		t.Fatalf("B spending (-want +got): %s", diff)
	}
	for _, id := range ids {
		s.json(bob, "GET", "/receipts/"+id, nil, 404, nil)
	}
	for _, path := range []string{"/products/" + target, "/products/" + target + "/price-history"} {
		s.json(bob, "GET", path, nil, 404, nil)
	}
	// Drive is deliberately unconfigured; protocol errors must still conform.
	s.json(alice, "GET", "/storage/drive/connect", nil, 503, nil)
	s.json(alice, "GET", "/storage/drive/callback?state=invalid&code=invalid", nil, 400, nil)
	s.json(bob, "GET", "/storage/drive/connect", nil, 404, nil)
	s.json(bob, "GET", "/storage/drive/callback?state=invalid&code=invalid", nil, 404, nil)
	s.json(bob, "DELETE", "/storage/connection", nil, 404, nil)
	s.json(alice, "DELETE", "/storage/connection", nil, 204, nil)
	s.json(alice, "POST", "/receipts/"+ids[0]+"/reprocess", nil, 200, nil)
	s.json(alice, "DELETE", "/receipts/"+ids[0], nil, 204, nil)
	s.json(alice, "DELETE", "/categories/"+category.Id.String()+"?force=true", nil, 204, nil)
	s.json(alice, "POST", "/auth/logout", nil, 204, nil)
	s.json(alice, "GET", "/me", nil, 401, nil)
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if !s.visited[op.OperationID] {
				t.Errorf("no real response validated for %s (%s %s)", op.OperationID, method, path)
			}
		}
	}
}

type e2eServer struct {
	t       *testing.T
	base    string
	router  routers.Router
	visited map[string]bool
}

func (s *e2eServer) start(address, databaseURL, images, email string) func() {
	s.t.Helper()
	binary := "server"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	binary, err := filepath.Abs(filepath.Join("..", "..", "bin", binary))
	if err != nil {
		s.t.Fatal(err)
	}
	log, err := os.CreateTemp(s.t.TempDir(), "server-*.log")
	if err != nil {
		s.t.Fatal(err)
	}
	// Only synthetic configuration reaches this process; no .env is loaded.
	cmd := exec.CommandContext(s.t.Context(), binary)
	cmd.Env = append(os.Environ(), "APP_ENV=dev", "APP_LISTEN_ADDR="+address, "APP_BASE_URL="+s.base,
		"DATABASE_URL="+databaseURL, "APP_ENCRYPTION_KEY="+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)),
		"ALLOWED_EMAILS=alice@example.com,bob@example.com", "ADMIN_EMAIL=alice@example.com", "DEV_LOGIN_EMAIL="+email,
		"GOOGLE_CLIENT_ID=", "GOOGLE_CLIENT_SECRET=", "STORAGE_BACKEND=local", "STORAGE_LOCAL_DIR="+images,
		"EXTRACTOR=fake", "AZURE_DI_MONTHLY_PAGES=500", "BACKUP_DESTINATION=none", "BACKUP_HEARTBEAT_URL=", "DRIVE_ROOT_FOLDER_ID=")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		s.t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = log.Close()
	}
	s.t.Cleanup(stop)
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		req, err := http.NewRequestWithContext(s.t.Context(), "GET", s.base+"/healthz", nil)
		if err != nil {
			s.t.Fatal(err)
		}
		response, err := client.Do(req)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == 200 {
				return stop
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	data, _ := os.ReadFile(log.Name())
	s.t.Fatalf("server did not become healthy: %s", data)
	return stop
}

func (s *e2eServer) signIn() *http.Client {
	s.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		s.t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(s.t.Context(), "GET", s.base+"/auth/dev/login", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 303 || len(jar.Cookies(req.URL)) != 1 {
		s.t.Fatalf("sign in: status %d, cookies %d", response.StatusCode, len(jar.Cookies(req.URL)))
	}
	return client
}

func (s *e2eServer) call(client *http.Client, method, path string, body io.Reader, contentType string, status int) []byte {
	s.t.Helper()
	req, err := http.NewRequestWithContext(s.t.Context(), method, s.base+"/api/v1"+path, body)
	if err != nil {
		s.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	if response.StatusCode != status {
		s.t.Fatalf("%s %s: status %d, want %d: %s", method, path, response.StatusCode, status, data)
	}
	route, parameters, err := s.router.FindRoute(req)
	if err != nil {
		s.t.Fatal(err)
	}
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: parameters, Route: route},
		Status:                 response.StatusCode, Header: response.Header, Body: io.NopCloser(bytes.NewReader(data)), Options: &openapi3filter.Options{IncludeResponseStatus: true},
	}
	if err := openapi3filter.ValidateResponse(s.t.Context(), input); err != nil {
		s.t.Fatalf("%s %s violates OpenAPI: %v", method, path, err)
	}
	s.visited[route.Operation.OperationID] = true
	return data
}

func (s *e2eServer) json(client *http.Client, method, path string, body any, status int, result any) {
	s.t.Helper()
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		input = bytes.NewReader(data)
	}
	data := s.call(client, method, path, input, "application/json", status)
	if result != nil {
		s.decode(data, result)
	}
}

func (s *e2eServer) decode(data []byte, result any) {
	s.t.Helper()
	if err := json.Unmarshal(data, result); err != nil {
		s.t.Fatal(err)
	}
}

func (s *e2eServer) waitProcessed(client *http.Client, ids []string) {
	s.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		var page httpapi.ReceiptPage
		s.json(client, "GET", "/receipts?ids="+strings.Join(ids, ","), nil, 200, &page)
		done := len(page.Items) == len(ids)
		for _, receipt := range page.Items {
			if receipt.Status == "failed" || receipt.Status == "waiting_quota" {
				s.t.Fatalf("processing failed: %+v", receipt)
			}
			done = done && (receipt.Status == "ready" || receipt.Status == "needs_review")
		}
		if done {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatal("processing did not finish")
}

func syntheticPhotos(t *testing.T) [][]byte {
	t.Helper()
	photos := make([][]byte, 3)
	for n := range 256 {
		img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		img.SetNRGBA(0, 0, color.NRGBA{R: uint8(n), A: 255})
		var data bytes.Buffer
		if err := png.Encode(&data, img); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data.Bytes())
		photos[int(digest[0])%3] = data.Bytes()
		if photos[0] != nil && photos[1] != nil && photos[2] != nil {
			return photos
		}
	}
	t.Fatal("could not generate all three fake extraction samples")
	return nil
}
