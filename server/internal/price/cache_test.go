package price

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPriceCacheCoalescesAndIsolatesResults(t *testing.T) {
	c := priceCache{entries: map[string]*cachedPrice{}}
	var calls atomic.Int32
	fetch := func() Result {
		calls.Add(1)
		return Result{Success: true, Options: []string{"ram"}, Price: &PriceInfo{Prices: map[string]interface{}{"value": 5.0}}}
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := c.get("same", fetch)
			r.Options[0] = "mutated"
			r.Price.Prices["value"] = 99.0
		}()
	}
	wg.Wait()
	r := c.get("same", fetch)
	if calls.Load() != 1 || r.Options[0] != "ram" || r.Price.Prices["value"] != 5.0 {
		t.Fatalf("cache leak or duplicate calls: %d, %+v", calls.Load(), r)
	}
	c.get("other-account", fetch)
	if calls.Load() != 2 {
		t.Fatal("different account/configuration reused cache")
	}
	c.mu.Lock()
	c.entries["same"].expires = time.Now().Add(-time.Second)
	c.mu.Unlock()
	c.get("same", fetch)
	if calls.Load() != 3 {
		t.Fatal("expired cache reused")
	}
}

func TestPriceCacheDoesNotKeepFailureOrDegradedResults(t *testing.T) {
	for _, result := range []Result{{Error: "429"}, {Success: true, Degraded: true, Price: &PriceInfo{}}} {
		c := priceCache{entries: map[string]*cachedPrice{}}
		calls := 0
		fetch := func() Result { calls++; return result }
		c.get("key", fetch)
		c.get("key", fetch)
		if calls != 2 {
			t.Fatal("failure/degraded result was cached")
		}
	}
	opts := []string{"z", "a"}
	if priceKey(opts) != priceKey([]string{"a", "z"}) || opts[0] != "z" {
		t.Fatal("key mutated caller options or depends on order")
	}
}
