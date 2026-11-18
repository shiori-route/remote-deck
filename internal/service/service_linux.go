// Package service owns local storage, instance locking and goroutine lifetimes.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"remotedeck/internal/api"
	"remotedeck/internal/runner"
	"remotedeck/internal/store"
	"remotedeck/internal/worker"
)

type Config struct{ Address, DataDirectory string }
type Service struct {
	directory string
	lock      *os.File
	db        *store.Store
	listener  net.Listener
	logger    *slog.Logger
	closeOnce sync.Once
	closeErr  error
}

func Open(c Config, logger *slog.Logger) (s *Service, err error) {
	if os.Geteuid() == 0 {
		return nil, errors.New("run as a dedicated non-root user")
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil || host != "127.0.0.1" {
		return nil, errors.New("address must be 127.0.0.1:<port>")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 0 || p > 65535 {
		return nil, errors.New("invalid port")
	}
	if c.DataDirectory == "" {
		return nil, errors.New("data directory is required")
	}
	dir, err := filepath.Abs(c.DataDirectory)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	if real != dir {
		return nil, errors.New("data directory cannot contain symlinks")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, errors.New("data directory must be owned by the service user with mode 0700")
	}
	s = &Service{directory: dir, logger: logger}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
			s = nil
		}
	}()
	s.lock, err = privateFile(filepath.Join(dir, "service.lock"))
	if err != nil {
		return s, err
	}
	if err = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return s, fmt.Errorf("data directory already in use: %w", err)
	}
	f, err := privateFile(filepath.Join(dir, "jobs.db"))
	if err != nil {
		return s, err
	}
	if err = f.Close(); err != nil {
		return s, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		path := filepath.Join(dir, "jobs.db"+suffix)
		if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			return s, e
		}
		f, e := privateFile(path)
		if e != nil {
			return s, e
		}
		if e = f.Close(); e != nil {
			return s, e
		}
	}
	s.db, err = store.Open(filepath.Join(dir, "jobs.db"))
	if err != nil {
		return s, err
	}
	s.listener, err = net.Listen("tcp4", c.Address)
	if err != nil {
		return s, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = s.db.Reconcile(ctx); err != nil {
		return s, err
	}
	return s, nil
}

func privateFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || info.Sys().(*syscall.Stat_t).Nlink != 1) {
		err = errors.New("storage file must be private, regular, owned and not hard-linked")
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (s *Service) Address() string { return s.listener.Addr().String() }

// Run owns cleanup and must be called once. Open alone can be undone with Close.
func (s *Service) Run(parent context.Context) (result error) {
	defer func() { result = errors.Join(result, s.Close()) }()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	h := api.Handler{Store: s.db, LogDirectory: s.directory, Logger: s.logger}
	server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	serveDone := make(chan error, 1)
	workDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(s.listener) }()
	go func() {
		workDone <- (worker.Worker{Store: s.db, Executor: runner.Runner{Directory: s.directory}, Logger: s.logger}).Run(ctx)
	}()
	s.logger.Info("service listening", "address", s.Address())
	select {
	case <-ctx.Done():
	case err := <-serveDone:
		serveDone = nil
		if !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, err)
		}
	case err := <-workDone:
		workDone = nil
		result = errors.Join(result, err)
	}
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	if err := server.Shutdown(shutdownCtx); err != nil {
		result = errors.Join(result, err, server.Close())
	}
	stop()
	if serveDone != nil {
		if err := <-serveDone; !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, err)
		}
	}
	if workDone != nil {
		result = errors.Join(result, <-workDone)
	}
	return result
}

func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		if s.listener != nil {
			if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				s.closeErr = errors.Join(s.closeErr, err)
			}
		}
		if s.db != nil {
			s.closeErr = errors.Join(s.closeErr, s.db.Close())
		}
		if s.lock != nil {
			s.closeErr = errors.Join(s.closeErr, s.lock.Close())
		}
	})
	return s.closeErr
}
