package signal

import (
	"go-trading/internal/hub"
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
	// Compare buying activity on Up token vs Down token.
	// Previous approach (buy vs sell on Up token only) was always positive
	// because in binary markets, most fills on the Up token are buys.
	upBuy, upSell := h.TradeBuySellVolume(ms.UpTokenID, tf.Window)
	downBuy, downSell := h.TradeBuySellVolume(ms.DownTokenID, tf.Window)
	upTotal := upBuy + upSell
	downTotal := downBuy + downSell
	total := upTotal + downTotal
	if total == 0 {
		return Score{Name: tf.Name(), Value: 0}
	}
	// Data shows negative tradeflow (more Down volume) has 78% WR vs positive at 49%.
	// More volume on Down token → bullish (contrarian signal).
	ratio := upTotal / total
	value := -((ratio - 0.5) * 2)
	return Score{Name: tf.Name(), Value: clamp(value, -1, 1)}
}
