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

func TestExpoClientFetchesReceipts(t *testing.T) {
	t.Parallel()
	var gotPath, gotAuth, gotMethod string
	var got struct {
		IDs []string `json:"ids"`
	}
	srv := newExpoServer(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		gotPath, gotAuth, gotMethod = r.URL.Path, r.Header.Get("Authorization"), r.Method
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = io.WriteString(w, `{"data":{"a":{"status":"ok"},"b":{"status":"error","message":"\"`+testToken+`\" is not a registered push notification recipient","details":{"error":"DeviceNotRegistered"}}}}`)
	})
	c := client(srv.URL + "/--/api/v2/push/send")
	c.AccessToken = "expo-access"
	receipts, err := c.Receipts(t.Context(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/--/api/v2/push/getReceipts" || gotAuth != "Bearer expo-access" {
		t.Errorf("method %q, path %q, Authorization %q", gotMethod, gotPath, gotAuth)
	}
	if strings.Join(got.IDs, ",") != "a,b,c" {
		t.Errorf("server got IDs %q", got.IDs)
	}
	if len(receipts) != 2 || receipts["a"].Status != expiry.TicketOK || receipts["b"].Details.Error != expiry.ErrorDeviceNotRegistered {
		t.Errorf("receipts %+v", receipts)
	}
	if _, ok := receipts["c"]; ok {
		t.Error("a receipt Expo did not return is in the map")
	}
}

func TestExpoClientReceiptsURL(t *testing.T) {
	t.Parallel()
	if got := expiry.ReceiptsURLFor(expiry.DefaultExpoPushURL); got != expiry.DefaultExpoReceiptsURL {
		t.Errorf("ReceiptsURLFor(default) = %q, want %q", got, expiry.DefaultExpoReceiptsURL)
	}
	for in, want := range map[string]string{
		"http://127.0.0.1:8080":             "http://127.0.0.1:8080/getReceipts",
		"http://127.0.0.1:8080/":            "http://127.0.0.1:8080/getReceipts",
		"http://127.0.0.1:8080/push/send?x": "http://127.0.0.1:8080/push/getReceipts",
	} {
		if got := expiry.ReceiptsURLFor(in); got != want {
			t.Errorf("ReceiptsURLFor(%q) = %q, want %q", in, got, want)
		}
	}

	// An explicit ReceiptsURL wins.
	var gotPath string
	srv := newExpoServer(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"data":{}}`)
	})
	c := client(srv.URL + "/push/send")
	c.ReceiptsURL = srv.URL + "/receipts"
	if _, err := c.Receipts(t.Context(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/receipts" {
		t.Errorf("requested %q, want /receipts", gotPath)
	}
}

func TestExpoClientReceiptsBatchLimitAndRetries(t *testing.T) {
	t.Parallel()
	srv := newExpoServer(t, func(w http.ResponseWriter, _ *http.Request, n int32) {
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"data":{}}`)
	})
	ids := make([]string, expiry.MaxReceiptBatchSize+1)
	if _, err := client(srv.URL).Receipts(t.Context(), ids); err == nil {
		t.Error("1001 IDs were accepted")
	}
	if n := srv.requests.Load(); n != 0 {
		t.Errorf("%d requests made for an oversized batch", n)
	}
	receipts, err := client(srv.URL).Receipts(t.Context(), ids[:expiry.MaxReceiptBatchSize])
	if err != nil || receipts == nil || len(receipts) != 0 {
		t.Errorf("1000 IDs after one 503: %v, %v", receipts, err)
	}
	if n := srv.requests.Load(); n != 2 {
		t.Errorf("%d requests, want 2 (one retry)", n)
	}
}

func TestExpoClientReceiptsErrors(t *testing.T) {
	t.Parallel()
	srv := newExpoServer(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errors":[{"code":"VALIDATION_ERROR","message":"bad id"}]}`)
	})
	_, err := client(srv.URL).Receipts(t.Context(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "VALIDATION_ERROR") {
		t.Errorf("error %v, want VALIDATION_ERROR", err)
	}
	if n := srv.requests.Load(); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}
