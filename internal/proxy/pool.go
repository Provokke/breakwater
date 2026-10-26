package proxy

import (
	"fmt"
	"net/url"
	"sync/atomic"
)

type Upstream struct {
	URL     *url.URL
	healthy atomic.Bool
}

func (u *Upstream) Healthy() bool { return u.healthy.Load() }

func (u *Upstream) SetHealthy(ok bool) { u.healthy.Store(ok) }

type Pool struct {
	upstreams []*Upstream
	next      atomic.Uint64
}

func NewPool(rawURLs []string) (*Pool, error) {
	if len(rawURLs) == 0 {
		return nil, fmt.Errorf("at least one upstream is required")
	}
	pool := &Pool{}
	for _, raw := range rawURLs {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid upstream %q", raw)
		}
		u := &Upstream{URL: parsed}
		u.healthy.Store(true)
		pool.upstreams = append(pool.upstreams, u)
	}
	return pool, nil
}

func (p *Pool) Next() *Upstream {
	n := uint64(len(p.upstreams))
	start := p.next.Add(1) - 1
	for i := uint64(0); i < n; i++ {
		candidate := p.upstreams[(start+i)%n]
		if candidate.Healthy() {
			return candidate
		}
	}
	return nil
}

func (p *Pool) All() []*Upstream {
	return p.upstreams
}

func (p *Pool) HealthyCount() int {
	count := 0
	for _, u := range p.upstreams {
		if u.Healthy() {
			count++
		}
	}
	return count
}

