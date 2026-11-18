package proxy

import (
	"math"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

type Limiter struct {
	mu        sync.Mutex
	rate      float64
	burst     float64
	clock     func() time.Time
	buckets   map[string]*bucket
	lastSweep time.Time
}

const sweepEvery = time.Minute

func NewLimiter(rate float64, burst int, clock func() time.Time) *Limiter {
	if clock == nil {
		clock = time.Now
	}
	return &Limiter{
		rate:      rate,
		burst:     float64(burst),
		clock:     clock,
		buckets:   make(map[string]*bucket),
		lastSweep: clock(),
	}
}

func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	l.sweep(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	l.refill(b, now)
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	if l.rate <= 0 {
		return false, time.Hour
	}
	wait := time.Duration(math.Ceil((1-b.tokens)/l.rate*1e9)) * time.Nanosecond
	return false, wait
}

func (l *Limiter) refill(b *bucket, now time.Time) {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
}

func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < sweepEvery {
		return
	}
	l.lastSweep = now
	for key, b := range l.buckets {
		l.refill(b, now)
		if b.tokens >= l.burst {
			delete(l.buckets, key)
		}
	}
}

func (l *Limiter) Tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
