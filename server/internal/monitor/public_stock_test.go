package monitor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ovh-buy/server/internal/types"
)

// Exercise the actual stock loop: cart endpoints deliberately fail, while
// public stock and catalog are healthy. Notifications must not call the cart.
func TestPublicStockNotifiesWithoutCart(t *testing.T) {
	m := outboxMonitor(t)
	m.state.Accounts = []types.OVHAccount{{ID: "public-stock-account", Zone: "FR", Endpoint: "ovh-eu", IsDefault: true}}
	sub := m.subscriptions[0]
	sub.replaceLastStatus(map[string]string{"gra|plan": "unavailable"})
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	available, sends, carts := true, 0, 0
	http.DefaultTransport = outboxTransport(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		switch {
		case strings.Contains(r.URL.Path, "/catalog/public/eco"):
			body = `{"locale":{"currencyCode":"EUR"},"plans":[{"planCode":"plan","pricings":[{"interval":1,"intervalUnit":"month","mode":"default","price":2999000000,"capacities":["renew"]},{"mode":"default","price":1899000000,"capacities":["installation"]}]}],"addons":[]}`
		case strings.Contains(r.URL.Path, "/datacenter/availabilities"):
			status := "unavailable"
			if available {
				status = "1H-low"
			}
			body = fmt.Sprintf(`[{"fqn":"plan","datacenters":[{"datacenter":"gra","availability":%q}]}]`, status)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			sends++
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(payload.Text, "29.99") || !strings.Contains(payload.Text, "18.99") {
				t.Errorf("missing catalog prices: %s", payload.Text)
			}
			body = `{"ok":true,"result":{"message_id":42}}`
		case r.URL.Host == "webhook.invalid":
		case strings.HasSuffix(r.URL.Path, "/editMessageText"), strings.HasSuffix(r.URL.Path, "/editMessageReplyMarkup"):
			body = `{"ok":true,"result":{"message_id":42}}`
		default:
			carts++
			return nil, fmt.Errorf("unexpected cart/account request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	m.CheckAvailabilityChange(sub, "public-stock")
	if sends != 1 || carts != 0 || sub.statusSnapshot()["gra|plan"] != "available" {
		t.Fatalf("sends=%d carts=%d statuses=%v", sends, carts, sub.statusSnapshot())
	}
	m.CheckAvailabilityChange(sub, "unchanged")
	if sends != 1 {
		t.Fatal("unchanged stock duplicated notification")
	}
	available = false
	m.CheckAvailabilityChange(sub, "delisted")
	if sub.statusSnapshot()["gra|plan"] != "unavailable" || carts != 0 {
		t.Fatalf("delisting failed: %v, unexpected calls=%d", sub.statusSnapshot(), carts)
	}
}
