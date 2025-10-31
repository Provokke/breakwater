package proxy

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Checker struct {
	Pool     *Pool
	Client   *http.Client
	Path     string
	Interval time.Duration
	OnChange func(upstream *Upstream, healthy bool)
}

func NewChecker(pool *Pool, path string, interval, timeout time.Duration) *Checker {
	if path == "" {
		path = "/"
	}
	return &Checker{
		Pool:     pool,
		Client:   &http.Client{Timeout: timeout},
		Path:     path,
		Interval: interval,
	}
}

func (c *Checker) probe(ctx context.Context, u *Upstream) bool {
	target := url.URL{Scheme: u.URL.Scheme, Host: u.URL.Host, Path: c.Path}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return false
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

func (c *Checker) CheckOnce(ctx context.Context) {
	var wg sync.WaitGroup
	for _, u := range c.Pool.All() {
		wg.Add(1)
		go func(u *Upstream) {
			defer wg.Done()
			healthy := c.probe(ctx, u)
			previous := u.healthy.Swap(healthy)
			if previous != healthy && c.OnChange != nil {
				c.OnChange(u, healthy)
			}
		}(u)
	}
	wg.Wait()
}

func (c *Checker) Run(ctx context.Context) {
	c.CheckOnce(ctx)
	ticker := time.NewTicker(c.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.CheckOnce(ctx)
		}
	}
}

