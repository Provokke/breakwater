package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Provokke/breakwater/internal/proxy"
)

func main() {
	configPath := flag.String("config", "breakwater.json", "path to the JSON config file")
	flag.Parse()
	logger := log.New(os.Stderr, "breakwater: ", log.LstdFlags)

	cfg, err := proxy.LoadConfig(*configPath)
	if err != nil {
		logger.Fatal(err)
	}
	pool, err := proxy.NewPool(cfg.Upstreams)
	if err != nil {
		logger.Fatal(err)
	}
	var limiter *proxy.Limiter
	if cfg.RatePerSecond > 0 {
		limiter = proxy.NewLimiter(cfg.RatePerSecond, cfg.Burst, nil)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	checker := proxy.NewChecker(pool, cfg.HealthPath, cfg.HealthInterval.Duration, cfg.HealthTimeout.Duration)
	checker.OnChange = func(u *proxy.Upstream, healthy bool) {
		logger.Printf("upstream %s healthy=%v", u.URL.Host, healthy)
	}
	go checker.Run(ctx)

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           proxy.New(proxy.Options{Pool: pool, Limiter: limiter, TrustForwarded: cfg.TrustForwarded, Logger: logger}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()

	logger.Printf("listening on %s with %d upstreams", cfg.Listen, len(cfg.Upstreams))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatal(err)
	}
}

