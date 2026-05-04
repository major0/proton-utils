package lumoCmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/major0/proton-cli/api/lumo"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

// serveFlags holds the parsed flags for the serve command.
type serveFlags struct {
	addr      string
	apiKey    string
	newAPIKey bool
	tlsCert   string
	tlsKey    string
	noTLS     bool
}

var sFlags serveFlags

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start a local OpenAI-compatible server backed by Proton Lumo",
	RunE:  runServe,
}

func init() {
	AddCommand(serveCmd)
	serveCmd.Flags().StringVar(&sFlags.addr, "addr", "127.0.0.1:8443", "Listen address and port")
	serveCmd.Flags().StringVar(&sFlags.apiKey, "api-key", "", "Use this API key (not persisted)")
	serveCmd.Flags().BoolVar(&sFlags.newAPIKey, "new-api-key", false, "Generate and persist a new API key")
	serveCmd.Flags().StringVar(&sFlags.tlsCert, "tls-cert", "", "Custom TLS certificate path")
	serveCmd.Flags().StringVar(&sFlags.tlsKey, "tls-key", "", "Custom TLS key path")
	serveCmd.Flags().BoolVar(&sFlags.noTLS, "no-tls", false, "Disable TLS, serve plain HTTP")
}

func runServe(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	session, err := cli.RestoreSession(ctx)
	if err != nil {
		return fmt.Errorf("no active session (run 'proton account login' first): %w", err)
	}

	client := lumo.NewClient(session)

	apiKey, err := resolveAPIKey(sFlags)
	if err != nil {
		return fmt.Errorf("resolving API key: %w", err)
	}

	certFile, keyFile, err := resolveTLS(sFlags)
	if err != nil {
		return fmt.Errorf("resolving TLS: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", chatHandler(client))
	mux.HandleFunc("GET /v1/models", modelsHandler())

	handler := authMiddleware(apiKey, loggingMiddleware(mux))
	srv := &http.Server{
		Addr:              sFlags.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown on signal.
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-sigCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	printBanner(sFlags, apiKey, certFile)

	if sFlags.noTLS {
		err = srv.ListenAndServe()
	} else {
		err = srv.ListenAndServeTLS(certFile, keyFile)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// resolveAPIKey determines the API key based on flags.
func resolveAPIKey(f serveFlags) (string, error) {
	if f.apiKey != "" {
		return f.apiKey, nil
	}
	dir := cli.ConfigFilePath()
	if dir != "" {
		// Use the directory containing the config file.
		dir = dir[:len(dir)-len("config.yaml")]
	}
	if f.newAPIKey {
		return GenerateAPIKey(dir)
	}
	return LoadOrGenerateAPIKey(dir)
}

// resolveTLS determines the TLS cert and key paths based on flags.
func resolveTLS(f serveFlags) (certFile, keyFile string, err error) {
	if f.noTLS {
		return "", "", nil
	}
	if f.tlsCert != "" && f.tlsKey != "" {
		return f.tlsCert, f.tlsKey, nil
	}
	dir := cli.ConfigFilePath()
	if dir != "" {
		dir = dir[:len(dir)-len("config.yaml")]
	}
	return LoadOrGenerateTLS(dir)
}

// printBanner writes the startup banner to stderr.
func printBanner(f serveFlags, apiKey, certFile string) {
	scheme := "https"
	if f.noTLS {
		scheme = "http"
	}
	fmt.Fprintf(os.Stderr, "\nProton Lumo server running\n")
	fmt.Fprintf(os.Stderr, "  Base URL:  %s://%s/v1\n", scheme, f.addr)
	fmt.Fprintf(os.Stderr, "  API Key:   %s\n", apiKey)
	if certFile != "" {
		fmt.Fprintf(os.Stderr, "  TLS Cert:  %s\n", certFile)
	}
	fmt.Fprintf(os.Stderr, "\n")
}
