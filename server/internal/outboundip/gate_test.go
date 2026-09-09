package outboundip

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ovh-buy/server/internal/types"
)

func withIPService(t *testing.T, ip string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "fl=582f232\ncolo=HKG\nip="+ip+"\nwarp=off\n")
	}))
	old := serviceURL
	serviceURL = srv.URL
	t.Cleanup(func() { serviceURL = old; srv.Close() })
	return srv.URL
}

func TestGateAllowsMatchingIPv4(t *testing.T) {
	proxyURL := withIPService(t, "198.51.100.10")
	status := NewGate().Ensure(types.OVHAccount{ID: "a", ProxyURL: proxyURL, ExpectedOutboundIP: "198.51.100.10"}, true)
	if !status.Allowed() || status.ActualIP != "198.51.100.10" {
		t.Fatalf("expected verified matching IP, got %#v", status)
	}
}

func TestGateSkipsDirectAccount(t *testing.T) {
	status := NewGate().Ensure(types.OVHAccount{ID: "direct"}, true)
	if !status.Allowed() || status.State != "direct" {
		t.Fatalf("expected direct account to bypass IP verification, got %#v", status)
	}
}

func TestGateBlocksMismatchAndInvalidExpectedIP(t *testing.T) {
	proxyURL := withIPService(t, "198.51.100.10")
	mismatch := NewGate().Ensure(types.OVHAccount{ID: "a", ProxyURL: proxyURL, ExpectedOutboundIP: "198.51.100.11"}, true)
	if mismatch.Allowed() || mismatch.State != "mismatch" {
		t.Fatalf("expected mismatch block, got %#v", mismatch)
	}
	invalid := NewGate().Ensure(types.OVHAccount{ID: "b", ProxyURL: proxyURL, ExpectedOutboundIP: "not-an-ip"}, true)
	if invalid.Allowed() || invalid.State != "failed" {
		t.Fatalf("expected invalid configuration block, got %#v", invalid)
	}
}

type countingTransport struct{ calls atomic.Int32 }

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return nil, io.EOF
}

func TestGuardedTransportDoesNotSendWhenIPIsUnverified(t *testing.T) {
	base := &countingTransport{}
	guard := GuardedTransport{
		Base:    base,
		Gate:    NewGate(),
		Account: types.OVHAccount{ID: "blocked", ProxyURL: "http://127.0.0.1:1", ExpectedOutboundIP: "not-an-ip"},
	}
	req := httptest.NewRequest(http.MethodGet, "https://api.ovh.com/1.0/me", nil)
	_, err := guard.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "已阻断") {
		t.Fatalf("expected blocked request error, got %v", err)
	}
	if base.calls.Load() != 0 {
		t.Fatalf("OVH base transport was called despite blocked IP state")
	}
}
