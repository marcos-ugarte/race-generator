// Package feed is the public READ surface for vg-racegen's generated (GA)
// rounds. It serves REST + WebSocket over a single relay.db that the
// race-generator writes; the feed never writes the DB.
//
// The wire contract (RacePublicDTO / Participant / FinishOrderEntry /
// Payout / LiveResultDTO) is byte-shape-compatible with ds-capture's
// /v1/races/* API so a consumer can switch between a DS feed and the GA
// feed without changing its parser.
//
// CRITICAL fairness/GLI property — RESULT GATING. GA rounds are
// pre-computed: the race-generator persists every round with Status='F'
// and the finishOrder already baked in, regardless of whether the video
// has played yet. The feed therefore DOES NOT trust Status. It derives
// the public `state` purely from TIME:
//
//	now <  VideoEndDt  ⇒  state="open"   (omit finishOrder + payouts)
//	now >= VideoEndDt  ⇒  state="final"  (include finishOrder + payouts)
//
// This mirrors the DS vendor's O→F transition (and virtuales-go's
// broadcaster gating) so a future round's winner can never leak ahead of
// its video.
package feed

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"vg-racegen/internal/models"
)

// RacePublicDTO is the public, read-only view of a race round. Field
// order and JSON tags mirror ds-capture's raceapi.RacePublicDTO so a
// consumer sees a uniform shape across both feeds.
type RacePublicDTO struct {
	RoundCode       string             `json:"roundCode"`
	GameType        string             `json:"gameType"`
	ScheduledAt     time.Time          `json:"scheduledAt"`
	State           string             `json:"state"`
	Bonus           int                `json:"bonus"`
	Participants    []Participant      `json:"participants"`
	FinishOrder     []FinishOrderEntry `json:"finishOrder,omitempty"`
	Payouts         []Payout           `json:"payouts,omitempty"`
	ServerTimestamp time.Time          `json:"serverTimestamp"`
}

// Participant is one runner / dorsal in a race.
type Participant struct {
	Dorsal       int      `json:"dorsal"`
	Name         string   `json:"name,omitempty"`
	DisplayOrder int      `json:"displayOrder"`
	WinOdds      *float64 `json:"winOdds,omitempty"`
}

// FinishOrderEntry is a single position in a final result, pinned in
// position-ascending order. Only ever present when state == "final".
type FinishOrderEntry struct {
	Position   int      `json:"position"`
	Dorsal     int      `json:"dorsal"`
	FinishTime *float64 `json:"finishTime,omitempty"`
}

// Payout is one declared WIN payout entry. Only present when
// state == "final".
type Payout struct {
	BetType   string  `json:"betType"`
	Selection string  `json:"selection"`
	Amount    float64 `json:"amount"`
}

// LiveResultDTO is the event envelope used by the poller broadcast and the
// WS frames. Payload is a value type (snapshot integrity).
type LiveResultDTO struct {
	EventID         string        `json:"eventId"`
	RoundCode       string        `json:"roundCode"`
	GameType        string        `json:"gameType"`
	State           string        `json:"state"`
	ServerTimestamp time.Time     `json:"serverTimestamp"`
	Payload         RacePublicDTO `json:"payload"`
}

// dtFormat is the naive-UTC layout the generator persists VideoStartDt /
// VideoEndDt in (see internal/racegen/adapter/round.go).
const dtFormat = "2006-01-02 15:04:05"

var (
	errEmptyRoundCode = errors.New("feed: empty roundCode")
	errNoScheduledAt  = errors.New("feed: cannot parse VideoStartDt")
	errNoVideoEnd     = errors.New("feed: cannot parse VideoEndDt")
	errEmptyGameType  = errors.New("feed: empty gameType")
)

// stateForRound derives the gated public state from time. now < end ⇒
// "open"; now >= end ⇒ "final". This is the single fairness boundary —
// every caller goes through it, never through GameRound.Status.
func stateForRound(videoEnd time.Time, now time.Time) string {
	if now.UTC().Before(videoEnd) {
		return "open"
	}
	return "final"
}

