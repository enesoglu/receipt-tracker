package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enesoglu/receipt-tracker/internal/extract"
)

const (
	apiVersion = "2024-11-30"
	model      = "prebuilt-receipt"
	// maxResponseBytes bounds a response body read into memory.
	maxResponseBytes = 32 << 20
)

// Meter counts the pages the client submits. Every submission the service
// accepts is counted, whether or not its analysis later succeeds.
type Meter interface {
	Count(ctx context.Context, pages int) error
}

// Clock is the time source of the client. Tests replace it so that waits
// take no real time and can be checked.
type Clock interface {
	Now() time.Time
	// Sleep waits for d or until ctx ends.
	Sleep(ctx context.Context, d time.Duration) error
}

// WallClock is the real Clock.
type WallClock struct{}

// Now returns the current time.
func (WallClock) Now() time.Time { return time.Now() }

// Sleep waits for d or until ctx ends.
func (WallClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Options configures a [Client]. Zero durations take the defaults, which
// follow the free tier's limits.
type Options struct {
	// Endpoint is the resource URL, such as
	// https://example.cognitiveservices.azure.com.
	Endpoint string
	// Key is the resource key. It is sent in a header and never logged or
	// put into an error.
	Key    string
	HTTP   *http.Client
	Clock  Clock
	Meter  Meter
	Logger *slog.Logger
	// SubmitInterval is the least time between two submissions. Default 1 s.
	SubmitInterval time.Duration
	// PollInterval is the least time between two polls of one operation.
	// Default 2 s.
	PollInterval time.Duration
	// Timeout bounds one extraction from the first submission to the
	// result. Default 3 minutes.
	Timeout time.Duration
	// MaxBytes is the largest document the service accepts. Default 4 MB.
	MaxBytes int
	// MaxAttempts bounds the tries of one request that is answered with 429
	// or 5xx. Default 5.
	MaxAttempts int
}

// Client is the Azure Document Intelligence extractor. Its methods may be
// called from several goroutines; requests are spaced across all of them.
type Client struct {
	opts     Options
	endpoint *url.URL

	mu         sync.Mutex
	lastSubmit time.Time
	lastGet    time.Time
}

// New returns a client for the resource at opts.Endpoint.
func New(opts Options) (*Client, error) {
	endpoint, err := url.Parse(strings.TrimRight(opts.Endpoint, "/"))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return nil, errors.New("azure document intelligence endpoint must be an absolute URL")
	}
	if opts.Key == "" || opts.Meter == nil || opts.Logger == nil {
		return nil, errors.New("azure document intelligence needs a key, a page meter and a logger")
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: time.Minute}
	}
	if opts.Clock == nil {
		opts.Clock = WallClock{}
	}
	if opts.SubmitInterval <= 0 {
		opts.SubmitInterval = time.Second
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 3 * time.Minute
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 4 << 20
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	return &Client{opts: opts, endpoint: endpoint}, nil
}

// Provider implements [extract.Extractor].
func (c *Client) Provider() string { return extract.ProviderAzure }

// Extract implements [extract.Extractor]: it submits the image, counts the
// page and polls until the analysis ends.
func (c *Client) Extract(ctx context.Context, img extract.Image) (extract.Extraction, error) {
	if img.ContentType != "image/jpeg" && img.ContentType != "image/png" {
		return extract.Extraction{}, extract.NewError(true, "", "Only JPEG and PNG images can be read.", fmt.Errorf("content type %q", img.ContentType))
	}
	body, contentType := img.Data, img.ContentType
	if len(body) > c.opts.MaxBytes {
		smaller, err := shrink(body, c.opts.MaxBytes)
		if err != nil {
			return extract.Extraction{}, extract.NewError(true, "", "The image is too large to be read.", err)
		}
		body, contentType = smaller, "image/jpeg"
	}
	deadline := c.opts.Clock.Now().Add(c.opts.Timeout)

	location, retryAfter, err := c.submit(ctx, deadline, body, contentType)
	if err != nil {
		return extract.Extraction{}, err
	}
	if err := c.opts.Meter.Count(ctx, 1); err != nil {
		// The page is used either way; failing to count it must not throw
		// away the result it pays for.
		c.opts.Logger.Error("count extraction page", "provider", extract.ProviderAzure, "error", err)
	}
	return c.poll(ctx, deadline, location, retryAfter)
}

// wait sleeps until at least interval has passed since *last, then records
// the time of the request about to be made.
func (c *Client) wait(ctx context.Context, deadline time.Time, last *time.Time, interval time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !last.IsZero() {
		if pause := last.Add(interval).Sub(c.opts.Clock.Now()); pause > 0 {
			if err := c.sleep(ctx, deadline, pause); err != nil {
				return err
			}
		}
	}
	*last = c.opts.Clock.Now()
	return nil
}

// sleep waits unless the wait would pass the deadline of the extraction.
func (c *Client) sleep(ctx context.Context, deadline time.Time, d time.Duration) error {
	if c.opts.Clock.Now().Add(d).After(deadline) {
		return extract.NewError(false, "", "Reading the receipt took too long.", errors.New("extraction deadline reached"))
	}
	if err := c.opts.Clock.Sleep(ctx, d); err != nil {
		return extract.NewError(false, "", "Reading the receipt was interrupted.", err)
	}
	return nil
}

// response is a finished HTTP exchange.
type response struct {
	status     int
	header     http.Header
	body       []byte
	retryAfter time.Duration
}

