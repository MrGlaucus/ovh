package handlers

import (
	"encoding/json"
	"testing"
)

func TestIPv4RejectsMalformedAndIPv6(t *testing.T) {
	for _, s := range []string{"example.com", "999.1.1.1", "1.2.3", "1.2.3.4/32", "::ffff:1.2.3.4", "::1"} {
		if isIPv4(s) {
			t.Fatalf("accepted invalid IPv4: %q", s)
		}
	}
	if !isIPv4("51.195.176.176") {
		t.Fatal("valid IPv4 rejected")
	}
}

func TestTelegramJSONNumberCompatibility(t *testing.T) {
	n, ok := getNumOrFloat(json.Number("123456789"))
	if !ok || n != 123456789 {
		t.Fatal("message id lost after UseNumber")
	}
	if idToString(json.Number("5800000000000000123")) != "5800000000000000123" {
		t.Fatal("large id lost precision")
	}
	if _, ok := getNumOrFloat(json.Number("bad")); ok {
		t.Fatal("invalid number accepted")
	}
}
