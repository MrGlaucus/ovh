package monitor

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLegacyButtonCanOnlyBeTakenOnceConcurrently(t *testing.T) {
	m := &Monitor{messageUUIDCacheTTL: time.Hour, messageUUIDCache: map[string]*CachedMessage{
		"button": {PlanCode: "ks", Timestamp: float64(time.Now().Unix())},
	}}
	var taken atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.MessageUUIDCacheTake("button") != nil {
				taken.Add(1)
			}
		}()
	}
	wg.Wait()
	if taken.Load() != 1 {
		t.Fatalf("button consumed %d times", taken.Load())
	}
}
