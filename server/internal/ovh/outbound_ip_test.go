package ovh

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ovh-buy/server/internal/types"
)

func testChecker(serverURL, expected string, writes *[]outboundIPResult) *outboundIPChecker {
	account := types.OVHAccount{ID: "account-1", ExpectedOutboundIP: expected}
	checker := newOutboundIPChecker(func(id string) (types.OVHAccount, bool) {
		return account, id == account.ID
	})
	checker.url = serverURL
	checker.write = func(_ string, actual, status, _ string, reason string) error {
		*writes = append(*writes, outboundIPResult{actualIP: actual, status: status, reason: reason})
		return nil
	}
	return checker
}

func TestOutboundIPCheckerBlocksMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ip":"198.51.100.9"}`)
	}))
	defer server.Close()

	var writes []outboundIPResult
	checker := testChecker(server.URL, "203.0.113.10", &writes)
	err := checker.check(context.Background(), "account-1", false)
	if err == nil || !strings.Contains(err.Error(), "已阻断") {
		t.Fatalf("expected fail-closed mismatch error, got %v", err)
	}
	if len(writes) != 1 || writes[0].status != "blocked" || writes[0].actualIP != "198.51.100.9" {
		t.Fatalf("unexpected persisted result: %#v", writes)
	}
}

func TestOutboundIPCheckerSingleflightAndCache(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"ip":"203.0.113.10"}`)
	}))
	defer server.Close()

	var writes []outboundIPResult
	checker := testChecker(server.URL, "203.0.113.10", &writes)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := checker.check(context.Background(), "account-1", false); err != nil {
				t.Errorf("check: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := requests.Load(); got != 1 {
		t.Fatalf("want one probe, got %d", got)
	}
	if len(writes) != 1 || writes[0].status != "verified" {
		t.Fatalf("unexpected writes: %#v", writes)
	}
}

type countingTransport struct{ calls atomic.Int32 }

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
}

func TestGuardedTransportOnlyAllowsVerifiedSignedRequest(t *testing.T) {
	base := &countingTransport{}
	guard := guardedTransport{base: base, check: func(context.Context) error { return errOutboundBlocked }}
	req := httptest.NewRequest(http.MethodGet, "https://api.ovh.example/1.0/me", nil)
	req.Header.Set("X-Ovh-Application", "app")
	if _, err := guard.RoundTrip(req); err == nil {
		t.Fatal("signed request must be blocked")
	}
	if base.calls.Load() != 0 {
		t.Fatal("blocked request reached base transport")
	}

	unsigned := httptest.NewRequest(http.MethodGet, "https://api.ovh.example/1.0/auth/time", nil)
	if _, err := guard.RoundTrip(unsigned); err != nil {
		t.Fatalf("unsigned request: %v", err)
	}
	if base.calls.Load() != 1 {
		t.Fatal("unsigned request did not reach base transport")
	}
}

type blockedError string

func (e blockedError) Error() string { return string(e) }

const errOutboundBlocked = blockedError("出口 IP 未确认，OVH 请求已阻断")
