package feed

// dto_full.go — the FULL (TV/POS mirror) projection of a GA round.
//
// RacePublicDTO ("slim") is intentionally lean: it carries only what a
// betting consumer needs (participants + win odds + gated finishOrder).
// It deliberately OMITS videoName, the complete odds vector, and the
// weather/interval cosmetics — which is too little to reconstruct a /tv
// gamepool entry.
//
// RaceFullDTO is the richer mirror (shape-compatible with ds-capture's
// "TV/POS payload mirror"): it adds betoffer, videoEndDt, roundInterval,
// weather block, the COMPLETE odds array, and the GATED reveal fields
// (videoName / finishOrder / payouts).
//
// GATING — the full DTO uses a STRICTER boundary than the slim one:
//
//	now <  VideoStartDt  ⇒  state="betting"  (videoName/finishOrder/payouts OMITTED)
//	now >= VideoStartDt  ⇒  state="running"  (revealed)
//
// videoName encodes the winner in its filename, so it is revealed at the
// SAME instant the /tv-of-prod reveals it: betting-close (VideoStartDt),
// NOT video-end. This honours "nobody sees the winner until the betting
// window closes" and means a FUTURE round (still in betting) can never
// leak videoName/finish. The slim DTO's VideoEndDt gate is left untouched
// for /live; only the full surface uses VideoStartDt.

import (
	"encoding/json"
	"fmt"
	"time"

	"vg-racegen/internal/config"
	"vg-racegen/internal/models"
)

// RaceFullDTO is the rich, /tv-reconstructable view of a GA round. It is a
// superset of RacePublicDTO's data; the gated fields (VideoName,
// FinishOrder, Payouts) appear only once now >= VideoStartDt.
type RaceFullDTO struct {
	RoundCode     string    `json:"roundCode"`
	GameType      string    `json:"gameType"`
	Betoffer      int       `json:"betoffer"`
	EventType     string    `json:"eventType"`
	ScheduledAt   time.Time `json:"scheduledAt"` // = VideoStartDt
	VideoEndDt    time.Time `json:"videoEndDt"`
	RoundInterval int       `json:"roundInterval"`
	State         string    `json:"state"` // "betting" (< VideoStartDt) or "running" (>=)
	Bonus         int       `json:"bonus"`

	// Cosmetic environment block (always present).
	Weather          string `json:"weather,omitempty"`
	Temperature      int    `json:"temperature"`
	Humidity         int    `json:"humidity"`
	Wind             string `json:"wind,omitempty"`
	CourseConditions string `json:"courseConditions,omitempty"`

	Participants []Participant `json:"participants"`

	// Odds is the COMPLETE OddsJson vector (all bet types, not just WIN).
	// Passed through verbatim from the generator. Always present (odds are
	// public during betting — only the winner-revealing fields are gated).
	Odds json.RawMessage `json:"odds,omitempty"`

	// Competitors is the COMPLETE CompetitorsJson stat block (the generator's
	// map keyed by 1-based dorsal-as-string, each value carrying the full
	// per-competitor stats: weight/numberOfRaces/numberOfWins/strikeRate/
	// last5/…). Passed through verbatim so a /tv consumer can reconstruct the
	// gamepool entry byte-for-byte. Always present (pre-race public info; it
	// reveals nothing about the winner). The slim Participants[] (name +
	// winOdds only) is kept alongside for the betting consumers.
	Competitors json.RawMessage `json:"competitors,omitempty"`

	// JackpotInfo is the COMPLETE JackpotInfoJson trend/counter block
	// (bonusValue/oldBonusValue/bonusHistory). Passed through verbatim.
	// Always present (a public counter; reveals nothing about the winner).
	JackpotInfo json.RawMessage `json:"jackpotInfo,omitempty"`

	// GATED — only when state == "running" (now >= VideoStartDt):
	VideoName   json.RawMessage    `json:"videoName,omitempty"`
	FinishOrder []FinishOrderEntry `json:"finishOrder,omitempty"`
	Payouts     []Payout           `json:"payouts,omitempty"`

	// Interval is the COMPLETE IntervalJson (per-tramo split times). It
	// reveals the race progression — i.e. the winner — so it is GATED exactly
	// like videoName/finish: present only when state == "running"
	// (now >= VideoStartDt), absent during betting.
	Interval json.RawMessage `json:"interval,omitempty"`

	ServerTimestamp time.Time `json:"serverTimestamp"`
}

