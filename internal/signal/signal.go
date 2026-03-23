package signal

import (
	"fmt"
	"go-tec/internal/hub"
	"strings"
	"time"
)

// Direction represents the trading action.
type Direction int

const (
	Hold    Direction = iota
	BuyUp             // BTC will finish higher than open
	BuyDown           // BTC will finish lower than open
)

func (d Direction) String() string {
	switch d {
	case BuyUp:
		return "BUY_UP"
	case BuyDown:
		return "BUY_DOWN"
	default:
		return "HOLD"
	}
}

// Score is the output of a single signal evaluator.
// Range: -1.0 (strong Down) to +1.0 (strong Up). 0 = no signal.
type Score struct {
	Name  string
	Value float64 // -1.0 to +1.0
}

// Evaluator computes a single trading signal from the hub state.
type Evaluator interface {
	Name() string
	Evaluate(h *hub.Hub, ms *hub.MarketState) Score
}

// Decision is the composite output of the signal engine.
// The risk manager consumes this to determine position sizing.
type Decision struct {
	Market       string
	Dir          Direction
	Confidence   float64   // 0.0 to 1.0 (how certain)
	Edge         float64   // expected value edge abs(model_prob - market_prob)
	ModelProb    float64   // model-predicted probability of Up winning (0.0 to 1.0)
	VolatilityCV float64  // BTC price coefficient of variation at evaluation time
	Signals      []Score  // individual signal contributions
	ShouldTrade  bool
	Reason       string
	EvaluatedAt  time.Time
}

func (d Decision) String() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("dir=%s conf=%.3f edge=%.4f trade=%v",
		d.Dir, d.Confidence, d.Edge, d.ShouldTrade))
	if d.Reason != "" {
		parts = append(parts, fmt.Sprintf("reason=%q", d.Reason))
	}
	for _, s := range d.Signals {
		parts = append(parts, fmt.Sprintf("%s=%.3f", s.Name, s.Value))
	}
	return strings.Join(parts, " ")
}

// clamp restricts v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
