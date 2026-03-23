package hub

import "sort"

// Orderbook holds a snapshot of bids and asks for a single asset.
// Bids are sorted high-to-low (best bid first), asks low-to-high (best ask first).
type Orderbook struct {
	Bids      []OrderLevel
	Asks      []OrderLevel
	Timestamp int64
}

func NewOrderbook() *Orderbook {
	return &Orderbook{}
}

// Update replaces the entire book. The SDK sends full snapshots, not deltas.
func (ob *Orderbook) Update(bids, asks []OrderLevel, ts int64) {
	sort.Slice(bids, func(i, j int) bool { return bids[i].Price > bids[j].Price })
	sort.Slice(asks, func(i, j int) bool { return asks[i].Price < asks[j].Price })
	ob.Bids = bids
	ob.Asks = asks
	ob.Timestamp = ts
}

func (ob *Orderbook) BestBid() (float64, bool) {
	if len(ob.Bids) == 0 {
		return 0, false
	}
	return ob.Bids[0].Price, true
}

func (ob *Orderbook) BestAsk() (float64, bool) {
	if len(ob.Asks) == 0 {
		return 0, false
	}
	return ob.Asks[0].Price, true
}

func (ob *Orderbook) MidPrice() (float64, bool) {
	bid, hasBid := ob.BestBid()
	ask, hasAsk := ob.BestAsk()
	if !hasBid || !hasAsk {
		return 0, false
	}
	return (bid + ask) / 2, true
}

func (ob *Orderbook) Spread() (float64, bool) {
	bid, hasBid := ob.BestBid()
	ask, hasAsk := ob.BestAsk()
	if !hasBid || !hasAsk {
		return 0, false
	}
	return ask - bid, true
}

// DepthAtLevels returns total bid and ask volume across the top n levels.
func (ob *Orderbook) DepthAtLevels(n int) (bidDepth, askDepth float64) {
	for i := 0; i < n && i < len(ob.Bids); i++ {
		bidDepth += ob.Bids[i].Size
	}
	for i := 0; i < n && i < len(ob.Asks); i++ {
		askDepth += ob.Asks[i].Size
	}
	return
}

// BidAskImbalance returns a ratio from 0.0 to 1.0 across the top n levels.
// > 0.5 means more bid (buying) pressure, < 0.5 means more ask (selling) pressure.
func (ob *Orderbook) BidAskImbalance(n int) float64 {
	bidDepth, askDepth := ob.DepthAtLevels(n)
	total := bidDepth + askDepth
	if total == 0 {
		return 0.5
	}
	return bidDepth / total
}
