package handlers

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestTelegramMenuCommandMatching(t *testing.T) {
	for _, text := range []string{"/buy", "/buy@ovh_bot", " /BUY@ovh_bot "} {
		if !matchesTelegramCommand(text, "buy") {
			t.Fatalf("%q 应匹配 /buy", text)
		}
	}
	for _, text := range []string{"/buyer", "/buy now", "buy"} {
		if matchesTelegramCommand(text, "buy") {
			t.Fatalf("%q 不应匹配 /buy", text)
		}
	}
}

func TestTelegramAccountChoiceCallbackDataFitsLimit(t *testing.T) {
	id := uuid.NewString()
	for action := range map[string]struct{}{
		"add_to_queue": {},
		"back":         {},
		"fav":          {},
		"cfg":          {},
		"bm":           {},
		"dc":           {},
		"bc":           {},
		"bd":           {},
	} {
		data, err := json.Marshal(map[string]string{"a": action, "u": id})
		if err != nil {
			t.Fatalf("marshal %q callback: %v", action, err)
		}
		if len(data) > 64 {
			t.Fatalf("%q callback is %d bytes, Telegram accepts at most 64: %s", action, len(data), data)
		}
	}
}
