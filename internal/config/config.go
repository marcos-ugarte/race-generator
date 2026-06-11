package config

import (
	"os"
	"strconv"
)

// Env holds all application configuration loaded from environment variables.
type Env struct {
	// DS Vendor
	DSWsURL  string // env: DS_WS_URL
	DSInitID string // env: DS_INIT_ID

	// Relay
	Port                int    // env: PORT
	DBPath              string // env: DB_PATH
	StaticDataPath      string // env: STATIC_DATA_PATH
	DeviceConfigURL     string // env: DEVICE_CONFIG_URL — TV-DS fetches device config from here
	WSPublicURL         string // env: WS_PUBLIC_URL — public WS URL returned by discovery endpoint
	PollIntervalMs      int    // env: POLL_INTERVAL_MS
	KeepaliveMs         int    // env: KEEPALIVE_MS
	DatabaseURL         string // env: DATABASE_URL — optional PostgreSQL DSN for /pos-go-ds route
	PosTranslationsPath string // env: POS_TRANSLATIONS_PATH — static JSON for POS translations/translationsCommon
	SubscribeAPIKey     string // env: SUBSCRIBE_API_KEY — API key for /api/subscribe endpoint

	// Internal sniffer (legacy pre-cutover capture path). When true the
	// tv-broadcaster opens its own WS connection to the vendor for race
	// data. Post-cutover (ds-capture is sole writer) this is dead weight:
	// the connection always fails on hosts without vendor routing and
	// fills logs with "no route to host" retries. The data feed already
	// flows in from the SQLite files written by ds-capture's collectors
	// + the pg_notify ROUND-LISTENER, so the sniffer can be disabled
	// without affecting any downstream surface. Default true preserves
	// legacy behaviour; the cutover override flips it false.
	SnifferEnabled bool // env: SNIFFER_ENABLED (default true)

	// Guardian (redundant data capture from Channel C)
	GuardianEnabled     bool   // env: GUARDIAN_ENABLED
	GuardianWsURL       string // env: GUARDIAN_WS_URL
	GuardianInitID      string // env: GUARDIAN_INIT_ID
	GuardianFailoverSec int    // env: GUARDIAN_FAILOVER_SEC — seconds before failover

	// /web-ds endpoint (F1 — mobile-vendor wire replication).
	// MobileDBPath is the SQLite file written by cmd/collector-ds-pos-mobile
	// and read by the wsserver /web-ds handlers (separate handle from the
	// relay DB so the two paths can't trample each other's prepared statements).
	// Default keeps the rest of the box bootable without the mobile collector
	// — InitMobile failures are non-fatal in cmd/tv-broadcaster.
	MobileDBPath string // env: MOBILE_DB_PATH
	// WebDsAllowedOrigins is a comma-separated allow-list of Origin headers
	// for the /web-ds WS handshake. Empty (default) → no allow-list, accept
	// any origin (dev). Non-empty → coder/websocket OriginPatterns enforced.
	WebDsAllowedOrigins string // env: WEB_DS_ALLOWED_ORIGINS

	// TV-DS clock-driven broadcast (see docs/TV_DS_SCHEDULER_DESIGN.md).
	// When enabled, cmd/tv-broadcaster spawns a Scheduler goroutine that
	// emits gameResult at the moment of slot transition (matching virteon-
	// platform's pattern) instead of waiting for pg_notify. Default off
	// during rollout — listener path keeps current behaviour.
	// Setting any tuning knob does NOT enable the scheduler; only
	// TvDsSchedulerEnabled=true does.
	TvDsSchedulerEnabled    bool // env: TV_DS_SCHEDULER_ENABLED (default false)
	TvDsSchedulerTickMs     int  // env: TV_DS_SCHEDULER_TICK_MS (default 100)
	TvDsSchedulerPrefetchN  int  // env: TV_DS_SCHEDULER_PREFETCH_RETRIES (default 5)
	TvDsSchedulerPrefetchMs int  // env: TV_DS_SCHEDULER_PREFETCH_INTERVAL_MS (default 100)
}