// ToPublic projects a generated GameRound (+ its precomputed results)
// onto the gated public DTO. results may be nil/empty; they are only
// surfaced once the round is final by time.
//
// now is injected so handlers/poller/tests can pin the gating boundary
// and ServerTimestamp.
func ToPublic(g *models.GameRound, results []models.GameResult, now time.Time) (*RacePublicDTO, error) {
	if g == nil || g.RoundCode == "" {
		return nil, errEmptyRoundCode
	}
	if g.GameType == "" {
		return nil, errEmptyGameType
	}

	scheduledAt, err := time.ParseInLocation(dtFormat, g.VideoStartDt, time.UTC)
	if err != nil {
		return nil, errNoScheduledAt
	}
	videoEnd, err := time.ParseInLocation(dtFormat, g.VideoEndDt, time.UTC)
	if err != nil {
		return nil, errNoVideoEnd
	}

	state := stateForRound(videoEnd, now)

	winOdds := parseWinOdds(g.OddsJSON, g.CompetitorsCount)
	participants, err := parseCompetitors(g.CompetitorsJSON, winOdds)
	if err != nil {
		return nil, fmt.Errorf("feed.ToPublic: %w", err)
	}

	bonus := g.Bonus
	if bonus < 1 || bonus > 3 {
		bonus = 1
	}

	dto := &RacePublicDTO{
		RoundCode:       g.RoundCode,
		GameType:        g.GameType,
		ScheduledAt:     scheduledAt,
		State:           state,
		Bonus:           bonus,
		Participants:    participants,
		ServerTimestamp: now.UTC(),
	}

	// GATING: finishOrder + payouts only after the video has ended.
	if state == "final" {
		dto.FinishOrder = finishOrder(results)
		dto.Payouts = payoutsFromWinOdds(winOdds)
	}

	return dto, nil
}

// parseCompetitors decodes the generator's CompetitorsJSON — a map keyed
// by 1-based dorsal-as-string ("1".."N") with a competitor stat block —
// into participants sorted by ascending dorsal. WinOdds are joined from
// the supplied per-dorsal map; absent odds leave WinOdds nil so the JSON
// omits the field.
func parseCompetitors(competitorsJSON string, winOdds map[int]float64) ([]Participant, error) {
	out := []Participant{}
	if competitorsJSON == "" || competitorsJSON == "null" {
		return out, nil
	}
	var raw map[string]struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(competitorsJSON), &raw); err != nil {
		return nil, fmt.Errorf("competitors json: %w", err)
	}
	dorsals := make([]int, 0, len(raw))
	for k := range raw {
		d, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		dorsals = append(dorsals, d)
	}
	sort.Ints(dorsals)
	for i, d := range dorsals {
		p := Participant{
			Dorsal:       d,
			Name:         raw[strconv.Itoa(d)].Name,
			DisplayOrder: i + 1,
		}
		if o, ok := winOdds[d]; ok {
			cp := o
			p.WinOdds = &cp
		}
		out = append(out, p)
	}
	return out, nil
}

// parseWinOdds extracts the per-dorsal WIN odds from the generator's
// OddsJSON. The generator stores OddsJSON as a flat []float64 with the WIN
// odds first (one per competitor, in dorsal order). We map index i → dorsal
// i+1 for the first competitorsCount entries.
func parseWinOdds(oddsJSON string, competitorsCount int) map[int]float64 {
	out := map[int]float64{}
	if oddsJSON == "" || oddsJSON == "null" {
		return out
	}
	var arr []float64
	if err := json.Unmarshal([]byte(oddsJSON), &arr); err != nil {
		return out
	}
	n := competitorsCount
	if n <= 0 || n > len(arr) {
		n = len(arr)
	}
	for i := 0; i < n; i++ {
		out[i+1] = arr[i]
	}
	return out
}

// finishOrder pins the precomputed results in position-ascending order,
// dropping any sentinel (RunnerNumber == 0) row. FinishTime 0 ⇒ nil so
// the JSON omits the field.
func finishOrder(results []models.GameResult) []FinishOrderEntry {
	if len(results) == 0 {
		return nil
	}
	out := make([]FinishOrderEntry, 0, len(results))
	for _, r := range results {
		if r.RunnerNumber == 0 {
			continue
		}
		entry := FinishOrderEntry{
			Position: r.Position,
			Dorsal:   r.RunnerNumber,
		}
		if r.FinishTime != nil && *r.FinishTime != 0 {
			t := *r.FinishTime
			entry.FinishTime = &t
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

// payoutsFromWinOdds emits one Payout{BetType:"win"} per non-zero WIN
// odds, selection-ascending. Caller only invokes this for final rounds.
func payoutsFromWinOdds(winOdds map[int]float64) []Payout {
	if len(winOdds) == 0 {
		return nil
	}
	dorsals := make([]int, 0, len(winOdds))
	for d := range winOdds {
		dorsals = append(dorsals, d)
	}
	sort.Ints(dorsals)
	out := make([]Payout, 0, len(dorsals))
	for _, d := range dorsals {
		amt := winOdds[d]
		if amt == 0 {
			continue
		}
		out = append(out, Payout{
			BetType:   "win",
			Selection: strconv.Itoa(d),
			Amount:    amt,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
