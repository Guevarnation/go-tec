package signal

import (
	"go-tec/internal/hub"
	"math"
)

// Momentum detects BTC price direction using linear regression slope
// over recent price ticks. A positive slope → BTC rising → Up is more likely.
type Momentum struct {
	Window      int     // price buffer entries to use (default 60 = ~1 min)
	Sensitivity float64 // slope scaling factor (default 2.0)
}

func NewMomentum() *Momentum {
	return &Momentum{
		Window:      60,
		Sensitivity: 2.0,
	}
}

func (m *Momentum) Name() string { return "momentum" }

func (m *Momentum) Evaluate(h *hub.Hub, _ *hub.MarketState) Score {
	slope, ok := h.BTCPriceSlope(m.Window)
	if !ok {
		return Score{Name: m.Name(), Value: 0}
	}

	// sigmoid maps slope to (-1, +1): tanh(slope * sensitivity)
	// slope in $/tick; with ~1 tick/sec, slope=1.0 means ~$60/min move
	value := math.Tanh(slope * m.Sensitivity)
	return Score{Name: m.Name(), Value: clamp(value, -1, 1)}
}