// Load reads configuration from environment variables and applies sensible defaults.
func Load() Env {
	return Env{
		// DS Vendor
		DSWsURL:  envString("DS_WS_URL", "wss://vgcontrol.com:1224/html5-prepare"),
		DSInitID: envString("DS_INIT_ID", "15e028097a8a0234e1b501c8f8408b64"),

		// Relay
		Port:                envInt("PORT", 4097),
		DBPath:              envString("DB_PATH", "./data/relay.db"),
		StaticDataPath:      envString("STATIC_DATA_PATH", "./data/ds-static.json"),
		DeviceConfigURL:     envString("DEVICE_CONFIG_URL", "http://localhost:4101"),
		WSPublicURL:         envString("WS_PUBLIC_URL", "ws://localhost:4097/tv-ds"),
		PollIntervalMs:      envInt("POLL_INTERVAL_MS", 30000),
		KeepaliveMs:         envInt("KEEPALIVE_MS", 25000),
		DatabaseURL:         envString("DATABASE_URL", ""),
		PosTranslationsPath: envString("POS_TRANSLATIONS_PATH", "./data/pos-translations.json"),
		SubscribeAPIKey:     envString("SUBSCRIBE_API_KEY", ""),

		// Internal sniffer (legacy pre-cutover capture)
		SnifferEnabled: envBool("SNIFFER_ENABLED", true),

		// Guardian
		GuardianEnabled:     envString("GUARDIAN_ENABLED", "") == "true",
		GuardianWsURL:       envString("GUARDIAN_WS_URL", "wss://vgcontrol.com:1229/html5-prepare"),
		GuardianInitID:      envString("GUARDIAN_INIT_ID", "15e028097a8a0234e1b501c8f8408b64"),
		GuardianFailoverSec: envInt("GUARDIAN_FAILOVER_SEC", 90),

		// /web-ds
		MobileDBPath:        envString("MOBILE_DB_PATH", "./data/ds-pos-mobile.db"),
		WebDsAllowedOrigins: envString("WEB_DS_ALLOWED_ORIGINS", ""),

		// TV-DS scheduler (rollout flag — default OFF, see docs/TV_DS_SCHEDULER_DESIGN.md).
		TvDsSchedulerEnabled:    envBool("TV_DS_SCHEDULER_ENABLED", false),
		TvDsSchedulerTickMs:     envInt("TV_DS_SCHEDULER_TICK_MS", 100),
		TvDsSchedulerPrefetchN:  envInt("TV_DS_SCHEDULER_PREFETCH_RETRIES", 5),
		TvDsSchedulerPrefetchMs: envInt("TV_DS_SCHEDULER_PREFETCH_INTERVAL_MS", 100),
	}
}

// envBool returns the value of the environment variable named by key parsed
// as a bool ("true"/"1" → true, anything else → false), or defaultVal if
// the variable is not set or empty.
func envBool(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	return v == "true" || v == "1"
}

// envString returns the value of the environment variable named by key,
// or defaultVal if the variable is not set or is empty.
func envString(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// envInt returns the value of the environment variable named by key parsed as an int,
// or defaultVal if the variable is not set, empty, or cannot be parsed.
func envInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// GameConfig holds the configuration for a specific game type.
type GameConfig struct {
	BetofferID       int
	ScheduleID       int
	RoundIntervalSec int
	Competitors      int
	NumOdds          int
	EventType        string
	VideoSeconds     int
	GameType         string
	VideoQuality     string // Suffix for video files: "h50", "crf27", "h"
	VideoFolder      string // Folder name in basePath: "dog6", "dog8", "DSVideo/horse", "dog63"
}

// GAME_TYPES maps game type names to their configuration.
var GAME_TYPES = map[string]GameConfig{
	"dog6": {
		BetofferID:       141,
		ScheduleID:       101,
		RoundIntervalSec: 240,
		Competitors:      6,
		NumOdds:          36,
		EventType:        "dog",
		GameType:         "dog6",
		VideoQuality:     "h50",
		VideoFolder:      "dog6",
	},
	"dog8": {
		BetofferID:       541,
		ScheduleID:       105,
		RoundIntervalSec: 240,
		Competitors:      8,
		NumOdds:          64,
		EventType:        "dog8",
		GameType:         "dog8",
		VideoQuality:     "crf27",
		VideoFolder:      "dog8",
	},
	"horse": {
		BetofferID:       251,
		ScheduleID:       102,
		RoundIntervalSec: 320,
		Competitors:      7,
		NumOdds:          49,
		EventType:        "horse",
		GameType:         "horse",
		VideoQuality:     "h50",
		VideoFolder:      "DSVideo/horse",
	},
	// "horse_classic" — Horse 7 (4min) variant. Same competitor count and
	// odds layout as "horse" (5min20s) but a different betoffer + interval.
	// Discovered 2026-04-24 from collector probe of MAC 00:1E:06:48:69:E5;
	// videos use 7-digit finishing-order naming under DSVideo/horse/.
	// Data only flows into SQLite when a sniffer subscribed to betoffer 241
	// is running — the main relay's DS_INIT_ID only sees 251.
	"horse_classic": {
		BetofferID:       241,
		ScheduleID:       102,
		RoundIntervalSec: 240,
		Competitors:      7,
		NumOdds:          49,
		EventType:        "horsec",
		GameType:         "horse_classic",
		VideoQuality:     "h50",
		VideoFolder:      "DSVideo/horse",
	},
	"dog63": {
		BetofferID:       741,
		ScheduleID:       107,
		RoundIntervalSec: 240,
		Competitors:      6,
		NumOdds:          253,
		EventType:        "dog63",
		GameType:         "dog63",
		VideoQuality:     "h",
		VideoFolder:      "dog63",
	},
}

// VideoBasePath is the base path for video files. Configurable via VIDEO_BASE_PATH env var.
var VideoBasePath = envString("VIDEO_BASE_PATH", "/.local")

// BetofferToGameType maps betoffer IDs to their game type name.
var BetofferToGameType = func() map[int]string {
	m := make(map[int]string, len(GAME_TYPES))
	for name, cfg := range GAME_TYPES {
		m[cfg.BetofferID] = name
	}
	return m
}()

// GameTypeByBetofferID returns the game type name for the given betoffer ID.
// Returns an empty string if not found.
func GameTypeByBetofferID(id int) string {
	return BetofferToGameType[id]
}
