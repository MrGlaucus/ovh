package telegram

import (
	"errors"
	"testing"
	"time"
)

func TestRetrySend(t *testing.T) {
	for _, tc := range []struct {
		name            string
		err             error
		failures, calls int
		success         bool
	}{
		{"502 recovers", &SendError{Message: "502", Temporary: true}, 2, 3, true},
		{"timeout bounded", &SendError{Message: "timeout", Temporary: true}, 9, 3, false},
		{"bad request", errors.New("400"), 9, 1, false},
		{"long rate limit", &SendError{Message: "429", Temporary: true, RetryAfter: 30 * time.Second}, 9, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ref, err := retrySend(func() (MessageRef, error) {
				calls++
				if calls <= tc.failures {
					return MessageRef{}, tc.err
				}
				return MessageRef{MessageID: 42}, nil
			}, func(time.Duration) {})
			if calls != tc.calls || (err == nil) != tc.success {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if tc.success && ref.MessageID != 42 {
				t.Fatal("lost message reference")
			}
		})
	}
}

func TestRetrySendHonorsRetryAfter(t *testing.T) {
	calls := 0
	var waited time.Duration
	_, err := retrySend(func() (MessageRef, error) {
		calls++
		if calls == 1 {
			return MessageRef{}, &SendError{Temporary: true, RetryAfter: 4 * time.Second}
		}
		return MessageRef{MessageID: 1}, nil
	}, func(d time.Duration) { waited += d })
	if err != nil || waited != 4*time.Second {
		t.Fatalf("wait=%s err=%v", waited, err)
	}
}
