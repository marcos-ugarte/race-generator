// Command feed is the public READ surface for vg-racegen's generated (GA)
// rounds. It is a standalone HTTP/WS process that reads a single relay.db
// (the race-generator's output) and never writes it.
//
// Surface mounted:
//
//	GET /v1/races/{current,upcoming,detail,live}/{...}   — JSON
//	GET /v1/events/subscribe                             — WebSocket
//	GET /v1/healthz, /v1/readyz                          — probes
//
// Real-time push: a single 1 s poller tick (no Postgres / pg_notify). The
// poller computes the current round per game type via raceutil (GA
// prefix), gates results by time (VideoEndDt), and fans transitions out
// to WS subscribers.
//
// Env (defaults shown):
//
//	RACEGEN_FEED_PORT            4198
//	DB_PATH                      ./data/relay.db   (same relay.db as the generator)
//	RACEGEN_FEED_TICK_MS         1000
//	RACEGEN_FEED_GAMETYPES       dog8,dog6,horse_classic  (config.SupportedGameTypes)
//	RACEGEN_API_KEYS             ""    (CSV; partner keys)
//	RACEGEN_ADMIN_KEYS           ""    (CSV; admin keys, also pass the public gate)
//	RACEGEN_PUBLIC_REQUIRE_KEY   false (true ⇒ REST + WS require a valid key)
//	RACEGEN_WS_MAX_CONNECTIONS   500
//	RACEGEN_WS_HEARTBEAT_SEC     30
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vg-racegen/internal/feed"
	racegencfg "vg-racegen/internal/racegen/config"
	"vg-racegen/internal/sqlite"
)

// version and commit are injected at build time via
//
//	-ldflags "-X main.version=... -X main.commit=..."
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	port := envInt("RACEGEN_FEED_PORT", 4198)
	dbPath := getenvDefault("DB_PATH", "./data/relay.db")
	tickMS := envInt("RACEGEN_FEED_TICK_MS", 1000)
	gameTypes := parseGameTypes(os.Getenv("RACEGEN_FEED_GAMETYPES"))
	wsMax := envInt("RACEGEN_WS_MAX_CONNECTIONS", 500)
	heartbeatSec := envInt("RACEGEN_WS_HEARTBEAT_SEC", 30)
	requireKey := envBool("RACEGEN_PUBLIC_REQUIRE_KEY", false)

	apiKeys, apiRej := feed.ParseKeysCSV(os.Getenv("RACEGEN_API_KEYS"))
	adminKeys, adminRej := feed.ParseKeysCSV(os.Getenv("RACEGEN_ADMIN_KEYS"))

	log.Println("====================================")
	log.Println("  vg-racegen/feed")
	log.Println("====================================")
	log.Printf("build:        %s (%s)", version, commit)
	log.Printf("port:         :%d", port)
	log.Printf("db:           %s (read-only consumer)", dbPath)
	log.Printf("tick:         %dms", tickMS)
	log.Printf("gameTypes:    %v", gameTypes)
	log.Printf("require-key:  %t (api=%d admin=%d keys; rejected api=%d admin=%d)",
		requireKey, len(apiKeys), len(adminKeys), apiRej, adminRej)
	if requireKey && len(apiKeys) == 0 && len(adminKeys) == 0 {
		log.Printf("WARNING: RACEGEN_PUBLIC_REQUIRE_KEY=true but no valid keys parsed — every request will 401")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Open relay.db. sqlite.Init is idempotent (CREATE TABLE IF NOT
	// EXISTS) and the generator owns the schema; the feed is a reader and
	// never calls any Upsert/Save path.
	if err := sqlite.Init(dbPath); err != nil {
		log.Fatalf("FATAL: open relay.db (%s): %v", dbPath, err)
	}
	defer func() { _ = sqlite.Close() }()

	reader := feed.NewSQLiteReader()

	poller := feed.NewPoller(reader, gameTypes,
		feed.WithInterval(time.Duration(tickMS)*time.Millisecond),
	)
	poller.Start(ctx)
	defer poller.Stop()

	deps := &feed.Deps{
		Reader: reader,
		Poller: poller,
		Auth: feed.AuthConfig{
			RequireKey: requireKey,
			APIKeys:    apiKeys,
			AdminKeys:  adminKeys,
		},
		WSMaxConnections:  wsMax,
		HeartbeatInterval: time.Duration(heartbeatSec) * time.Second,
	}

	mux := http.NewServeMux()
	if _, err := feed.Register(mux, deps); err != nil {
		log.Fatalf("FATAL: feed.Register: %v", err)
	}

	srv := &http.Server{
		Addr:    ":" + strconv.Itoa(port),
		Handler: mux,
		// No WriteTimeout — it would kill long-lived WS connections.
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown.
	go func() {
		<-ctx.Done()
		log.Println("[FEED] shutdown signal — draining (5s)")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("[FEED] listening on :%d", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("FATAL: ListenAndServe: %v", err)
	}
	log.Println("[FEED] stopped")
}

// parseGameTypes splits the CSV env, defaulting to the canonical
// generated-game list when empty.
func parseGameTypes(csv string) []string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return racegencfg.SupportedGameTypes()
	}
	out := make([]string, 0)
	for _, p := range strings.Split(csv, ",") {
		if g := strings.TrimSpace(p); g != "" {
			out = append(out, g)
		}
	}
	if len(out) == 0 {
		return racegencfg.SupportedGameTypes()
	}
	return out
}

func getenvDefault(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
