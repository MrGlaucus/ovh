package handlers

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestTelegramAccountChoiceCallbackDataFitsLimit(t *testing.T) {
	id := uuid.NewString()
	for action := range map[string]struct{}{
		"add_to_queue": {},
		"back":         {},
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
