// Package app composes the separate Super entry without importing the legacy CLI,
// server, Session, model configuration or execution runtime.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/konglong87/super-niuma/internal/super/httpapi"
	"github.com/konglong87/super-niuma/internal/super/sqlitestore"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

const (
	headerTimeout   = 5 * time.Second
	requestTimeout  = 15 * time.Second
	idleTimeout     = 60 * time.Second
	shutdownTimeout = 5 * time.Second
	maxHeaderBytes  = 16 * 1024
	apiPrefix       = "/api/super/v1/"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts, err := parseOptions(args, stdout)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if opts.Version {
		_, err = fmt.Fprintln(stdout, "super-niuma", Version)
		return err
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	listener, err := net.Listen("tcp", opts.Address)
	if err != nil {
		return errors.New("cannot bind Super listener; check the address and port")
	}
	defer listener.Close()
	store, err := sqlitestore.Open(ctx, filepath.Join(opts.StateDirectory, DatabaseName))
	if err != nil {
		return errors.New("cannot open Super state; check the directory and permissions")
	}
	defer store.Close()
	service := workorder.NewService(store, logger)
	api := httpapi.NewHandler(httpapi.Config{Service: service, AllowedHost: listener.Addr().String(), Logger: logger})
	handler := securityHeaders(routeHandler(api, protectHost(listener.Addr().String(), staticHandler())))
	address := "http://" + listener.Addr().String()
	logger.Info("Super intake ready", "action", "super.started", "url", address, "execution_enabled", false)
	if _, err := fmt.Fprintf(stdout, "Super Niuma intake-only: %s\nExecution is disabled; existing sessions are not opened.\n", address); err != nil {
		return err
	}
	return serve(ctx, listener, handler, logger)
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler, logger *slog.Logger) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: headerTimeout, ReadTimeout: requestTimeout,
		WriteTimeout: requestTimeout, IdleTimeout: idleTimeout, MaxHeaderBytes: maxHeaderBytes}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("Super HTTP server stopped unexpectedly")
	case <-ctx.Done():
		stopCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(stopCtx)
		if shutdownErr != nil {
			_ = server.Close()
		}
		<-done
		logger.Info("Super intake stopped", "action", "super.stopped")
		if shutdownErr != nil {
			return errors.New("Super shutdown exceeded its deadline")
		}
		return nil
	}
}
