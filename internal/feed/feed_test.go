package feed

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"vg-racegen/internal/models"
	"vg-racegen/internal/raceutil"
)

// fakeReader is an in-memory Reader for tests. Rounds keyed by code;
// per-betoffer upcoming/recent lists are computed by time against `now`.
type fakeReader struct {
	rounds  map[string]*models.GameRound
	results map[string][]models.GameResult
	now     func() time.Time
	pingErr error
}

func newFakeReader(now func() time.Time) *fakeReader {
	return &fakeReader{
		rounds:  map[string]*models.GameRound{},
		results: map[string][]models.GameResult{},
		now:     now,
	}
}

func (f *fakeReader) add(g *models.GameRound, res []models.GameResult) {
	f.rounds[g.RoundCode] = g
	f.results[g.RoundCode] = res
}

func (f *fakeReader) GameByRoundCode(code string) (*models.GameRound, error) {
	if !strings.HasPrefix(code, "GA") {
		return nil, nil
	}
	return f.rounds[code], nil
}

func (f *fakeReader) ResultsByRoundCode(code string) ([]models.GameResult, error) {
	return f.results[code], nil
}

func (f *fakeReader) UpcomingGames(betofferID, limit int) ([]*models.GameRound, error) {
	now := f.now().UTC()
	var out []*models.GameRound
	for _, g := range f.rounds {
		if g.GameTypeID != betofferID {
			continue
		}
		end, _ := time.ParseInLocation(dtFormat, g.VideoEndDt, time.UTC)
		if end.After(now) {
			out = append(out, g)
		}
	}
	sortByStart(out, true)
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeReader) RecentResults(betofferID, limit int) ([]*models.GameRound, map[string][]models.GameResult, error) {
	now := f.now().UTC()
	var out []*models.GameRound
	for _, g := range f.rounds {
		if g.GameTypeID != betofferID {
			continue
		}
		end, _ := time.ParseInLocation(dtFormat, g.VideoEndDt, time.UTC)
		if !end.After(now) {
			out = append(out, g)
		}
	}
	sortByStart(out, false)
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	rm := map[string][]models.GameResult{}
	for _, g := range out {
		rm[g.RoundCode] = f.results[g.RoundCode]
	}
	return out, rm, nil
}

func (f *fakeReader) Ping() error { return f.pingErr }

func sortByStart(g []*models.GameRound, asc bool) {
	for i := 0; i < len(g); i++ {
		for j := i + 1; j < len(g); j++ {
			less := g[j].VideoStartDt < g[i].VideoStartDt
			if (asc && less) || (!asc && !less && g[j].VideoStartDt != g[i].VideoStartDt) {
				g[i], g[j] = g[j], g[i]
			}
		}
	}
}

// dog8Round builds a GA dog8 round (betoffer 541) with a unique race
// number, window [start,end].
func dog8Round(raceNum string, start, end time.Time) (*models.GameRound, []models.GameResult) {
	g, res := sampleRound(start, end)
	g.RoundCode = "GA541_105_2026061100" + raceNum
	for i := range res {
		res[i].GameRoundID = g.RoundCode
	}
	return g, res
}

func newTestServer(t *testing.T, reader Reader, auth AuthConfig, poller *Poller) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	deps := &Deps{
		Reader: reader,
		Poller: poller,
		Auth:   auth,
		Clock:  func() time.Time { return fixedNow },
	}
	if _, err := Register(mux, deps); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return httptest.NewServer(mux)
}

var fixedNow = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

func TestHandleCurrent_ReturnsOpenRoundGated(t *testing.T) {
	fr := newFakeReader(func() time.Time { return fixedNow })
	// One open round (current) + one already finished.
	openG, openRes := dog8Round("50", fixedNow.Add(-30*time.Second), fixedNow.Add(30*time.Second))
	finG, finRes := dog8Round("49", fixedNow.Add(-300*time.Second), fixedNow.Add(-240*time.Second))
	fr.add(openG, openRes)
	fr.add(finG, finRes)

	poller := NewPoller(fr, []string{"dog8"})
	srv := newTestServer(t, fr, AuthConfig{}, poller)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/races/current/dog8")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var dto RacePublicDTO
	json.NewDecoder(resp.Body).Decode(&dto)
	if dto.State != "open" {
		t.Fatalf("state = %q, want open (current = in-progress)", dto.State)
	}
	if dto.FinishOrder != nil {
		t.Errorf("LEAK: finishOrder on open current round")
	}
}

func TestHandleLive_FinalHasFinishOrder(t *testing.T) {
	fr := newFakeReader(func() time.Time { return fixedNow })
	finG, finRes := dog8Round("49", fixedNow.Add(-300*time.Second), fixedNow.Add(-240*time.Second))
	fr.add(finG, finRes)

	poller := NewPoller(fr, []string{"dog8"})
	srv := newTestServer(t, fr, AuthConfig{}, poller)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/races/live/dog8")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env listEnvelope
	json.NewDecoder(resp.Body).Decode(&env)
	if len(env.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(env.Items))
	}
	if env.Items[0].State != "final" || len(env.Items[0].FinishOrder) != 3 {
		t.Errorf("live item not final-with-results: %+v", env.Items[0])
	}
}

func TestHandleUpcoming_AllOpenNoLeak(t *testing.T) {
	fr := newFakeReader(func() time.Time { return fixedNow })
	for _, rn := range []string{"50", "51", "52"} {
		g, res := dog8Round(rn, fixedNow.Add(30*time.Second), fixedNow.Add(90*time.Second))
		fr.add(g, res)
	}
	poller := NewPoller(fr, []string{"dog8"})
	srv := newTestServer(t, fr, AuthConfig{}, poller)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/races/upcoming/dog8?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env listEnvelope
	json.NewDecoder(resp.Body).Decode(&env)
	if len(env.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(env.Items))
	}
	for _, it := range env.Items {
		if it.State != "open" || it.FinishOrder != nil {
			t.Errorf("LEAK on upcoming item: %+v", it)
		}
	}
}

