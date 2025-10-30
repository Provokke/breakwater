<div align="center">

# breakwater

An HTTP reverse proxy in Go with per-client token-bucket rate limiting, round-robin load balancing and active health checks. Standard library only.

![License](https://img.shields.io/badge/LICENSE-MIT-blue?style=for-the-badge)  ![Language](https://img.shields.io/badge/GO-00ADD8?style=for-the-badge&logo=go&logoColor=white)  ![Tests](https://img.shields.io/badge/TESTS-29-brightgreen?style=for-the-badge)

Refer to [LICENSE](LICENSE) for the legal bits.

</div>

## What's in it

- A reverse proxy built on `net/http/httputil.ReverseProxy` with the `Rewrite` hook, so the original `Host` is kept and `X-Forwarded-For`, `-Host` and `-Proto` are set properly.
- A token-bucket limiter keyed by client address. Each client gets a burst and a steady refill rate. A blocked request gets a `429` with a `Retry-After` header worked out from how long the next token takes. Idle buckets are swept so the map does not grow forever.
- Round-robin selection across upstreams that skips any marked unhealthy.
- Active health checks: every upstream is probed on an interval, concurrently, and comes back into rotation as soon as a probe passes.
- Passive failure detection: if a proxied request fails to connect, that upstream is marked down straight away instead of waiting for the next probe, and the client gets a `502`.
- `503` when no upstream is healthy.
- `X-Forwarded-For` is only trusted for rate limiting if you say so in the config, otherwise a client could dodge its limit by spoofing the header.
- `/_breakwater/status` returns upstream health and request counters as JSON.

```json
{
  "listen": ":8080",
  "upstreams": ["http://127.0.0.1:9001", "http://127.0.0.1:9002"],
  "rate_per_second": 10,
  "burst": 20,
  "health_path": "/healthz",
  "health_interval": "5s"
}
```

## 🏗️ Architecture

```
 client ──► Proxy.ServeHTTP
              ├─ /_breakwater/status ──► JSON
              ├─ Limiter.Allow(client)  ──► 429 + Retry-After
              ├─ Pool.Next()            ──► 503 if nothing healthy
              └─ ReverseProxy ──► upstream
                     └─ transport error ──► mark down, 502

 Checker.Run ──► probe each upstream every interval ──► Upstream.healthy
```

- **internal/proxy/ratelimit.go** holds the `Limiter`. It takes a clock function so the tests can move time by hand.
- **internal/proxy/pool.go** holds `Pool` and `Upstream`. Health is an atomic bool, so request handlers never take a lock to read it.
- **internal/proxy/health.go** is the `Checker`. Anything from 200 to 399 counts as healthy.
- **internal/proxy/proxy.go** ties it together as an `http.Handler`.
- **internal/proxy/config.go** loads the JSON config, rejects unknown fields and fills in defaults.
- **cmd/breakwater** is the executable, with graceful shutdown on interrupt.

## 🚀 Quick start

Go 1.24 or newer.

```
go build -o breakwater ./cmd/breakwater
cp breakwater.example.json breakwater.json
./breakwater -config breakwater.json
```

Then ask it how it is going:

```
curl localhost:8080/_breakwater/status
```

```
{"upstreams":[{"url":"http://127.0.0.1:9001","healthy":true},{"url":"http://127.0.0.1:9002","healthy":false}],"counters":{"proxied":0,"rate_limited":0,"no_upstream":0,"bad_gateway":0},"time":"2026-11-19T02:14:05Z"}
```

Config fields: `listen`, `upstreams`, `rate_per_second` (0 turns limiting off), `burst`, `trust_forwarded`, `health_path`, `health_interval` and `health_timeout`.

## 🧪 Testing

```
go test -race -count=1 ./...
```

29 tests, all passing under the race detector. The proxy tests run against real `httptest` servers:

- Rate limiter: burst then block, refill, burst cap, independent clients, idle sweeping, zero rate, concurrent use (7).
- Pool: bad input, round-robin order, skipping unhealthy upstreams, all down, recovery (5).
- Config: defaults, duration parsing, error cases, loading from a file (4).
- Proxy: forwarding, round-robin across backends, per-client 429s and refill, spoofed headers ignored, forwarded headers, host kept, 503, 502 and marking down, status endpoint (10).
- Health checker: down and up transitions, unreachable upstreams, stopping on cancel (3).
