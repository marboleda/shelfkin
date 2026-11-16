package expiry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultExpoPushURL is Expo's push send endpoint.
const DefaultExpoPushURL = "https://exp.host/--/api/v2/push/send"

// DefaultExpoReceiptsURL is Expo's push receipts endpoint.
const DefaultExpoReceiptsURL = "https://exp.host/--/api/v2/push/getReceipts"

// MaxBatchSize is the most messages Expo accepts in one request.
const MaxBatchSize = 100

// MaxReceiptBatchSize is the most ticket IDs Expo accepts in one receipts
// request.
const MaxReceiptBatchSize = 1000

// MaxRetries is how many times a request that got HTTP 429 or 5xx (or no
// response at all) is retried before giving up.
const MaxRetries = 3

// DefaultBackoff is the wait before the first retry; it doubles each time.
const DefaultBackoff = time.Second

// maxRetryAfter caps how long a Retry-After header can make the client wait.
const maxRetryAfter = 30 * time.Second

// Ticket and receipt statuses and the error this job acts on.
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

// Receipt is Expo's delivery result for one ticket: Status is TicketOK or
// TicketError. Like Ticket.Message, Message can quote the push token, so
// never log it; log Details.Error instead.
type Receipt struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details"`
}

// Sender is the job's view of Expo: it sends pushes and fetches their
// receipts. Tests use a fake; only ExpoClient talks to Expo.
type Sender interface {
	// Send sends one batch of at most MaxBatchSize messages. On success it
	// returns exactly one ticket per message, in the same order. An error
	// means Expo did not accept the batch and no ticket is known for any of
	// it.
	Send(ctx context.Context, msgs []Message) ([]Ticket, error)
	// Receipts fetches the receipts for at most MaxReceiptBatchSize ticket
	// IDs, keyed by ticket ID. A ticket whose receipt Expo does not have (not
	// ready yet, or no longer kept) is missing from the map.
	Receipts(ctx context.Context, ticketIDs []string) (map[string]Receipt, error)
}

// ExpoClient sends pushes and fetches receipts with the Expo Push HTTP API.
type ExpoClient struct {
	// URL is the push send endpoint; DefaultExpoPushURL when empty.
	URL string
	// ReceiptsURL is the receipts endpoint. When empty it is
	// ReceiptsURLFor(URL), which for Expo's own URL is
	// DefaultExpoReceiptsURL.
	ReceiptsURL string
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

type expoErrors struct {
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
	var resp struct {
		Data []Ticket `json:"data"`
	}
	check := func(status int) error {
		if len(resp.Data) != len(msgs) {
			return &HTTPError{StatusCode: status, Codes: []string{
				fmt.Sprintf("got %d tickets for %d messages", len(resp.Data), len(msgs)),
			}}
		}
		return nil
	}
	if err := c.do(ctx, c.sendURL(), body, &resp, check); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// Receipts implements Sender. It retries like Send.
func (c *ExpoClient) Receipts(ctx context.Context, ticketIDs []string) (map[string]Receipt, error) {
	if len(ticketIDs) == 0 {
		return map[string]Receipt{}, nil
	}
	if len(ticketIDs) > MaxReceiptBatchSize {
		return nil, fmt.Errorf("expo push: %d ticket IDs in one receipts request, at most %d allowed", len(ticketIDs), MaxReceiptBatchSize)
	}
	body, err := json.Marshal(map[string][]string{"ids": ticketIDs})
	if err != nil {
		return nil, fmt.Errorf("expo push: encoding ticket IDs: %w", err)
	}
	var resp struct {
		Data map[string]Receipt `json:"data"`
	}
	if err := c.do(ctx, c.receiptsURL(), body, &resp, nil); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		resp.Data = map[string]Receipt{}
	}
	return resp.Data, nil
}

func (c *ExpoClient) sendURL() string {
	if c.URL == "" {
		return DefaultExpoPushURL
	}
	return c.URL
}

func (c *ExpoClient) receiptsURL() string {
	if c.ReceiptsURL != "" {
		return c.ReceiptsURL
	}
	return ReceiptsURLFor(c.sendURL())
}

// ReceiptsURLFor returns the receipts endpoint that sits next to the push
// send endpoint pushURL: pushURL with its last path segment replaced by
// "getReceipts" and any query dropped. For DefaultExpoPushURL it is
// DefaultExpoReceiptsURL. A pushURL that does not parse is returned as is,
// so the request fails rather than going somewhere unexpected.
func ReceiptsURLFor(pushURL string) string {
	u, err := url.Parse(pushURL)
	if err != nil {
		return pushURL
	}
	dir := u.Path[:strings.LastIndex(u.Path, "/")+1]
	if dir == "" {
		dir = "/"
	}
	u.Path = dir + "getReceipts"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// do POSTs body to url and decodes a 2xx response into out, retrying as
// described on Send. check, if not nil, validates the decoded response; its
// error is not retried.
func (c *ExpoClient) do(ctx context.Context, url string, body []byte, out any, check func(status int) error) error {
	wait := c.Backoff
	if wait <= 0 {
		wait = DefaultBackoff
	}
	for attempt := 0; ; attempt++ {
		retryAfter, err := c.post(ctx, url, body, out, check)
		if err == nil {
			return nil
		}
		var httpErr *HTTPError
		retryable := !errors.As(err, &httpErr) || httpErr.retryable()
		if !retryable || attempt == MaxRetries || ctx.Err() != nil {
			return err
		}
		delay := wait
		if retryAfter > 0 {
			delay = min(retryAfter, maxRetryAfter)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		wait *= 2
	}
}

// post makes one request. It returns the Retry-After delay, if any, along
// with an error.
func (c *ExpoClient) post(ctx context.Context, url string, body []byte, out any, check func(status int) error) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		// A bad URL; retrying cannot help, so report it as a non-retryable
		// HTTPError with no status.
		return 0, &HTTPError{Codes: []string{"invalid request URL"}}
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
		return 0, fmt.Errorf("expo push: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return 0, fmt.Errorf("expo push: reading response: %w", err)
	}

	var parsed expoErrors
	decodeErr := json.Unmarshal(raw, &parsed)
	var codes []string
	for _, e := range parsed.Errors {
		codes = append(codes, e.Code)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return retryAfter(resp.Header.Get("Retry-After")), &HTTPError{StatusCode: resp.StatusCode, Codes: codes}
	}
	if decodeErr == nil {
		decodeErr = json.Unmarshal(raw, out)
	}
	if decodeErr != nil {
		return 0, &HTTPError{StatusCode: resp.StatusCode, Codes: []string{"invalid response body"}}
	}
	if len(codes) > 0 {
		return 0, &HTTPError{StatusCode: resp.StatusCode, Codes: codes}
	}
	if check != nil {
		return 0, check(resp.StatusCode)
	}
	return 0, nil
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