func TestHandleCurrent_UnknownGameType400(t *testing.T) {
	fr := newFakeReader(func() time.Time { return fixedNow })
	poller := NewPoller(fr, []string{"dog8"})
	srv := newTestServer(t, fr, AuthConfig{}, poller)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/races/current/banana")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAuth_RequireKey(t *testing.T) {
	fr := newFakeReader(func() time.Time { return fixedNow })
	g, res := dog8Round("50", fixedNow.Add(-30*time.Second), fixedNow.Add(30*time.Second))
	fr.add(g, res)
	poller := NewPoller(fr, []string{"dog8"})
	auth := AuthConfig{
		RequireKey: true,
		APIKeys:    map[string]struct{}{"testkeytestkey12": {}},
	}
	srv := newTestServer(t, fr, auth, poller)
	defer srv.Close()

	// (a) no key ⇒ 401
	resp, _ := http.Get(srv.URL + "/v1/races/current/dog8")
	if resp.StatusCode != 401 {
		t.Fatalf("no-key status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// (b) valid key via header ⇒ 200
	req, _ := http.NewRequest("GET", srv.URL+"/v1/races/current/dog8", nil)
	req.Header.Set("X-API-Key", "testkeytestkey12")
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != 200 {
		t.Fatalf("keyed status = %d, want 200", resp2.StatusCode)
	}
	resp2.Body.Close()

	// (c) valid key via query ⇒ 200
	resp3, _ := http.Get(srv.URL + "/v1/races/current/dog8?apikey=testkeytestkey12")
	if resp3.StatusCode != 200 {
		t.Fatalf("query-key status = %d, want 200", resp3.StatusCode)
	}
	resp3.Body.Close()
}

func TestHealth(t *testing.T) {
	fr := newFakeReader(func() time.Time { return fixedNow })
	poller := NewPoller(fr, []string{"dog8"})
	poller.started.Store(true) // simulate Started without launching the loop
	srv := newTestServer(t, fr, AuthConfig{}, poller)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/v1/healthz")
	if resp.StatusCode != 200 {
		t.Fatalf("healthz = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp2, _ := http.Get(srv.URL + "/v1/readyz")
	if resp2.StatusCode != 200 {
		t.Fatalf("readyz = %d, want 200", resp2.StatusCode)
	}
	resp2.Body.Close()
}

func TestParseKeysCSV(t *testing.T) {
	keys, rej := ParseKeysCSV("validkey00000001, short, validkey00000002,, bad key!!")
	if len(keys) != 2 {
		t.Fatalf("keys = %d, want 2", len(keys))
	}
	if rej != 2 {
		t.Fatalf("rejected = %d, want 2 (short + 'bad key!!')", rej)
	}
}

// TestWS_HelloHeartbeatAndEvent connects a WS client, asserts the hello
// frame, then a poller-published event arrives.
func TestWS_HelloHeartbeatAndEvent(t *testing.T) {
	fr := newFakeReader(func() time.Time { return fixedNow })
	// A round that is open right now so the poller broadcasts an "open"
	// event when ticked. The poller derives the round code from raceutil
	// at fixedNow, so register the round under exactly that GA code.
	gaCode := "GA" + raceutil.CurrentRoundCode("dog8", fixedNow)
	g, res := dog8Round("50", fixedNow.Add(-10*time.Second), fixedNow.Add(50*time.Second))
	g.RoundCode = gaCode
	for i := range res {
		res[i].GameRoundID = gaCode
	}
	fr.add(g, res)

	poller := NewPoller(fr, []string{"dog8"},
		WithClock(func() time.Time { return fixedNow }),
	)
	poller.started.Store(true)

	srv := newTestServer(t, fr, AuthConfig{}, poller)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + WSPath
	u, _ := url.Parse(wsURL)
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// hello first.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read hello: %v", err)
	}
	var hello map[string]any
	json.Unmarshal(msg, &hello)
	if hello["type"] != "hello" {
		t.Fatalf("first frame type = %v, want hello", hello["type"])
	}

	// Now publish an event by ticking the poller (the client is subscribed).
	// Give the subscription a moment to register, then tick.
	time.Sleep(50 * time.Millisecond)
	poller.tick(nil)

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg2, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	var ev map[string]any
	json.Unmarshal(msg2, &ev)
	if ev["type"] != "open" {
		t.Fatalf("event type = %v, want open", ev["type"])
	}
	data, _ := json.Marshal(ev["data"])
	var live LiveResultDTO
	json.Unmarshal(data, &live)
	if live.Payload.FinishOrder != nil {
		t.Errorf("LEAK: finishOrder in open WS event")
	}
	_ = io.Discard
}

// TestWS_RequireKey401 verifies the WS upgrade is refused without a key.
func TestWS_RequireKey401(t *testing.T) {
	fr := newFakeReader(func() time.Time { return fixedNow })
	poller := NewPoller(fr, []string{"dog8"})
	auth := AuthConfig{RequireKey: true, APIKeys: map[string]struct{}{"testkeytestkey12": {}}}
	srv := newTestServer(t, fr, auth, poller)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + WSPath
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatalf("expected dial failure without key")
	}
	if resp == nil || resp.StatusCode != 401 {
		t.Fatalf("ws no-key status = %v, want 401", resp)
	}
}
