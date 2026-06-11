package feed

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"vg-racegen/internal/config"
	"vg-racegen/internal/models"
	"vg-racegen/internal/raceutil"
)

// Reader is the read-only seam the poller and handlers use to pull GA
// rounds from relay.db. The production implementation (sqliteReader)
// delegates to the package-level GA helpers in internal/sqlite; tests
// substitute a fake.
type Reader interface {
	// GameByRoundCode returns the GA round for a code, or (nil, nil) when
	// absent. Implementations must only return GA-prefixed rounds.
	GameByRoundCode(roundCode string) (*models.GameRound, error)
	// ResultsByRoundCode returns the precomputed finish for a GA round.
	ResultsByRoundCode(roundCode string) ([]models.GameResult, error)
	// UpcomingGames returns the next `limit` GA rounds for a betoffer
	// whose video has not yet ended, ASC by VideoStartDt.
	UpcomingGames(betofferID, limit int) ([]*models.GameRound, error)
	// RecentResults returns the last `limit` finished GA rounds for a
	// betoffer plus their results.
	RecentResults(betofferID, limit int) ([]*models.GameRound, map[string][]models.GameResult, error)
	// Ping does a cheap readability probe for /v1/readyz.
	Ping() error
}

// Poller drives result-transition detection by a single 1 s tick (no
// Postgres / pg_notify in vg-racegen). Each tick, per game type, it
// computes the current round code with raceutil (GA prefix), loads the
// round from relay.db, gates the state by time, and broadcasts to WS
// subscribers when the (round, state) tuple changes.
type Poller struct {
	reader    Reader
	gameTypes []string
	interval  time.Duration
	clock     func() time.Time
	logger    *log.Logger

	mu        sync.RWMutex
	subs      map[uint64]*subscriber
	nextSubID uint64

	eventSeq uint64 // atomic

	// lastByGame dedups: the last (round, state) tuple broadcast per
	// game type.
	lastByGame map[string]lastEmitted

	startOnce sync.Once
	stopOnce  sync.Once
	doneOnce  sync.Once
	started   atomic.Bool
	stopCh    chan struct{}
	doneCh    chan struct{}

	ticks atomic.Uint64 // observability: completed ticks
}

type lastEmitted struct {
	round string
	state string
}

type subscriber struct {
	id   uint64
	ch   chan LiveResultDTO
	once sync.Once
}

const subBufferSize = 16

// Option configures a Poller.
type Option func(*Poller)

// WithInterval overrides the default 1 s tick.
func WithInterval(d time.Duration) Option {
	return func(p *Poller) { p.interval = d }
}

// WithClock injects a clock for tests.
func WithClock(c func() time.Time) Option {
	return func(p *Poller) { p.clock = c }
}

// WithLogger overrides the default logger.
func WithLogger(l *log.Logger) Option {
	return func(p *Poller) { p.logger = l }
}

// NewPoller constructs a Poller over the given reader and game types.
// gameTypes that are not in config.GAME_TYPES are skipped at tick time.
func NewPoller(reader Reader, gameTypes []string, opts ...Option) *Poller {
	p := &Poller{
		reader:     reader,
		gameTypes:  append([]string(nil), gameTypes...),
		interval:   time.Second,
		clock:      time.Now,
		logger:     log.Default(),
		subs:       make(map[uint64]*subscriber),
		lastByGame: make(map[string]lastEmitted),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	for _, o := range opts {
		o(p)
	}
	if p.interval <= 0 {
		p.interval = time.Second
	}
	if p.clock == nil {
		p.clock = time.Now
	}
	if p.logger == nil {
		p.logger = log.Default()
	}
	return p
}

// Start launches the tick loop. Idempotent.
func (p *Poller) Start(ctx context.Context) {
	p.startOnce.Do(func() {
		p.started.Store(true)
		go p.run(ctx)
	})
}

// Stop signals shutdown and waits (bounded) for the loop to exit. Safe to
// call twice; returns immediately if Start was never called.
func (p *Poller) Stop() {
	p.stopOnce.Do(func() { close(p.stopCh) })
	if !p.started.Load() {
		p.doneOnce.Do(func() { close(p.doneCh) })
		return
	}
	timeout := 2 * p.interval
	if timeout < 200*time.Millisecond {
		timeout = 200 * time.Millisecond
	}
	select {
	case <-p.doneCh:
	case <-time.After(timeout):
	}
}

// Started reports whether Start has been invoked (for /v1/readyz).
func (p *Poller) Started() bool { return p.started.Load() }

// Ticks returns the number of completed ticks (observability).
func (p *Poller) Ticks() uint64 { return p.ticks.Load() }

// SubscriberCount returns the current subscriber count.
func (p *Poller) SubscriberCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.subs)
}

