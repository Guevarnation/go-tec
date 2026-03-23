package signal

import (
	"go-tec/internal/hub"
	"time"
)

// TradeFlow measures buying vs selling pressure on the Up token from
// recent trade executions. Aggressive buying on Up = bullish signal.
type TradeFlow struct {
	Window time.Duration // lookback window (default 60s)
}

func NewTradeFlow() *TradeFlow {
	return &TradeFlow{Window: 60 * time.Second}
}

func (tf *TradeFlow) Name() string { return "tradeflow" }

func (tf *TradeFlow) Evaluate(h *hub.Hub, ms *hub.MarketState) Score {
	buyVol, sellVol := h.TradeBuySellVolume(ms.UpTokenID, tf.Window)
	total := buyVol + sellVol
	if total == 0 {
		return Score{Name: tf.Name(), Value: 0}
	}
	// buyVol/total in [0,1]; map to [-1, +1]
	ratio := buyVol / total
	value := (ratio - 0.5) * 2
	return Score{Name: tf.Name(), Value: clamp(value, -1, 1)}
}
