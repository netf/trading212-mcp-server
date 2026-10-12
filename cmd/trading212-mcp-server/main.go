// Command trading212-mcp-server serves the Trading 212 API over the Model
// Context Protocol.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/netf/trading212-mcp-server/internal/config"
	"github.com/netf/trading212-mcp-server/internal/server"
	"github.com/netf/trading212-mcp-server/internal/trading212"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "trading212-mcp-server:", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("trading212-mcp-server", flag.ContinueOnError)
	fs.SetOutput(stderr)
	envFile := fs.String("env-file", ".env", "path to an optional .env file")
	showVersion := fs.Bool("version", false, "print the version and exit")
	verbose := fs.Bool("v", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}
	if *showVersion {
		fmt.Fprintln(stderr, version)
		return nil
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	// stdout carries the stdio transport, so logs always go to stderr.
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	dotenv, err := config.ReadDotEnv(*envFile)
	if err != nil {
		return err
	}
	cfg, err := config.Load(os.LookupEnv, dotenv)
	if err != nil {
		return err
	}
	logger.Debug("loaded configuration", "config", cfg.String())

	client, err := trading212.NewClient(cfg.APIKey, cfg.APISecret, string(cfg.Environment),
		trading212.WithCacheTTLs(trading212.TTLs{
			Account:  cfg.AccountTTL,
			History:  cfg.HistoryTTL,
			Metadata: cfg.MetadataTTL,
		}))
	if err != nil {
		return fmt.Errorf("create trading 212 client: %w", err)
	}
	opts := server.Options{Version: version, Logger: logger}
	if *verbose {
		opts.ProtocolLogger = logger
	}
	srv := server.New(client, opts)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cfg.Transport {
	case config.TransportStdio:
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("serve stdio: %w", err)
		}
		return nil
	case config.TransportSSE:
		handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		return serveHTTP(ctx, logger, cfg.HTTPAddr, "/sse", handler)
	case config.TransportStreamableHTTP:
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
			&mcp.StreamableHTTPOptions{Logger: logger})
		return serveHTTP(ctx, logger, cfg.HTTPAddr, "/mcp", handler)
	default:
		return fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
}

func serveHTTP(ctx context.Context, logger *slog.Logger, addr, path string, handler http.Handler) error {
	mux := http.NewServeMux()
	mux.Handle(path, http.NewCrossOriginProtection().Handler(handler))
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "url", "http://"+addr+path)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http: %w", err)
	}
	return nil
}
