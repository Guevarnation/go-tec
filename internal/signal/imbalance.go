package signal

import "go-tec/internal/hub"

// Imbalance reads the Up token's orderbook and measures whether buying
// or selling pressure dominates. More bid depth on Up → market is bullish.
type Imbalance struct {
	Depth int // number of price levels to consider (default 5)
}

func NewImbalance() *Imbalance {
	return &Imbalance{Depth: 5}
}

func (ib *Imbalance) Name() string { return "imbalance" }

func (ib *Imbalance) Evaluate(h *hub.Hub, ms *hub.MarketState) Score {
	ob := h.GetOrderbook(ms.UpTokenID)
	if ob == nil {
		return Score{Name: ib.Name(), Value: 0}
	}

	// BidAskImbalance returns 0.0-1.0 where >0.5 = more bids = bullish
	raw := ob.BidAskImbalance(ib.Depth)

	// Map [0, 1] → [-1, +1]: strong bids = positive (Up), strong asks = negative (Down)
	value := (raw - 0.5) * 2
	return Score{Name: ib.Name(), Value: clamp(value, -1, 1)}
}
