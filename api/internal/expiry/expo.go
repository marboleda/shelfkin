package expiry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// DefaultExpoPushURL is Expo's push send endpoint.
const DefaultExpoPushURL = "https://exp.host/--/api/v2/push/send"

// MaxBatchSize is the most messages Expo accepts in one request.
const MaxBatchSize = 100

// MaxRetries is how many times a request that got HTTP 429 or 5xx (or no
// response at all) is retried before giving up.
const MaxRetries = 3

// DefaultBackoff is the wait before the first retry; it doubles each time.
const DefaultBackoff = time.Second

// maxRetryAfter caps how long a Retry-After header can make the client wait.
const maxRetryAfter = 30 * time.Second

// Ticket statuses and the ticket error this job acts on.
const (
	TicketOK                 = "ok"
	TicketError              = "error"
	ErrorDeviceNotRegistered = "DeviceNotRegistered"
)

// Message is one Expo push message to one device.
type Message struct {
	To    string            `json:"to"`
	Title string            `json:"title"`
	Body  string            `json:"body"`
	Data  map[string]string `json:"data,omitempty"`
	Sound string            `json:"sound,omitempty"`
}

// Ticket is Expo's answer for one message. Message can quote the push token,
// so never log it; log Details.Error instead.
type Ticket struct {
	Status  string `json:"status"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details"`
}

// Sender sends one batch of at most MaxBatchSize messages. On success it
// returns exactly one ticket per message, in the same order. An error means
// Expo did not accept the batch and no ticket is known for any of it.
type Sender interface {
	Send(ctx context.Context, msgs []Message) ([]Ticket, error)
}

// ExpoClient sends pushes with the Expo Push HTTP API.
type ExpoClient struct {
	// URL is the push send endpoint; DefaultExpoPushURL when empty.
	URL string
	// AccessToken, when set, is sent as a bearer token (Expo's "enhanced
	// push security").
	AccessToken string
	// HTTPClient is http.DefaultClient when nil.
	HTTPClient *http.Client
	// Backoff is the wait before the first retry; DefaultBackoff when zero.
	Backoff time.Duration
}

// HTTPError is a non-2xx response from Expo. It holds only the status and
// Expo's error codes, never the response body, which can quote push tokens.
type HTTPError struct {
	StatusCode int
	Codes      []string
}

func (e *HTTPError) Error() string {
	if len(e.Codes) == 0 {
		return fmt.Sprintf("expo push: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("expo push: HTTP %d %v", e.StatusCode, e.Codes)
}

func (e *HTTPError) retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

type expoResponse struct {
	Data   []Ticket `json:"data"`
	Errors []struct {
		Code string `json:"code"`
	} `json:"errors"`
}

// Send implements Sender. It retries HTTP 429, 5xx and transport errors up to
// MaxRetries times, waiting Backoff, then twice as long each time (or what a
// Retry-After header asks for, up to 30s).
func (c *ExpoClient) Send(ctx context.Context, msgs []Message) ([]Ticket, error) {
	if len(msgs) == 0 {
		return nil, nil
	}
	if len(msgs) > MaxBatchSize {
		return nil, fmt.Errorf("expo push: %d messages in one batch, at most %d allowed", len(msgs), MaxBatchSize)
	}
	body, err := json.Marshal(msgs)
	if err != nil {
		return nil, fmt.Errorf("expo push: encoding messages: %w", err)
	}
	wait := c.Backoff
	if wait <= 0 {
		wait = DefaultBackoff
	}
	for attempt := 0; ; attempt++ {
		tickets, retryAfter, err := c.post(ctx, body, len(msgs))
		if err == nil {
			return tickets, nil
		}
		var httpErr *HTTPError
		retryable := !errors.As(err, &httpErr) || httpErr.retryable()
		if !retryable || attempt == MaxRetries || ctx.Err() != nil {
			return nil, err
		}
		delay := wait
		if retryAfter > 0 {
			delay = min(retryAfter, maxRetryAfter)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		wait *= 2
	}
}

// post makes one request. It returns the Retry-After delay, if any, along
// with an error.
func (c *ExpoClient) post(ctx context.Context, body []byte, n int) ([]Ticket, time.Duration, error) {
	url := c.URL
	if url == "" {
		url = DefaultExpoPushURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		// A bad URL; retrying cannot help, so report it as a non-retryable
		// HTTPError with no status.
		return nil, 0, &HTTPError{Codes: []string{"invalid request URL"}}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// Transport errors (connection refused, timeouts) are retried. The
		// error names the URL but not the body.
		return nil, 0, fmt.Errorf("expo push: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("expo push: reading response: %w", err)
	}

	var parsed expoResponse
	decodeErr := json.Unmarshal(raw, &parsed)
	var codes []string
	for _, e := range parsed.Errors {
		codes = append(codes, e.Code)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, retryAfter(resp.Header.Get("Retry-After")), &HTTPError{StatusCode: resp.StatusCode, Codes: codes}
	}
	if decodeErr != nil {
		return nil, 0, &HTTPError{StatusCode: resp.StatusCode, Codes: []string{"invalid response body"}}
	}
	if len(codes) > 0 {
		return nil, 0, &HTTPError{StatusCode: resp.StatusCode, Codes: codes}
	}
	if len(parsed.Data) != n {
		return nil, 0, &HTTPError{StatusCode: resp.StatusCode, Codes: []string{
			fmt.Sprintf("got %d tickets for %d messages", len(parsed.Data), n),
		}}
	}
	return parsed.Data, 0, nil
}

// retryAfter parses a Retry-After header given in seconds; 0 if absent or
// not a number of seconds.
func retryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
