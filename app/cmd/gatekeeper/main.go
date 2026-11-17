// Command gatekeeper runs the deployment gate as an HTTP server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gatekeeper/internal/config"
	"gatekeeper/internal/server"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "gatekeeper:", err)
		os.Exit(1)
	}
}

// options are the command-line settings.
type options struct {
	configPath string
	listen     string
	registry   string
	debug      bool
}

// parseFlags reads the command line. It returns flag.ErrHelp when the user
// asked for the usage text.
func parseFlags(args []string, out io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("gatekeeper", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.configPath, "config", "config.json", "path to the configuration file")
	fs.StringVar(&o.listen, "listen", "", "address to listen on (overrides the configuration file)")
	fs.StringVar(&o.registry, "registry", "", "registry assumed for images that do not name one (overrides the configuration file)")
	fs.BoolVar(&o.debug, "debug", false, "enable the debug endpoints (overrides the configuration file)")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return o, nil
}

// loadConfig reads the configuration file and applies the command-line
// overrides on top of it.
func loadConfig(o options) (config.Config, error) {
	cfg, err := config.Load(o.configPath)
	if err != nil {
		return config.Config{}, err
	}
	if o.listen != "" {
		cfg.Listen = o.listen
	}
	if o.registry != "" {
		cfg.Registry = o.registry
	}
	if o.debug {
		cfg.Debug = true
	}
	return cfg, nil
}

func run(args []string, out io.Writer) error {
	o, err := parseFlags(args, out)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	cfg, err := loadConfig(o)
	if err != nil {
		return err
	}
	handler, err := server.NewServer(cfg)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
