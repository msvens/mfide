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
	"github.com/msvens/mfide/internal/syncer"
	"go.uber.org/zap"
)

// statusStore is what /health reads; *dao.PGDB is one.
type statusStore interface {
	Ping() error
	Status() (*dao.Status, error)
}

type mserver struct {
	pg         statusStore
	auth       *auth
	players    playerStore
	r          *http.ServeMux
	l          *zap.SugaredLogger
	prefixPath string
}

func newServer(prefixPath string, pg statusStore, a *auth, players playerStore, logger *zap.SugaredLogger) *mserver {
	s := &mserver{pg: pg, auth: a, players: players, r: http.NewServeMux(), l: logger, prefixPath: prefixPath}
	s.routes()
	return s
}

// syncShutdownWait is how long shutdown waits for a sync in progress to reach a
// step boundary.
const syncShutdownWait = 10 * time.Second

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

	var at syncer.Clock
	if config.SyncEnabled() {
		if at, err = syncer.ParseClock(config.SyncAt()); err != nil {
			l.Fatalw("invalid config", zap.Error(err))
		}
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

	syncCtx, stopSync := context.WithCancel(context.Background())
	var syncDone <-chan struct{}
	if config.SyncEnabled() {
		syncDone = syncer.Schedule(syncCtx, pg, syncer.OptionsFromConfig(), at, l)
	} else {
		l.Info("sync: disabled (sync.enabled); run `mfide sync` yourself")
	}

	<-done
	l.Info("shutting down server")
	stopSync()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		l.Errorw("server shutdown failed", zap.Error(err))
	}
	if syncDone != nil {
		select {
		case <-syncDone:
		case <-time.After(syncShutdownWait):
			// Closing the pool would wait for the sync's queries; exiting ends them,
			// and Postgres rolls back the open transaction.
			l.Warn("sync still running; exiting without waiting (its transaction rolls back)")
			return
		}
	}
	if err := pg.Close(); err != nil {
		l.Errorw("database close failed", zap.Error(err))
	}
	l.Info("server exited properly")
}
