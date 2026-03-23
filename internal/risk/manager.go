package risk

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go-tec/internal/hub"
	"go-tec/internal/signal"
)

// ManagerConfig holds tunable risk parameters.
type ManagerConfig struct {
	Bankroll        float64 // starting paper balance (default $100)
	FractionalKelly float64 // fraction of full Kelly to bet (default 0.25 = quarter Kelly)
	MaxPosition     float64 // max cost per single market (default $10)
	MaxExposure     float64 // max total cost across all open positions (default $25)
	DrawdownLimit   float64 // halt trading at this % drawdown from peak (default 0.20)
	MinBet          float64 // minimum bet cost to avoid dust positions (default $0.50)
}

func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		Bankroll:        100.0,
		FractionalKelly: 0.25,
		MaxPosition:     10.0,
		MaxExposure:     25.0,
		DrawdownLimit:   0.20,
		MinBet:          0.50,
	}
}

// Position represents a paper trade in a single market.
type Position struct {
	Slug      string
	Direction signal.Direction
	TokenID   string
	Price     float64 // entry price per share
	Size      float64 // number of shares
	Cost      float64 // total cost (price * size)
	KellyFrac float64 // full Kelly fraction at entry
	ModelProb float64 // model probability at entry
	OpenedAt  time.Time
}

// Order is the output of risk evaluation -- a sized paper trade ready to open.
type Order struct {
	Market    string
	Direction signal.Direction
	TokenID   string
	Price     float64
	Size      float64
	Cost      float64
	KellyFrac float64
	ModelProb float64
}

func (o Order) String() string {
	return fmt.Sprintf("%s %s price=%.4f size=%.2f cost=$%.2f kelly=%.3f model_p=%.3f",
		o.Direction, o.Market, o.Price, o.Size, o.Cost, o.KellyFrac, o.ModelProb)
}

// Settlement records how a position was closed.
type Settlement struct {
	Slug      string
	Direction signal.Direction
	Won       bool
	PnL       float64
	Cost      float64
	SettledAt time.Time
}

// ResolutionChecker queries an external API for the outcome of a resolved market.
// Implemented by market.Discovery.
type ResolutionChecker interface {
	CheckResolution(ctx context.Context, startTS int64) (outcome string, resolved bool, err error)
}

// SettleFunc is called after each position settlement for external persistence.
type SettleFunc func(slug string, won bool, pnl float64, outcome string, bankrollAfter float64)

// Manager sizes positions, tracks paper P&L, and enforces risk limits.
type Manager struct {
	mu sync.Mutex

	cfg          ManagerConfig
	bankroll     float64
	peakBankroll float64
	positions    map[string]*Position
	settlements  []Settlement

	totalPnL float64
	wins     int
	losses   int
	halted   bool

	OnSettle SettleFunc // optional callback for settlement persistence
	logger   *slog.Logger
}

func NewManager(logger *slog.Logger, cfg ManagerConfig) *Manager {
	return &Manager{
		cfg:          cfg,
		bankroll:     cfg.Bankroll,
		peakBankroll: cfg.Bankroll,
		positions:    make(map[string]*Position),
		logger:       logger,
	}
}

// Evaluate takes a signal decision, looks up the orderbook for the target
// token, computes Kelly sizing, enforces limits, and returns an Order
// (or nil if the trade is rejected).
func (m *Manager) Evaluate(dec signal.Decision, h *hub.Hub) *Order {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.halted {
		return nil
	}

	if !dec.ShouldTrade {
		return nil
	}

	if _, exists := m.positions[dec.Market]; exists {
		return nil
	}

	ms := h.GetMarketBySlug(dec.Market)
	if ms == nil {
		return nil
	}

	var tokenID string
	var modelProb float64

	switch dec.Dir {
	case signal.BuyUp:
		tokenID = ms.UpTokenID
		modelProb = dec.ModelProb
	case signal.BuyDown:
		tokenID = ms.DownTokenID
		modelProb = 1 - dec.ModelProb
	default:
		return nil
	}

	ob := h.GetOrderbook(tokenID)
	if ob == nil {
		return nil
	}
	entryPrice, ok := ob.BestAsk()
	if !ok || entryPrice <= 0 || entryPrice >= 1 {
		return nil
	}

	kellyFrac := KellyFraction(modelProb, entryPrice)
	if kellyFrac <= 0 {
		return nil
	}

	betCost := kellyFrac * m.cfg.FractionalKelly * m.bankroll

	if betCost > m.cfg.MaxPosition {
		betCost = m.cfg.MaxPosition
	}

	currentExp := m.currentExposure()
	if currentExp+betCost > m.cfg.MaxExposure {
		remaining := m.cfg.MaxExposure - currentExp
		if remaining < m.cfg.MinBet {
			return nil
		}
		betCost = remaining
	}

	if betCost < m.cfg.MinBet {
		return nil
	}

	shares := betCost / entryPrice

	return &Order{
		Market:    dec.Market,
		Direction: dec.Dir,
		TokenID:   tokenID,
		Price:     entryPrice,
		Size:      shares,
		Cost:      betCost,
		KellyFrac: kellyFrac,
		ModelProb: modelProb,
	}
}

