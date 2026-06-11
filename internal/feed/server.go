package feed

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// Deps bundles everything the feed surface needs to mount its routes.
type Deps struct {
	Reader Reader
	Poller *Poller
	Auth   AuthConfig
	// WSMaxConnections caps concurrent WS clients (0 ⇒ unlimited).
	WSMaxConnections int
	// HeartbeatInterval for WS (0 ⇒ 30s).
	HeartbeatInterval time.Duration
	// Clock is injectable for tests (nil ⇒ time.Now).
	Clock  func() time.Time
	Logger *log.Logger
}

func (d *Deps) validate() error {
	if d.Reader == nil {
		return fmt.Errorf("feed: Deps.Reader is nil")
	}
	if d.Poller == nil {
		return fmt.Errorf("feed: Deps.Poller is nil")
	}
	if d.Logger == nil {
		d.Logger = log.Default()
	}
	return nil
}

// Register mounts the public REST surface at /v1/races/ behind the
// middleware chain (recover → requestID → auth). Returns a WS
// connected-count accessor after also mounting the WS upgrade handler at
// /v1/events/subscribe and the health probes at /v1/healthz, /v1/readyz.
func Register(mux *http.ServeMux, deps *Deps) (wsConnected func() int, err error) {
	if mux == nil {
		return nil, fmt.Errorf("feed.Register: mux is nil")
	}
	if deps == nil {
		return nil, fmt.Errorf("feed.Register: deps is nil")
	}
	if verr := deps.validate(); verr != nil {
		return nil, verr
	}

	// REST sub-mux mounted under /v1/races/ with StripPrefix so handlers
	// register relative paths.
	racesMux := http.NewServeMux()
	h := &handlers{
		reader: deps.Reader,
		poller: deps.Poller,
		clock:  deps.Clock,
		logger: deps.Logger,
	}
	h.register(racesMux)
	wrapped := chain(deps.Auth, deps.Logger, http.StripPrefix("/v1/races", racesMux))
	mux.Handle("/v1/races/", wrapped)

	// WS upgrade. The same auth contract gates the WS handler.
	wsConnected = RegisterWS(mux, WSConfig{
		Poller:            deps.Poller,
		Auth:              deps.Auth,
		HeartbeatInterval: deps.HeartbeatInterval,
		MaxConnections:    deps.WSMaxConnections,
		Logger:            deps.Logger,
	})

	// Health probes — NO auth, NO chain (LB/k8s probes must not be gated).
	registerHealth(mux, deps, wsConnected)

	return wsConnected, nil
}
