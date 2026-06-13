package feed

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"vg-racegen/internal/config"
	"vg-racegen/internal/models"
)

// listEnvelope is the response shape for the paginated list handlers
// (/upcoming, /live).
type listEnvelope struct {
	Items []RacePublicDTO `json:"items"`
}

const (
	defaultListLimit = 20
	maxListLimit     = 50
)

// roundCodeShape is the loose validator for /detail/{roundCode}.
var roundCodeShape = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// handlers carries per-server state for the REST routes.
type handlers struct {
	reader Reader
	poller *Poller
	clock  func() time.Time
	logger *log.Logger
}

func (h *handlers) now() time.Time {
	if h.clock != nil {
		return h.clock()
	}
	return time.Now()
}

// register installs the four public REST routes onto the sub-mux. Paths
// are relative because the caller mounts this under /v1/races/ with
// StripPrefix.
func (h *handlers) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /current/{gameType}", h.handleCurrent)
	mux.HandleFunc("GET /upcoming/{gameType}", h.handleUpcoming)
	mux.HandleFunc("GET /detail/{roundCode}", h.handleDetail)
	mux.HandleFunc("GET /live/{gameType}", h.handleLive)
}

func writeJSONError(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONOK(w http.ResponseWriter, body any, cacheControl string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControl)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

func cacheControlForState(state string) string {
	if state == "final" {
		return "public, max-age=30"
	}
	return "public, max-age=5"
}

// cacheControlForFullState mirrors cacheControlForState for the full DTO's
// "betting"/"running" states. Running rounds are immutable (the result is
// revealed) → cache a little longer; betting rounds change as the window
// approaches → short TTL.
func cacheControlForFullState(state string) string {
	if state == "running" {
		return "public, max-age=30"
	}
	return "public, max-age=5"
}

// parseListLimit clamps ?limit to [1, maxListLimit], defaulting to
// defaultListLimit. A supplied-but-malformed value returns an error so the
// handler can emit 400.
func parseListLimit(raw string) (int, error) {
	if raw == "" {
		return defaultListLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxListLimit {
		return 0, errors.New("bad_limit")
	}
	return n, nil
}

// handleCurrent returns the in-progress (or freshest known) GA round for
// gameType. It prefers the next-upcoming round (the one whose video is
// playing or about to), falling back to the most recent final round.
func (h *handlers) handleCurrent(w http.ResponseWriter, r *http.Request) {
	gameType := r.PathValue("gameType")
	cfg, ok := config.GAME_TYPES[gameType]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, map[string]any{"error": "unknown_game_type", "gameType": gameType})
		return
	}

	// Current = first GA round whose video has not yet ended (in-progress
	// or imminent). UpcomingGamesGA already filters VideoEndDt>now.
	games, err := h.reader.UpcomingGames(cfg.BetofferID, 1)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, map[string]any{"error": "upstream_unavailable"})
		return
	}
	var g *models.GameRound
	if len(games) > 0 {
		g = games[0]
	} else {
		// No open round — fall back to the most recent final.
		recent, _, rerr := h.reader.RecentResults(cfg.BetofferID, 1)
		if rerr != nil {
			writeJSONError(w, http.StatusServiceUnavailable, map[string]any{"error": "upstream_unavailable"})
			return
		}
		if len(recent) > 0 {
			g = recent[0]
		}
	}
	if g == nil {
		writeJSONError(w, http.StatusNotFound, map[string]any{"error": "no_round_yet", "gameType": gameType})
		return
	}

	// /current returns the FULL (TV/POS mirror) DTO so a /tv consumer can
	// reconstruct the gamepool. videoName/finish stay gated by VideoStartDt.
	results := h.resultsFor(g.RoundCode)
	dto, err := ToFull(g, results, h.now())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSONOK(w, dto, cacheControlForFullState(dto.State))
}

// handleDetail returns a single GA round by round code.
func (h *handlers) handleDetail(w http.ResponseWriter, r *http.Request) {
	roundCode := r.PathValue("roundCode")
	if !roundCodeShape.MatchString(roundCode) {
		writeJSONError(w, http.StatusBadRequest, map[string]any{"error": "bad_round_code"})
		return
	}
	g, err := h.reader.GameByRoundCode(roundCode)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, map[string]any{"error": "upstream_unavailable"})
		return
	}
	if g == nil {
		writeJSONError(w, http.StatusNotFound, map[string]any{"error": "round_not_found", "roundCode": roundCode})
		return
	}
	// /detail returns the FULL (TV/POS mirror) DTO (same surface as
	// /current); videoName/finish gated by VideoStartDt.
	results := h.resultsFor(g.RoundCode)
	dto, err := ToFull(g, results, h.now())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSONOK(w, dto, cacheControlForFullState(dto.State))
}

// handleUpcoming returns the next N open GA rounds for gameType. All
// returned rounds are open by construction (UpcomingGamesGA filters
// VideoEndDt>now), so finishOrder is gated off on every item.
func (h *handlers) handleUpcoming(w http.ResponseWriter, r *http.Request) {
	gameType := r.PathValue("gameType")
	cfg, ok := config.GAME_TYPES[gameType]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, map[string]any{"error": "unknown_game_type", "gameType": gameType})
		return
	}
	limit, err := parseListLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, map[string]any{"error": "bad_limit", "min": 1, "max": maxListLimit})
		return
	}

	games, err := h.reader.UpcomingGames(cfg.BetofferID, limit)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, map[string]any{"error": "upstream_unavailable"})
		return
	}
	now := h.now()
	items := make([]RacePublicDTO, 0, len(games))
	for _, g := range games {
		// Upcoming rounds are open; pass nil results so the gating mapper
		// has nothing to leak even if VideoEndDt has just slipped past.
		dto, derr := ToPublic(g, nil, now)
		if derr != nil {
			h.logger.Printf("[FEED] upcoming map skip %s: %v", g.RoundCode, derr)
			continue
		}
		items = append(items, *dto)
	}
	writeJSONOK(w, listEnvelope{Items: items}, "public, max-age=10")
}

// handleLive returns the last N finished GA rounds for gameType, newest
// first. These are final by time, so finishOrder + payouts are included.
func (h *handlers) handleLive(w http.ResponseWriter, r *http.Request) {
	gameType := r.PathValue("gameType")
	cfg, ok := config.GAME_TYPES[gameType]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, map[string]any{"error": "unknown_game_type", "gameType": gameType})
		return
	}
	limit, err := parseListLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, map[string]any{"error": "bad_limit", "min": 1, "max": maxListLimit})
		return
	}

	games, resultMap, err := h.reader.RecentResults(cfg.BetofferID, limit)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, map[string]any{"error": "upstream_unavailable"})
		return
	}
	now := h.now()
	items := make([]RacePublicDTO, 0, len(games))
	for _, g := range games {
		dto, derr := ToPublic(g, resultMap[g.RoundCode], now)
		if derr != nil {
			h.logger.Printf("[FEED] live map skip %s: %v", g.RoundCode, derr)
			continue
		}
		items = append(items, *dto)
	}
	writeJSONOK(w, listEnvelope{Items: items}, "public, max-age=10")
}

// resultsFor pulls the precomputed finish for a round; on error it returns
// nil so the gating mapper simply omits finishOrder (fail-safe).
func (h *handlers) resultsFor(roundCode string) []models.GameResult {
	res, err := h.reader.ResultsByRoundCode(roundCode)
	if err != nil {
		h.logger.Printf("[FEED] results lookup %s: %v", roundCode, err)
		return nil
	}
	return res
}