// do sends one request and reads the whole answer. Network failures are
// retryable. The request carries the key only in its header, and errors
// never include the request.
func (c *Client) do(ctx context.Context, method, target string, body []byte, contentType string) (response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return response{}, extract.NewError(true, "", "The extraction request could not be built.", errors.New("invalid request URL"))
	}
	request.Header.Set("Ocp-Apim-Subscription-Key", c.opts.Key)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	answer, err := c.opts.HTTP.Do(request)
	if err != nil {
		// A *url.Error names the URL, which holds no secret; the key is in
		// a header and is never part of the error.
		return response{}, extract.NewError(false, "", "The extraction service could not be reached.", err)
	}
	defer func() { _ = answer.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(answer.Body, maxResponseBytes))
	if err != nil {
		return response{}, extract.NewError(false, "", "The extraction service did not answer completely.", err)
	}
	return response{status: answer.StatusCode, header: answer.Header, body: data, retryAfter: retryAfterOf(answer.Header, c.opts.Clock.Now())}, nil
}

// retryAfterOf reads Retry-After as seconds or as an HTTP date.
func retryAfterOf(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// backoff is the wait before try number attempt (from 1): 1 s, 2 s, 4 s, ...
// or what the service asked for, whichever is longer.
func backoff(attempt int, asked time.Duration) time.Duration {
	wait := time.Second << (attempt - 1)
	return max(wait, asked)
}

// failure turns an answer that is not a success into an extraction error.
func failure(r response) error {
	var envelope struct {
		Error *serviceError `json:"error"`
	}
	_ = json.Unmarshal(r.body, &envelope)
	code := envelope.Error.code()
	cause := fmt.Errorf("extraction service answered %d", r.status)
	switch {
	case retryable(r.status):
		return extract.NewError(false, code, "The extraction service is busy or unavailable.", cause)
	case r.status == http.StatusUnauthorized || r.status == http.StatusForbidden:
		return extract.NewError(true, code, "The extraction service is not configured correctly.", cause)
	default:
		return extract.NewError(true, code, "The extraction service could not read this image.", cause)
	}
}

func (c *Client) submit(ctx context.Context, deadline time.Time, body []byte, contentType string) (*url.URL, time.Duration, error) {
	target := c.endpoint.JoinPath("documentintelligence", "documentModels", model+":analyze")
	target.RawQuery = url.Values{"api-version": {apiVersion}}.Encode()
	var last error
	for attempt := 1; attempt <= c.opts.MaxAttempts; attempt++ {
		if err := c.wait(ctx, deadline, &c.lastSubmit, c.opts.SubmitInterval); err != nil {
			return nil, 0, err
		}
		r, err := c.do(ctx, http.MethodPost, target.String(), body, contentType)
		switch {
		case err != nil:
			last = err
		case r.status == http.StatusAccepted:
			location, err := c.operationLocation(r.header.Get("Operation-Location"))
			if err != nil {
				return nil, 0, err
			}
			return location, r.retryAfter, nil
		case retryable(r.status):
			last = failure(r)
			if attempt < c.opts.MaxAttempts {
				if err := c.sleep(ctx, deadline, backoff(attempt, r.retryAfter)); err != nil {
					return nil, 0, err
				}
			}
			continue
		default:
			return nil, 0, failure(r)
		}
		if attempt < c.opts.MaxAttempts {
			if err := c.sleep(ctx, deadline, backoff(attempt, 0)); err != nil {
				return nil, 0, err
			}
		}
	}
	return nil, 0, last
}

// operationLocation checks that the result URL points at the configured
// resource, because the key is sent to it.
func (c *Client) operationLocation(value string) (*url.URL, error) {
	location, err := url.Parse(value)
	if err != nil || location.Scheme != c.endpoint.Scheme || location.Host != c.endpoint.Host {
		return nil, extract.NewError(false, "", "The extraction service gave an unexpected answer.", errors.New("operation location does not match the endpoint"))
	}
	return location, nil
}

func (c *Client) poll(ctx context.Context, deadline time.Time, location *url.URL, retryAfter time.Duration) (extract.Extraction, error) {
	failures := 0
	pause := max(c.opts.PollInterval, retryAfter)
	for {
		if err := c.sleep(ctx, deadline, pause); err != nil {
			return extract.Extraction{}, err
		}
		if err := c.wait(ctx, deadline, &c.lastGet, time.Second); err != nil {
			return extract.Extraction{}, err
		}
		r, err := c.do(ctx, http.MethodGet, location.String(), nil, "")
		if err == nil && r.status != http.StatusOK {
			err = failure(r)
			if !retryable(r.status) {
				return extract.Extraction{}, err
			}
		}
		if err != nil {
			failures++
			if failures >= c.opts.MaxAttempts {
				return extract.Extraction{}, err
			}
			pause = max(c.opts.PollInterval, backoff(failures, r.retryAfter))
			continue
		}
		failures = 0
		var op operation
		if err := json.Unmarshal(r.body, &op); err != nil {
			return extract.Extraction{}, extract.NewError(false, "", "The extraction service gave an unexpected answer.", err)
		}
		switch op.Status {
		case "notStarted", "running":
			pause = max(c.opts.PollInterval, r.retryAfter)
		case "succeeded":
			result, err := parse(r.body)
			if err != nil {
				return extract.Extraction{}, extract.NewError(true, "", "The extraction result could not be read.", err)
			}
			return extract.Extraction{Model: model, Raw: r.body, Result: result}, nil
		case "failed":
			return extract.Extraction{}, extract.NewError(true, op.Error.code(), "The extraction service could not read this image.", errors.New("analysis failed"))
		default:
			return extract.Extraction{}, extract.NewError(true, op.Error.code(), "The extraction service stopped reading this image.", fmt.Errorf("analysis status %q", op.Status))
		}
	}
}
