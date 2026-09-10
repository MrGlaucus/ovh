package telegram

import (
	"testing"

	"github.com/ovh-buy/server/internal/types"
)

func TestAuthorizedActorRequiresConfiguredWhitelist(t *testing.T) {
	cfg := types.Config{TgChatID: "-100123", TgAllowedUserIDs: ""}
	if isAuthorizedActor(cfg, "-100123", "42") {
		t.Fatal("empty whitelist must reject every Telegram user")
	}
}

func TestAuthorizedActorChecksChatAndUser(t *testing.T) {
	cfg := types.Config{TgChatID: "-100123", TgAllowedUserIDs: "42,99"}
	cases := []struct {
		name       string
		chatID     interface{}
		userID     interface{}
		wantAccept bool
	}{
		{"allowed group member", "-100123", "42", true},
		{"unlisted group member", "-100123", "7", false},
		{"allowed user wrong chat", "-100456", "42", false},
		{"missing user id", "-100123", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAuthorizedActor(cfg, tc.chatID, tc.userID); got != tc.wantAccept {
				t.Fatalf("isAuthorizedActor() = %v, want %v", got, tc.wantAccept)
			}
		})
	}
}

func TestAuthorizedActorAllowsWhitelistedPrivateChat(t *testing.T) {
	cfg := types.Config{TgChatID: "42", TgAllowedUserIDs: "42"}
	if !isAuthorizedActor(cfg, "42", "42") {
		t.Fatal("whitelisted private-chat owner should be authorized")
	}
}
