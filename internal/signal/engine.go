package signal

import (
	"go-trading/internal/hub"
	"log/slog"
	"math"
	"time"
)

// EngineConfig holds tunable parameters for the signal engine.
type EngineConfig struct {
	MinDataPoints int           // minimum BTC price ticks before evaluating (default 30)
	MaxExpiry     time.Duration // don't trade if expiry > this (default 4m30s, i.e. wait 30s after open)
	MinExpiry     time.Duration // don't trade if expiry < this (default 30s, no fills possible)
	MinConfidence float64       // minimum composite confidence to trade (default 0.20)
	MinEdge       float64       // minimum model-vs-market edge to trade (default 0.05)
	MaxEdge       float64       // reject "too good to be true" edge — overconfident bets lose money (default 0.10)
	MaxVolatility float64       // max BTC price CV before suppressing trades (default 0.003 = 0.3%)
	VolWindow     int           // window for volatility calculation in ticks (default 60)
	SkipHoursUTC  map[int]bool  // UTC hours to skip trading (data shows consistent losses)
}

func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		MinDataPoints: 30,
		MaxExpiry:     4*time.Minute + 30*time.Second,
		MinExpiry:     30 * time.Second,
		MinConfidence: 0.20,
		MinEdge:       0.05,
		MaxEdge:       0.10,
		MaxVolatility: 0.003,
		VolWindow:     60,
		SkipHoursUTC: map[int]bool{
			15: true, 16: true, 17: true, 18: true, 19: true, 20: true, 21: true,
		},
	}
}

type weightedEval struct {
	eval   Evaluator
	weight float64
}

// Engine combines multiple signal evaluators into a composite trading decision.
type Engine struct {
	signals []weightedEval
	edge    *Edge
	cfg     EngineConfig
	logger  *slog.Logger
}

func NewEngine(logger *slog.Logger, cfg EngineConfig) *Engine {
	edge := NewEdge()
	return &Engine{
		signals: []weightedEval{
			{eval: NewMomentum(), weight: 0.55},
			{eval: NewImbalance(), weight: 0.05},
			{eval: edge, weight: 0.30},
			{eval: NewTradeFlow(), weight: 0.10},
		},
		edge:   edge,
		cfg:    cfg,
		logger: logger,
	}
}

// Evaluate produces a trading decision for the current market.
func (e *Engine) Evaluate(h *hub.Hub) Decision {
	now := time.Now()
	dec := Decision{EvaluatedAt: now}

	ms := h.CurrentMarket()
	if ms == nil {
		dec.Reason = "no active market"
		return dec
	}
	dec.Market = ms.Slug

	expiry := ms.TimeToExpiry()
	if expiry > e.cfg.MaxExpiry {
		dec.Reason = "too early in window"
		return dec
	}
	if expiry < e.cfg.MinExpiry {
		dec.Reason = "too close to expiry"
		return dec
	}

	_, _, priceOK := h.BTCPrice()
	if !priceOK {
		dec.Reason = "no BTC price data"
		return dec
	}

	if h.BTCPriceCount() < e.cfg.MinDataPoints {
		dec.Reason = "insufficient price data"
		return dec
	}

	// Hour-of-day gate: skip hours with consistently negative P&L
	if len(e.cfg.SkipHoursUTC) > 0 && e.cfg.SkipHoursUTC[now.UTC().Hour()] {
		dec.Reason = "skip hour (negative P&L history)"
		return dec
	}

	// Volatility gate: suppress in high-volatility regimes where
	// simple momentum signals become unreliable noise
	btcPrice, _, _ := h.BTCPrice()
	if stdDev, ok := h.BTCPriceStdDev(e.cfg.VolWindow); ok && btcPrice > 0 {
		dec.VolatilityCV = stdDev / btcPrice
		if dec.VolatilityCV > e.cfg.MaxVolatility {
			dec.Reason = "volatility too high"
			return dec
		}
	}

	// Compute individual signals
	var totalWeight float64
	var weightedSum float64

	for _, ws := range e.signals {
		score := ws.eval.Evaluate(h, ms)
		dec.Signals = append(dec.Signals, score)
		weightedSum += score.Value * ws.weight
		totalWeight += ws.weight
	}

	if totalWeight == 0 {
		dec.Reason = "no signal weights"
		return dec
	}

	composite := weightedSum / totalWeight
	dec.Confidence = math.Abs(composite)

	if composite > 0 {
		dec.Dir = BuyUp
	} else if composite < 0 {
		dec.Dir = BuyDown
	} else {
		dec.Dir = Hold
		dec.Reason = "signals cancel out"
		return dec
	}

	// Compute raw edge: model probability vs market implied probability
	modelProb, modelOK := e.edge.ModelProbability(h)
	marketProb, marketOK := h.ImpliedProbability(ms.UpTokenID)
	if modelOK {
		dec.ModelProb = modelProb
	}
	if modelOK && marketOK {
		dec.Edge = math.Abs(modelProb - marketProb)
	}

	// Gate: only recommend trading if confidence and edge are in acceptable range
	switch {
	case dec.Confidence < e.cfg.MinConfidence:
		dec.ShouldTrade = false
		dec.Reason = "confidence below threshold"
	case dec.Edge < e.cfg.MinEdge:
		dec.ShouldTrade = false
		dec.Reason = "edge below threshold"
	case e.cfg.MaxEdge > 0 && dec.Edge > e.cfg.MaxEdge:
		dec.ShouldTrade = false
		dec.Reason = "edge too high (overconfident)"
	default:
		dec.ShouldTrade = true
		dec.Reason = "signal confirmed"
	}

	return dec
}
