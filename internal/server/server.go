package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/msvens/mfide/internal/config"
	"github.com/msvens/mfide/internal/dao"
	"go.uber.org/zap"
)

type mserver struct {
	pg         *dao.PGDB
	auth       *auth
	players    playerStore
	r          *http.ServeMux
	l          *zap.SugaredLogger
	prefixPath string
}

func newServer(prefixPath string, pg *dao.PGDB, a *auth, players playerStore, logger *zap.SugaredLogger) *mserver {
	s := &mserver{pg: pg, auth: a, players: players, r: http.NewServeMux(), l: logger, prefixPath: prefixPath}
	s.routes()
	return s
}

// StartMServer runs the HTTP server until SIGINT/SIGTERM.
func StartMServer() {
	zl, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}
	defer func() { _ = zl.Sync() }()
	l := zl.Sugar()

	if err := config.InitConfig(); err != nil {
		l.Fatalw("could not init config", zap.Error(err))
	}

	pg, err := dao.NewPGDB()
	if err != nil {
		l.Fatalw("could not connect to database", zap.Error(err))
	}
	if err := pg.Check(); err != nil {
		l.Fatalw("database schema mismatch", zap.Error(err))
	}

	a := newAuth(pg.ApiKey, config.ApiKeyCacheTTL(), config.ApiIPRatePerMin())
	s := newServer(config.ServerPrefix(), pg, a, pg.Player, l)
	srv := &http.Server{Addr: config.ServerAddr(), Handler: s.r}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.Fatalw("listen", zap.Error(err))
		}
	}()
	l.Infow("server started", "addr", srv.Addr)

	<-done
	l.Info("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		l.Errorw("server shutdown failed", zap.Error(err))
	}
	if err := pg.Close(); err != nil {
		l.Errorw("database close failed", zap.Error(err))
	}
	l.Info("server exited properly")
}
