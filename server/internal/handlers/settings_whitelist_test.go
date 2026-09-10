package handlers

import "testing"

func TestNormalizeTelegramUserIDs(t *testing.T) {
	got, err := normalizeTelegramUserIDs(" 99,42,99 ")
	if err != nil {
		t.Fatalf("normalizeTelegramUserIDs() error = %v", err)
	}
	if got != "42,99" {
		t.Fatalf("normalizeTelegramUserIDs() = %q, want %q", got, "42,99")
	}
}

func TestNormalizeTelegramUserIDsRejectsNonNumericValue(t *testing.T) {
	if _, err := normalizeTelegramUserIDs("42,@owner"); err == nil {
		t.Fatal("normalizeTelegramUserIDs() accepted a non-numeric User ID")
	}
}