// OpenPosition records a paper trade.
func (m *Manager) OpenPosition(o *Order) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.positions[o.Market] = &Position{
		Slug:      o.Market,
		Direction: o.Direction,
		TokenID:   o.TokenID,
		Price:     o.Price,
		Size:      o.Size,
		Cost:      o.Cost,
		KellyFrac: o.KellyFrac,
		ModelProb: o.ModelProb,
		OpenedAt:  time.Now(),
	}
}

// SettleResolved checks all open positions against the hub for resolved
// markets and records P&L for each.
func (m *Manager) SettleResolved(h *hub.Hub) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for slug, pos := range m.positions {
		ms := h.GetMarketBySlug(slug)
		if ms == nil || ms.Status != hub.MarketResolved {
			continue
		}

		won := m.didWin(pos.Direction, ms.WinningOutcome)

		var pnl float64
		if won {
			pnl = (1.0 - pos.Price) * pos.Size
			m.wins++
		} else {
			pnl = -pos.Cost
			m.losses++
		}

		m.bankroll += pnl
		m.totalPnL += pnl
		if m.bankroll > m.peakBankroll {
			m.peakBankroll = m.bankroll
		}

		s := Settlement{
			Slug:      slug,
			Direction: pos.Direction,
			Won:       won,
			PnL:       pnl,
			Cost:      pos.Cost,
			SettledAt: time.Now(),
		}
		m.settlements = append(m.settlements, s)
		delete(m.positions, slug)

		m.logger.Info("position_settled",
			"slug", slug,
			"direction", pos.Direction,
			"entry", pos.Price,
			"shares", pos.Size,
			"won", won,
			"pnl", fmt.Sprintf("$%.2f", pnl),
			"bankroll", fmt.Sprintf("$%.2f", m.bankroll),
			"record", fmt.Sprintf("%dW/%dL", m.wins, m.losses),
		)

		if m.OnSettle != nil {
			m.OnSettle(slug, won, pnl, ms.WinningOutcome, m.bankroll)
		}

		drawdown := (m.peakBankroll - m.bankroll) / m.peakBankroll
		if drawdown >= m.cfg.DrawdownLimit {
			m.halted = true
			m.logger.Warn("drawdown breaker triggered",
				"drawdown", fmt.Sprintf("%.1f%%", drawdown*100),
				"bankroll", fmt.Sprintf("$%.2f", m.bankroll),
				"peak", fmt.Sprintf("$%.2f", m.peakBankroll),
			)
		}
	}
}

func (m *Manager) didWin(dir signal.Direction, outcome string) bool {
	o := strings.ToLower(outcome)
	switch dir {
	case signal.BuyUp:
		return o == "up" || o == "yes"
	case signal.BuyDown:
		return o == "down" || o == "no"
	}
	return false
}

func (m *Manager) currentExposure() float64 {
	var total float64
	for _, p := range m.positions {
		total += p.Cost
	}
	return total
}

// IsHalted returns true if the drawdown breaker has been triggered.
func (m *Manager) IsHalted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.halted
}

// Status returns a summary string for logging.
type Status struct {
	Bankroll     float64
	TotalPnL     float64
	OpenPositions int
	Exposure     float64
	Wins         int
	Losses       int
	WinRate      float64
	Halted       bool
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	total := m.wins + m.losses
	var winRate float64
	if total > 0 {
		winRate = float64(m.wins) / float64(total)
	}

	return Status{
		Bankroll:      m.bankroll,
		TotalPnL:      m.totalPnL,
		OpenPositions: len(m.positions),
		Exposure:      m.currentExposure(),
		Wins:          m.wins,
		Losses:        m.losses,
		WinRate:       winRate,
		Halted:        m.halted,
	}
}

func (s Status) String() string {
	return fmt.Sprintf(
		"bankroll=$%.2f pnl=$%.2f open=%d exposure=$%.2f record=%dW/%dL wr=%.0f%% halted=%v",
		s.Bankroll, s.TotalPnL, s.OpenPositions, s.Exposure,
		s.Wins, s.Losses, s.WinRate*100, s.Halted,
	)
}

// SettleExpired queries the Gamma API for positions whose market window
// has passed but no WebSocket resolution event was received.
func (m *Manager) SettleExpired(ctx context.Context, h *hub.Hub, rc ResolutionChecker) {
	m.mu.Lock()
	var expired []string
	for slug, pos := range m.positions {
		ms := h.GetMarketBySlug(slug)
		if ms == nil {
			continue
		}
		if ms.Status == hub.MarketResolved {
			continue
		}
		if time.Since(ms.EndTime) > 30*time.Second {
			expired = append(expired, pos.Slug)
		}
	}
	m.mu.Unlock()

	for _, slug := range expired {
		ms := h.GetMarketBySlug(slug)
		if ms == nil {
			continue
		}

		outcome, resolved, err := rc.CheckResolution(ctx, ms.StartTime.Unix())
		if err != nil {
			m.logger.Debug("resolution check failed", "slug", slug, "err", err)
			continue
		}
		if !resolved {
			continue
		}

		h.ForceResolve(slug, outcome)
		m.logger.Info("resolved via API", "slug", slug, "outcome", outcome)
	}
}