// stateForFull derives the full-DTO state from time, using VideoStartDt
// (betting-close) as the reveal boundary — STRICTER than the slim DTO's
// VideoEndDt. now < start ⇒ "betting"; now >= start ⇒ "running".
func stateForFull(videoStart time.Time, now time.Time) string {
	if now.UTC().Before(videoStart) {
		return "betting"
	}
	return "running"
}

// ToFull projects a generated GameRound (+ its precomputed results) onto
// the gated full DTO. results may be nil/empty; finishOrder/payouts are
// surfaced only once the round is running by time. videoName is likewise
// withheld until VideoStartDt.
//
// now is injected so handlers/tests can pin the gating boundary.
func ToFull(g *models.GameRound, results []models.GameResult, now time.Time) (*RaceFullDTO, error) {
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

	state := stateForFull(scheduledAt, now)

	winOdds := parseWinOdds(g.OddsJSON, g.CompetitorsCount)
	participants, err := parseCompetitors(g.CompetitorsJSON, winOdds)
	if err != nil {
		return nil, fmt.Errorf("feed.ToFull: %w", err)
	}

	bonus := g.Bonus
	if bonus < 1 || bonus > 3 {
		bonus = 1
	}

	betoffer := g.GameTypeID
	eventType := g.GameType
	if gc, ok := config.GAME_TYPES[g.GameType]; ok {
		if betoffer == 0 {
			betoffer = gc.BetofferID
		}
		eventType = gc.EventType
	}

	roundInterval := g.RoundInterval
	if roundInterval == 0 {
		roundInterval = int(videoEnd.Sub(scheduledAt).Seconds())
	}

	dto := &RaceFullDTO{
		RoundCode:        g.RoundCode,
		GameType:         g.GameType,
		Betoffer:         betoffer,
		EventType:        eventType,
		ScheduledAt:      scheduledAt,
		VideoEndDt:       videoEnd,
		RoundInterval:    roundInterval,
		State:            state,
		Bonus:            bonus,
		Weather:          g.Weather,
		Temperature:      g.Temperature,
		Humidity:         g.Humidity,
		Wind:             g.Wind,
		CourseConditions: g.CourseConditions,
		Participants:     participants,
		ServerTimestamp:  now.UTC(),
	}

	// Odds: the COMPLETE vector, public during betting. Passed through
	// verbatim. (Odds reveal nothing about the winner — they are the price
	// the player bets against — so they are NOT gated.)
	if g.OddsJSON != "" && g.OddsJSON != "null" {
		dto.Odds = json.RawMessage(g.OddsJSON)
	}

	// Competitors: the COMPLETE stat block, passed through verbatim. Always
	// present (pre-race public info — names/stats reveal nothing about the
	// winner). Empty/sentinel values are omitted so the consumer can fall
	// back to the slim Participants[].
	if g.CompetitorsJSON != "" && g.CompetitorsJSON != "null" && g.CompetitorsJSON != "[]" && g.CompetitorsJSON != "{}" {
		dto.Competitors = json.RawMessage(g.CompetitorsJSON)
	}

	// JackpotInfo: the COMPLETE counter/trend block, passed through verbatim.
	// Always present (a public counter).
	if g.JackpotInfoJSON != "" && g.JackpotInfoJSON != "null" {
		dto.JackpotInfo = json.RawMessage(g.JackpotInfoJSON)
	}

	// GATING: videoName / finishOrder / payouts / interval only once running.
	// interval reveals the per-tramo progression (= the winner), so it is
	// gated on now>=VideoStartDt exactly like videoName/finish.
	if state == "running" {
		if g.VideoNameJSON != "" && g.VideoNameJSON != "null" {
			dto.VideoName = json.RawMessage(g.VideoNameJSON)
		} else if g.VideoName != "" {
			// Bare basename — wrap so the consumer always gets an object.
			b, _ := json.Marshal(map[string]string{"name": g.VideoName})
			dto.VideoName = json.RawMessage(b)
		}
		dto.FinishOrder = finishOrder(results)
		dto.Payouts = payoutsFromWinOdds(winOdds)
		if g.IntervalJSON != "" && g.IntervalJSON != "null" {
			dto.Interval = json.RawMessage(g.IntervalJSON)
		}
	}

	return dto, nil
}
