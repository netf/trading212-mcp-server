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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/netf/trading212-mcp-server/internal/config"
	"github.com/netf/trading212-mcp-server/internal/httpserver"
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

	dotenv, err := config.ReadDotEnv(*envFile)
	if err != nil {
		return err
	}
	cfg, err := config.Load(os.LookupEnv, dotenv)
	if err != nil {
		return err
	}
	if *verbose {
		cfg.LogLevel = slog.LevelDebug
	}
	logger := newLogger(stderr, cfg.LogLevel, cfg.LogFormat)
	logStartup(logger, cfg)

	client, err := trading212.NewClient(cfg.APIKey, cfg.APISecret, string(cfg.Environment),
		trading212.WithCacheTTLs(trading212.TTLs{
			Account:  cfg.AccountTTL,
			History:  cfg.HistoryTTL,
			Metadata: cfg.MetadataTTL,
		}),
		trading212.WithLogger(logger))
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

	// With bearer auth on, the SDK's localhost Host-header check is redundant
	// (a DNS-rebinding page cannot supply the token) and would reject
	// requests forwarded by a local tunnel such as cloudflared.
	authOn := len(cfg.AuthTokens) > 0
	getServer := func(*http.Request) *mcp.Server { return srv }
	var (
		path    string
		handler http.Handler
	)
	switch cfg.Transport {
	case config.TransportStdio:
		logger.Info("serving on stdio")
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("serve stdio: %w", err)
		}
		logger.Info("stopped")
		return nil
	case config.TransportSSE:
		path = "/sse"
		handler = mcp.NewSSEHandler(getServer, &mcp.SSEOptions{DisableLocalhostProtection: authOn})
	case config.TransportStreamableHTTP:
		path = "/mcp"
		handler = mcp.NewStreamableHTTPHandler(getServer,
			&mcp.StreamableHTTPOptions{Logger: logger, DisableLocalhostProtection: authOn})
	default:
		return fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
	handler = httpserver.Handler(handler, httpserver.Options{Path: path, Tokens: cfg.AuthTokens, Logger: logger})
	if err := httpserver.Serve(ctx, logger, cfg.HTTPAddr, handler); err != nil {
		return err //nolint:wrapcheck // already wrapped with context by httpserver.
	}
	logger.Info("stopped")
	return nil
}

// newLogger writes to w, which is stderr in production: stdout carries the
// stdio transport.
func newLogger(w io.Writer, level slog.Level, format config.LogFormat) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if format == config.LogFormatJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func logStartup(logger *slog.Logger, cfg config.Config) {
	attrs := []any{
		"version", version,
		"environment", cfg.Environment,
		"transport", cfg.Transport,
		"log_level", cfg.LogLevel.String(),
	}
	if cfg.Transport.HTTP() {
		attrs = append(attrs, "addr", cfg.HTTPAddr, "auth", !cfg.AuthDisabled, "auth_clients", cfg.AuthClientNames())
	}
	logger.Info("starting trading212-mcp-server", attrs...)
	logger.Debug("loaded configuration", "config", cfg.String())

	if !cfg.Transport.HTTP() {
		return
	}
	if cfg.AuthDisabled {
		logger.Warn("authentication is disabled; anyone who can reach " + cfg.HTTPAddr + " can use the Trading 212 account")
	}
	if cfg.Environment == config.EnvironmentLive {
		logger.Warn("serving a live account over HTTP; authenticated clients can place orders and change pies " +
			"unless the Trading 212 API key lacks those permissions")
	}
}