// Subscribe registers a new subscriber. ch streams events; unsubscribe is
// idempotent. (No replay ring — GA consumers reconnect and poll
// /v1/races/current for the warm snapshot.)
func (p *Poller) Subscribe() (ch <-chan LiveResultDTO, unsubscribe func()) {
	p.mu.Lock()
	p.nextSubID++
	id := p.nextSubID
	s := &subscriber{id: id, ch: make(chan LiveResultDTO, subBufferSize)}
	p.subs[id] = s
	p.mu.Unlock()

	unsubscribe = func() {
		p.mu.Lock()
		if existing, ok := p.subs[id]; ok {
			delete(p.subs, id)
			existing.once.Do(func() { close(existing.ch) })
		}
		p.mu.Unlock()
	}
	return s.ch, unsubscribe
}

func (p *Poller) run(ctx context.Context) {
	defer p.doneOnce.Do(func() { close(p.doneCh) })

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.tick(ctx) // immediate first tick

	for {
		select {
		case <-ctx.Done():
			p.shutdown()
			return
		case <-p.stopCh:
			p.shutdown()
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *Poller) shutdown() {
	p.mu.Lock()
	for id, s := range p.subs {
		s.once.Do(func() { close(s.ch) })
		delete(p.subs, id)
	}
	p.mu.Unlock()
}

// tick computes the current round per game type, detects transitions, and
// broadcasts. Once a round reaches "final" it is terminal for that game
// type until a new round code appears — avoids re-broadcasting the same
// finished round forever.
func (p *Poller) tick(ctx context.Context) {
	defer p.ticks.Add(1)
	now := p.clock().UTC()

	for _, gt := range p.gameTypes {
		if _, ok := config.GAME_TYPES[gt]; !ok {
			continue
		}
		code := raceutil.CurrentRoundCode(gt, now)
		if code == "" {
			continue
		}
		gaCode := "GA" + code

		prev := p.peekLast(gt)
		// If the current round is already final-emitted, nothing changes
		// until raceutil rolls the code forward.
		if prev.round == gaCode && prev.state == "final" {
			continue
		}

		g, err := p.reader.GameByRoundCode(gaCode)
		if err != nil {
			p.logger.Printf("[FEED/poller] GameByRoundCode(%s) failed: %v", gaCode, err)
			continue
		}
		if g == nil {
			// Round not generated yet — skip silently (the generator may
			// be backfilling). Avoid spamming logs.
			continue
		}

		var results []models.GameResult
		// Results are only needed (and only surfaced) once the round is
		// final by time. Fetch eagerly so the gating mapper has them.
		if r, rerr := p.reader.ResultsByRoundCode(gaCode); rerr == nil {
			results = r
		}

		dto, err := ToPublic(g, results, now)
		if err != nil {
			p.logger.Printf("[FEED/poller] ToPublic(%s) skipped: %v", gaCode, err)
			continue
		}

		cur := lastEmitted{round: gaCode, state: dto.State}
		if cur == prev {
			continue
		}
		p.setLast(gt, cur)

		ev := LiveResultDTO{
			EventID:         p.nextEventID(),
			RoundCode:       dto.RoundCode,
			GameType:        dto.GameType,
			State:           dto.State,
			ServerTimestamp: dto.ServerTimestamp,
			Payload:         *dto,
		}
		p.broadcast(ev)
	}
	_ = ctx
}

func (p *Poller) peekLast(gt string) lastEmitted {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastByGame[gt]
}

func (p *Poller) setLast(gt string, le lastEmitted) {
	p.mu.Lock()
	p.lastByGame[gt] = le
	p.mu.Unlock()
}

// nextEventID emits a strictly monotonic ID (counter-first so it sorts
// lexicographically even under a frozen test clock).
func (p *Poller) nextEventID() string {
	c := atomic.AddUint64(&p.eventSeq, 1)
	return fmt.Sprintf("%016x-%020d", c, p.clock().UTC().UnixNano())
}

// broadcast fans an event out to every subscriber. Non-blocking with
// drop-oldest so a slow subscriber cannot stall the poller. Sends happen
// under the read lock; unsubscribe/shutdown take the write lock before
// closing, serialising send-vs-close.
func (p *Poller) broadcast(ev LiveResultDTO) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, s := range p.subs {
		select {
		case s.ch <- ev:
		default:
			select {
			case <-s.ch:
			default:
			}
			select {
			case s.ch <- ev:
			default:
			}
		}
	}
}
