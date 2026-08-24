package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	GRACE_PERIOD      = 30 * time.Second
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = time.Minute
)

type HttpServer struct {
	server *http.Server
	Done   chan error
}

func NewHttpServer(handler http.Handler, addr string) HttpServer {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	return HttpServer{
		server: server,
		Done:   make(chan error, 1),
	}
}

func (s *HttpServer) Start() error {
	addr := s.server.Addr

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("error listening on %s: %w", addr, err)
	}

	slog.Info("listening on " + addr)
	go func() {
		err := s.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		s.Done <- err
	}()

	return nil
}

func (s *HttpServer) Shutdown() error {
	slog.Info("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), GRACE_PERIOD)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		if closeErr := s.server.Close(); closeErr != nil {
			return errors.Join(err, closeErr)
		}
		return err
	}
	return nil
}
