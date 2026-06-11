package models

import "encoding/json"

// GameRound represents a race/game stored in SQLite.
type GameRound struct {
	RoundCode        string
	GameTypeID       int
	GameType         string
	RaceNumber       string
	RaceDate         string
	Status           string // "O" (open) or "F" (finished)
	CompetitorsCount int
	CompetitorsJSON  string
	OddsJSON         string
	WinOddsJSON      string
	Bonus            int
	VideoName        string
	VideoNameJSON    string // Raw videoname object from vendor
	Weather          string
	Temperature      int
	Humidity         int
	Wind             string
	CourseConditions string
	IntervalJSON     string
	JackpotInfoJSON  string
	VideoStartDt     string
	VideoEndDt       string
	RoundInterval    int
	ScheduledAt      string
	CreatedAt        string
	FinishedAt       *string

	// HorseExtras holds horse-specific fields captured from the vendor gameResult.
	// Not persisted to SQLite — populated only during the broadcast path.
	// Fields: gateOpen, overlayStart, overlayEnd, overlayType, colorStarter1..16, distance.
	HorseExtras map[string]interface{} `json:"-"`
}

// GameResult represents finish positions for a race.
type GameResult struct {
	GameRoundID  string
	Position     int
	RunnerNumber int
	FinishTime   *float64
}

// WSMessage is used for initial msgType detection.
type WSMessage struct {
	MsgID   interface{} `json:"msgId"`
	MsgType string      `json:"msgType"`
}

// InitRequest from TV client.
type InitRequest struct {
	MsgID        int    `json:"msgId"`
	MsgType      string `json:"msgType"`
	DeviceID     string `json:"deviceId"`
	DeviceType   string `json:"deviceType"`
	HistoryGames int    `json:"historyGames"`
	FutureGames  int    `json:"futureGames"`
	Version      string `json:"version"`
	ClientDt     string `json:"clientDt"`
}

// GameRoundRequest from client.
type GameRoundRequest struct {
	MsgID        int    `json:"msgId"`
	MsgType      string `json:"msgType"`
	GameID       string `json:"gameId,omitempty"`
	BetofferID   int    `json:"betofferId"`
	HistoryGames int    `json:"historyGames"`
	FutureGames  int    `json:"futureGames"`
}

// TimeRequest from client.
type TimeRequest struct {
	MsgID    int    `json:"msgId"`
	MsgType  string `json:"msgType"`
	ClientDt string `json:"clientDt"`
}

// GamePoolEntry is a single game in the gamepool response.
// Fields match the DS vendor protocol exactly.
type GamePoolEntry struct {
	ID               string          `json:"id"`
	IDBetOffer       int             `json:"idBetoffer"`
	IDRace           int             `json:"idRace"`
	IDSchedule       string          `json:"idSchedule"`
	EventType        string          `json:"eventType"`
	RoundInterval    int             `json:"roundInterval"`
	VideoStartDt     string          `json:"videoStartDt"`
	VideoEndDt       string          `json:"videoEndDt"`
	Odds             json.RawMessage `json:"odds"`
	Competitors      json.RawMessage `json:"competitors"`
	Finish           json.RawMessage `json:"finish"`
	Interval         json.RawMessage `json:"interval"`
	Bonus            *int            `json:"bonus"`
	CourseConditions string          `json:"courseConditions"`
	Weather          string          `json:"weather"`
	Temperature      int             `json:"temperature"`
	Humidity         int             `json:"humidity"`
	Wind             string          `json:"wind"`
	VideoName        json.RawMessage `json:"videoname"`
	JackpotInfo      json.RawMessage `json:"jackpotInfo"`
	ITCodeEvent      json.RawMessage `json:"it_code_event"`
	ITCodeSchedule   json.RawMessage `json:"it_code_schedule"`
	CreDt            string          `json:"creDt"`
}

// DeviceLoginRequest from POS client.
type DeviceLoginRequest struct {
	MsgID      int    `json:"msgId"`
	MsgType    string `json:"msgType"`
	DeviceType string `json:"deviceType"`
	DeviceID   string `json:"deviceId"`
	UniqueID   string `json:"uniqueId"`
	Version    string `json:"version"`
	ClientDt   string `json:"clientDt"`
}

// HealthResponse is returned by /health.
type HealthResponse struct {
	Status    string          `json:"status"`
	WSClients map[string]int  `json:"wsClients"`
	Uptime    int64           `json:"uptime"`
	Games     []GameTypeStats `json:"games"`
}

// GameTypeStats holds counts per game type.
type GameTypeStats struct {
	GameType string `json:"GameType"`
	Total    int    `json:"total"`
	Finished int    `json:"finished"`
}
