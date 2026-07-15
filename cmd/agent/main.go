package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/gjcourt/modemscope/internal/hitron"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("modemscope exiting", "err", err)
		os.Exit(1)
	}
}

func run() error {
	listenAddr := os.Getenv("MODEMSCOPE_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":9104"
	}
	modemURL := os.Getenv("MODEMSCOPE_MODEM_URL")
	if modemURL == "" {
		modemURL = "https://192.168.100.1"
	}
	// Total budget for one scrape's worth of modem I/O. Keep it below the
	// Prometheus scrape timeout (10s in the ServiceMonitor) — otherwise a slow
	// modem makes Prometheus give up before modemscope_up=0 is delivered.
	budget := 8 * time.Second
	if v := os.Getenv("MODEMSCOPE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		budget = d
	}

	// Fetch makes six sequential requests, so no single one may eat the budget.
	client := hitron.NewClient(modemURL, budget)
	log := slog.Default()

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		hitron.NewCollector(client, log, budget),
	)

	mux := http.NewServeMux()
	// Concurrent scrapes are coalesced inside the collector (see hitron.fetch),
	// so the modem is protected without rejecting a scrape here —
	// MaxRequestsInFlight would 503 the second caller, which Prometheus reads as
	// the exporter being down.
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		Timeout: budget + time.Second,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Liveness is "the exporter is running", deliberately not "the modem is
		// reachable" — an unreachable modem is a metric (modemscope_up 0), not a
		// reason for Kubernetes to restart this pod.
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("metrics server listening", "addr", listenAddr, "modem", modemURL)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Warn("server shutdown", "err", err)
	}
	return nil
}
