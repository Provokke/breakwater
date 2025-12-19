package proxy

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type contextKey struct{}

const StatusPath = "/_breakwater/status"

type Options struct {
	Pool           *Pool
	Limiter        *Limiter
	TrustForwarded bool
	Transport      http.RoundTripper
	Logger         *log.Logger
}

type Counters struct {
	Proxied     uint64 `json:"proxied"`
	RateLimited uint64 `json:"rate_limited"`
	NoUpstream  uint64 `json:"no_upstream"`
	BadGateway  uint64 `json:"bad_gateway"`
}

type Proxy struct {
	pool    *Pool
	limiter *Limiter
	trust   bool
	logger  *log.Logger
	rp      *httputil.ReverseProxy

	proxied, limited, noUpstream, badGateway atomic.Uint64
}

func New(opts Options) *Proxy {
	p := &Proxy{
		pool:    opts.Pool,
		limiter: opts.Limiter,
		trust:   opts.TrustForwarded,
		logger:  opts.Logger,
	}
	p.rp = &httputil.ReverseProxy{
		Rewrite:      p.rewrite,
		Transport:    opts.Transport,
		ErrorHandler: p.fail,
	}
	return p
}

func (p *Proxy) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}

func (p *Proxy) rewrite(pr *httputil.ProxyRequest) {
	up := pr.In.Context().Value(contextKey{}).(*Upstream)
	pr.SetURL(up.URL)
	pr.Out.Host = pr.In.Host
	if p.trust {
		pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
	}
	pr.SetXForwarded()
}

func (p *Proxy) fail(w http.ResponseWriter, r *http.Request, err error) {
	if up, ok := r.Context().Value(contextKey{}).(*Upstream); ok {
		up.SetHealthy(false)
		p.logf("upstream %s failed: %v", up.URL.Host, err)
	}
	p.badGateway.Add(1)
	http.Error(w, "bad gateway", http.StatusBadGateway)
}

func ClientKey(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
			if first != "" {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == StatusPath {
		p.status(w)
		return
	}
	if p.limiter != nil {
		if ok, wait := p.limiter.Allow(ClientKey(r, p.trust)); !ok {
			p.limited.Add(1)
			seconds := int(math.Ceil(wait.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}
	up := p.pool.Next()
	if up == nil {
		p.noUpstream.Add(1)
		http.Error(w, "no healthy upstream", http.StatusServiceUnavailable)
		return
	}
	p.proxied.Add(1)
	ctx := context.WithValue(r.Context(), contextKey{}, up)
	p.rp.ServeHTTP(w, r.WithContext(ctx))
}

func (p *Proxy) Counters() Counters {
	return Counters{
		Proxied:     p.proxied.Load(),
		RateLimited: p.limited.Load(),
		NoUpstream:  p.noUpstream.Load(),
		BadGateway:  p.badGateway.Load(),
	}
}

type upstreamStatus struct {
	URL     string `json:"url"`
	Healthy bool   `json:"healthy"`
}

func (p *Proxy) status(w http.ResponseWriter) {
	body := struct {
		Upstreams []upstreamStatus `json:"upstreams"`
		Counters  Counters         `json:"counters"`
		Time      time.Time        `json:"time"`
	}{Counters: p.Counters(), Time: time.Now().UTC()}
	for _, u := range p.pool.All() {
		body.Upstreams = append(body.Upstreams, upstreamStatus{URL: u.URL.String(), Healthy: u.Healthy()})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

