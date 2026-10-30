package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func backend(t *testing.T, name string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		hits.Add(1)
		w.Header().Set("X-Backend", name)
		fmt.Fprintf(w, "%s %s %s xff=%s host=%s", name, r.Method, r.URL.RequestURI(), r.Header.Get("X-Forwarded-For"), r.Host)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func newProxy(t *testing.T, urls []string, limiter *Limiter, trust bool) (*Proxy, *httptest.Server) {
	t.Helper()
	pool, err := NewPool(urls)
	if err != nil {
		t.Fatal(err)
	}
	p := New(Options{Pool: pool, Limiter: limiter, TrustForwarded: trust})
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)
	return p, front
}

func get(t *testing.T, url string, headers ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestProxyForwardsRequest(t *testing.T) {
	a, _ := backend(t, "a")
	_, front := newProxy(t, []string{a.URL}, nil, false)
	resp, body := get(t, front.URL+"/some/path?x=1")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Backend") != "a" {
		t.Fatalf("status %d, headers %v", resp.StatusCode, resp.Header)
	}
	if !strings.Contains(body, "GET /some/path?x=1") {
		t.Fatalf("body = %q", body)
	}
}

func TestProxyRoundRobinsAcrossBackends(t *testing.T) {
	a, ah := backend(t, "a")
	b, bh := backend(t, "b")
	_, front := newProxy(t, []string{a.URL, b.URL}, nil, false)
	for i := 0; i < 10; i++ {
		get(t, front.URL+"/")
	}
	if ah.Load() != 5 || bh.Load() != 5 {
		t.Fatalf("a=%d b=%d, want 5 each", ah.Load(), bh.Load())
	}
}

func TestProxyRateLimitsPerClient(t *testing.T) {
	a, _ := backend(t, "a")
	clock := newClock()
	limiter := NewLimiter(1, 2, clock.Now)
	p, front := newProxy(t, []string{a.URL}, limiter, true)
	for i := 0; i < 2; i++ {
		if resp, _ := get(t, front.URL+"/", "X-Forwarded-For", "10.0.0.1"); resp.StatusCode != 200 {
			t.Fatalf("request %d status %d", i, resp.StatusCode)
		}
	}
	resp, _ := get(t, front.URL+"/", "X-Forwarded-For", "10.0.0.1")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q", resp.Header.Get("Retry-After"))
	}
	if resp, _ := get(t, front.URL+"/", "X-Forwarded-For", "10.0.0.2"); resp.StatusCode != 200 {
		t.Fatalf("second client status %d", resp.StatusCode)
	}
	if p.Counters().RateLimited != 1 {
		t.Fatalf("counters = %+v", p.Counters())
	}
	clock.Advance(time.Second)
	if resp, _ := get(t, front.URL+"/", "X-Forwarded-For", "10.0.0.1"); resp.StatusCode != 200 {
		t.Fatalf("after refill status %d", resp.StatusCode)
	}
}

func TestProxyLimitsByRemoteAddrWhenNotTrusting(t *testing.T) {
	a, _ := backend(t, "a")
	limiter := NewLimiter(0.001, 1, nil)
	_, front := newProxy(t, []string{a.URL}, limiter, false)
	get(t, front.URL+"/", "X-Forwarded-For", "1.1.1.1")
	resp, _ := get(t, front.URL+"/", "X-Forwarded-For", "2.2.2.2")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("spoofed header should not dodge the limit, got %d", resp.StatusCode)
	}
}

func TestClientKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.7:5555"
	if got := ClientKey(r, false); got != "192.0.2.7" {
		t.Fatalf("key = %q", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := ClientKey(r, true); got != "203.0.113.9" {
		t.Fatalf("trusted key = %q", got)
	}
	if got := ClientKey(r, false); got != "192.0.2.7" {
		t.Fatalf("untrusted key = %q", got)
	}
	r.RemoteAddr = "weird"
	if got := ClientKey(r, false); got != "weird" {
		t.Fatalf("fallback key = %q", got)
	}
}

func TestProxySetsForwardedHeaders(t *testing.T) {
	a, _ := backend(t, "a")
	_, front := newProxy(t, []string{a.URL}, nil, false)
	_, body := get(t, front.URL+"/", "X-Forwarded-For", "6.6.6.6")
	if !strings.Contains(body, "xff=127.0.0.1") || strings.Contains(body, "6.6.6.6") {
		t.Fatalf("body = %q", body)
	}
	_, trustedFront := newProxy(t, []string{a.URL}, nil, true)
	_, body = get(t, trustedFront.URL+"/", "X-Forwarded-For", "6.6.6.6")
	if !strings.Contains(body, "xff=6.6.6.6, 127.0.0.1") {
		t.Fatalf("trusted body = %q", body)
	}
}

func TestProxyKeepsOriginalHost(t *testing.T) {
	a, _ := backend(t, "a")
	_, front := newProxy(t, []string{a.URL}, nil, false)
	_, body := get(t, front.URL+"/")
	if !strings.Contains(body, "host="+strings.TrimPrefix(front.URL, "http://")) {
		t.Fatalf("body = %q", body)
	}
}

func TestProxyNoHealthyUpstream(t *testing.T) {
	a, _ := backend(t, "a")
	p, front := newProxy(t, []string{a.URL}, nil, false)
	p.pool.All()[0].SetHealthy(false)
	resp, _ := get(t, front.URL+"/")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", resp.StatusCode)
	}
	if p.Counters().NoUpstream != 1 {
		t.Fatalf("counters = %+v", p.Counters())
	}
}

func TestProxyMarksFailedUpstreamDown(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	live, _ := backend(t, "live")
	p, front := newProxy(t, []string{url, live.URL}, nil, false)
	resp, _ := get(t, front.URL+"/")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", resp.StatusCode)
	}
	if p.pool.All()[0].Healthy() {
		t.Fatal("failed upstream should be marked down")
	}
	for i := 0; i < 4; i++ {
		if resp, _ := get(t, front.URL+"/"); resp.StatusCode != 200 {
			t.Fatalf("request %d status %d", i, resp.StatusCode)
		}
	}
}

func TestProxyStatusEndpoint(t *testing.T) {
	a, _ := backend(t, "a")
	p, front := newProxy(t, []string{a.URL}, nil, false)
	get(t, front.URL+"/")
	_, body := get(t, front.URL+StatusPath)
	var status struct {
		Upstreams []struct {
			URL     string `json:"url"`
			Healthy bool   `json:"healthy"`
		} `json:"upstreams"`
		Counters Counters `json:"counters"`
	}
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Upstreams) != 1 || !status.Upstreams[0].Healthy || status.Counters.Proxied != 1 {
		t.Fatalf("status = %+v", status)
	}
	if p.Counters().Proxied != 1 {
		t.Fatal("status endpoint should not count as proxied")
	}
}

func TestCheckerMarksUpstreamsDownAndUp(t *testing.T) {
	var failing atomic.Bool
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer flaky.Close()
	good, _ := backend(t, "good")
	pool, _ := NewPool([]string{flaky.URL, good.URL})
	var changes atomic.Int32
	checker := NewChecker(pool, "/healthz", time.Second, time.Second)
	checker.OnChange = func(*Upstream, bool) { changes.Add(1) }

	checker.CheckOnce(context.Background())
	if pool.HealthyCount() != 2 || changes.Load() != 0 {
		t.Fatalf("healthy=%d changes=%d", pool.HealthyCount(), changes.Load())
	}
	failing.Store(true)
	checker.CheckOnce(context.Background())
	if pool.All()[0].Healthy() || !pool.All()[1].Healthy() {
		t.Fatal("only the flaky upstream should be down")
	}
	failing.Store(false)
	checker.CheckOnce(context.Background())
	if pool.HealthyCount() != 2 || changes.Load() != 2 {
		t.Fatalf("healthy=%d changes=%d", pool.HealthyCount(), changes.Load())
	}
}

func TestCheckerTreatsUnreachableAsDown(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	pool, _ := NewPool([]string{url})
	NewChecker(pool, "", time.Second, 200*time.Millisecond).CheckOnce(context.Background())
	if pool.HealthyCount() != 0 {
		t.Fatal("unreachable upstream should be down")
	}
}

func TestCheckerRunStopsOnCancel(t *testing.T) {
	good, _ := backend(t, "good")
	pool, _ := NewPool([]string{good.URL})
	pool.All()[0].SetHealthy(false)
	checker := NewChecker(pool, "/healthz", 10*time.Millisecond, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		checker.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !pool.All()[0].Healthy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !pool.All()[0].Healthy() {
		t.Fatal("checker never marked the upstream healthy")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

