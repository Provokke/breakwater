package proxy

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestLimiterAllowsBurstThenBlocks(t *testing.T) {
	clock := newClock()
	l := NewLimiter(1, 3, clock.Now)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok {
		t.Fatal("fourth request should be blocked")
	}
	if wait != time.Second {
		t.Fatalf("wait = %v, want 1s", wait)
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	clock := newClock()
	l := NewLimiter(2, 2, clock.Now)
	l.Allow("a")
	l.Allow("a")
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("bucket should be empty")
	}
	clock.Advance(500 * time.Millisecond)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("one token should have refilled")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("only one token should have refilled")
	}
}

func TestLimiterNeverExceedsBurst(t *testing.T) {
	clock := newClock()
	l := NewLimiter(100, 2, clock.Now)
	clock.Advance(time.Hour)
	allowed := 0
	for i := 0; i < 10; i++ {
		if ok, _ := l.Allow("a"); ok {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("allowed %d, want 2", allowed)
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	clock := newClock()
	l := NewLimiter(1, 1, clock.Now)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a should pass")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("a should be limited")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("b should have its own bucket")
	}
}

func TestLimiterSweepsIdleBuckets(t *testing.T) {
	clock := newClock()
	l := NewLimiter(10, 5, clock.Now)
	for _, key := range []string{"a", "b", "c"} {
		l.Allow(key)
	}
	if l.Tracked() != 3 {
		t.Fatalf("tracked = %d", l.Tracked())
	}
	clock.Advance(2 * time.Minute)
	l.Allow("d")
	if l.Tracked() != 1 {
		t.Fatalf("tracked = %d after sweep, want 1", l.Tracked())
	}
}

func TestLimiterZeroRateNeverRefills(t *testing.T) {
	clock := newClock()
	l := NewLimiter(0, 1, clock.Now)
	l.Allow("a")
	ok, wait := l.Allow("a")
	if ok || wait < time.Minute {
		t.Fatalf("ok=%v wait=%v", ok, wait)
	}
}

func TestLimiterConcurrentUse(t *testing.T) {
	l := NewLimiter(1000, 100, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				l.Allow("shared")
			}
		}()
	}
	wg.Wait()
}
