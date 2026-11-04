package expiry_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marboleda/shelfkin/api/internal/expiry"
)

const testToken = "ExponentPushToken[secret-device-token]"

// expoServer is a stand-in for Expo's push endpoint. handle answers each
// request; requests counts them.
type expoServer struct {
	*httptest.Server
	requests atomic.Int32
}

func newExpoServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int32)) *expoServer {
	t.Helper()
	s := &expoServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, s.requests.Add(1))
	}))
	t.Cleanup(s.Close)
	return s
}

func okTickets(w http.ResponseWriter, r *http.Request) {
	var msgs []expiry.Message
	if err := json.NewDecoder(r.Body).Decode(&msgs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tickets := make([]map[string]any, len(msgs))
	for i := range msgs {
		tickets[i] = map[string]any{"status": "ok", "id": "ticket"}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": tickets})
}

func client(url string) *expiry.ExpoClient {
	return &expiry.ExpoClient{URL: url, Backoff: time.Millisecond}
}

func oneMessage() []expiry.Message {
	return []expiry.Message{{To: testToken, Title: "Expiring soon in Home", Body: "Milk", Data: map[string]string{"householdId": "h1"}}}
}

func TestExpoClientSendsMessages(t *testing.T) {
	t.Parallel()
	var gotAuth, gotType, gotMethod string
	var got []expiry.Message
	srv := newExpoServer(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		gotAuth, gotType, gotMethod = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Method
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = io.WriteString(w, `{"data":[{"status":"ok","id":"a"},{"status":"error","message":"\"`+testToken+`\" is not a registered push notification recipient","details":{"error":"DeviceNotRegistered"}}]}`)
	})
	c := client(srv.URL)
	c.AccessToken = "expo-access"
	msgs := append(oneMessage(), expiry.Message{To: "other", Title: "t", Body: "b"})
	tickets, err := c.Send(t.Context(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotType != "application/json" || gotAuth != "Bearer expo-access" {
		t.Errorf("method %q, Content-Type %q, Authorization %q", gotMethod, gotType, gotAuth)
	}
	if len(got) != 2 || got[0].To != testToken || got[0].Data["householdId"] != "h1" {
		t.Errorf("server got %+v", got)
	}
	if len(tickets) != 2 || tickets[0].Status != expiry.TicketOK || tickets[1].Details.Error != expiry.ErrorDeviceNotRegistered {
		t.Errorf("tickets %+v", tickets)
	}
}

func TestExpoClientNoAccessToken(t *testing.T) {
	t.Parallel()
	var auth []string
	srv := newExpoServer(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		auth = r.Header.Values("Authorization")
		okTickets(w, r)
	})
	if _, err := client(srv.URL).Send(t.Context(), oneMessage()); err != nil {
		t.Fatal(err)
	}
	if len(auth) != 0 {
		t.Errorf("Authorization %q sent without an access token", auth)
	}
}

func TestExpoClientRejectsOversizedBatch(t *testing.T) {
	t.Parallel()
	srv := newExpoServer(t, func(w http.ResponseWriter, r *http.Request, _ int32) { okTickets(w, r) })
	msgs := make([]expiry.Message, expiry.MaxBatchSize+1)
	if _, err := client(srv.URL).Send(t.Context(), msgs); err == nil {
		t.Error("101 messages were accepted")
	}
	if n := srv.requests.Load(); n != 0 {
		t.Errorf("%d requests made", n)
	}
	if _, err := client(srv.URL).Send(t.Context(), msgs[:expiry.MaxBatchSize]); err != nil {
		t.Errorf("100 messages: %v", err)
	}
}

func TestExpoClientRetries(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			srv := newExpoServer(t, func(w http.ResponseWriter, r *http.Request, n int32) {
				if n <= expiry.MaxRetries {
					w.WriteHeader(status)
					return
				}
				okTickets(w, r)
			})
			tickets, err := client(srv.URL).Send(t.Context(), oneMessage())
			if err != nil || len(tickets) != 1 {
				t.Fatalf("after %d failures: %v, %d tickets", expiry.MaxRetries, err, len(tickets))
			}
			if n := srv.requests.Load(); n != expiry.MaxRetries+1 {
				t.Errorf("%d requests, want %d", n, expiry.MaxRetries+1)
			}
		})
	}
}

func TestExpoClientGivesUpAfterRetries(t *testing.T) {
	t.Parallel()
	srv := newExpoServer(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := client(srv.URL).Send(t.Context(), oneMessage())
	var httpErr *expiry.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error %v, want HTTP 503", err)
	}
	if n := srv.requests.Load(); n != expiry.MaxRetries+1 {
		t.Errorf("%d requests, want %d", n, expiry.MaxRetries+1)
	}
}

func TestExpoClientDoesNotRetryOtherErrors(t *testing.T) {
	t.Parallel()
	srv := newExpoServer(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errors":[{"code":"VALIDATION_ERROR","message":"bad token `+testToken+`"}]}`)
	})
	_, err := client(srv.URL).Send(t.Context(), oneMessage())
	if err == nil {
		t.Fatal("no error for HTTP 400")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error %q quotes the push token", err)
	}
	if !strings.Contains(err.Error(), "VALIDATION_ERROR") {
		t.Errorf("error %q does not name Expo's error code", err)
	}
	if n := srv.requests.Load(); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

func TestExpoClientRequestErrorsIn200(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"errors":       `{"errors":[{"code":"PUSH_TOO_MANY_EXPERIENCE_IDS"}]}`,
		"ticket count": `{"data":[]}`,
		"invalid json": `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newExpoServer(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
				_, _ = io.WriteString(w, body)
			})
			if _, err := client(srv.URL).Send(t.Context(), oneMessage()); err == nil {
				t.Errorf("no error for %s", body)
			}
			if n := srv.requests.Load(); n != 1 {
				t.Errorf("%d requests, want 1", n)
			}
		})
	}
}
