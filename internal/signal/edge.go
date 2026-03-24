package signal

import (
	"go-trading/internal/hub"
	"math"
)

// Edge detects mispricing between BTC momentum and the market's implied
// probability. If BTC is clearly rising but the Up token is still cheap,
// the market hasn't caught up -- that's the profit opportunity.
type Edge struct {
	SlopeWindow  int     // price entries for momentum (default 60)
	Sensitivity  float64 // sigmoid sensitivity (default 1.0, reduced from 2.0 to fix overconfidence)
	MaxModelProb float64 // cap model probability to prevent extreme confidence (default 0.80)
}

func NewEdge() *Edge {
	return &Edge{
		SlopeWindow:  60,
		Sensitivity:  1.0,
		MaxModelProb: 0.80,
	}
}

func (e *Edge) Name() string { return "edge" }

func (e *Edge) Evaluate(h *hub.Hub, ms *hub.MarketState) Score {
	slope, ok := h.BTCPriceSlope(e.SlopeWindow)
	if !ok {
		return Score{Name: e.Name(), Value: 0}
	}

	marketProb, ok := h.ImpliedProbability(ms.UpTokenID)
	if !ok {
		return Score{Name: e.Name(), Value: 0}
	}

	// Model-predicted probability that Up wins, based on BTC momentum
	modelProb := sigmoid(slope * e.Sensitivity)
	// Cap extreme confidence — calibration data shows 0.80-0.90 bucket wins 0%
	modelProb = clamp(modelProb, 1-e.MaxModelProb, e.MaxModelProb)

	// Edge = model - market. Positive → Up is underpriced. Negative → Down is underpriced.
	// Clamp to [-1, 1] though in practice it's bounded by [0,1] probabilities.
	edgeValue := modelProb - marketProb
	return Score{Name: e.Name(), Value: clamp(edgeValue, -1, 1)}
}

// ModelProbability exposes the sigmoid-mapped momentum probability
// for use by the engine when computing the raw edge magnitude.
func (e *Edge) ModelProbability(h *hub.Hub) (float64, bool) {
	slope, ok := h.BTCPriceSlope(e.SlopeWindow)
	if !ok {
		return 0, false
	}
	p := sigmoid(slope * e.Sensitivity)
	p = clamp(p, 1-e.MaxModelProb, e.MaxModelProb)
	return p, true
}

func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}
