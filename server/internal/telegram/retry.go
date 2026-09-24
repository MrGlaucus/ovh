package telegram

import (
	"errors"
	"time"
)

// SendError keeps retry policy separate from the scrubbed user-facing error.
type SendError struct {
	Message    string
	Temporary  bool
	RetryAfter time.Duration
}

func (e *SendError) Error() string { return e.Message }

// Three attempts at most. Long rate limits are deferred to the persistent outbox.
// Telegram has no sendMessage idempotency key: an ambiguous network timeout can
// result in a duplicate message, but must never replay an order.
func retrySend(send func() (MessageRef, error), sleep func(time.Duration)) (MessageRef, error) {
	for attempt := 0; ; attempt++ {
		ref, err := send()
		var failure *SendError
		if err == nil || attempt == 2 || !errors.As(err, &failure) || !failure.Temporary {
			return ref, err
		}
		delay := time.Duration(attempt+1) * time.Second
		if failure.RetryAfter > delay {
			delay = failure.RetryAfter
		}
		if delay > 5*time.Second {
			return ref, err
		}
		sleep(delay)
	}
}
