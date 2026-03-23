package hub

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	DefaultPriceBufCap = 600 // ~10 min at 1 update/sec
	DefaultTradeBufCap = 200
)

// Hub is the central state store. Stream goroutines write to it via the
// EventHandler methods; signal models read from it via query methods.
// All access is protected by a sync.RWMutex.
type Hub struct {
	mu         sync.RWMutex
	orderbooks map[string]*Orderbook
	priceBuf   *PriceBuffer
	markets    map[string]*MarketState
	trades     map[string]*TradeBuffer
	logger     *slog.Logger
}

func New(logger *slog.Logger) *Hub {
	return &Hub{
		orderbooks: make(map[string]*Orderbook),
		priceBuf:   NewPriceBuffer(DefaultPriceBufCap),
		markets:    make(map[string]*MarketState),
		trades:     make(map[string]*TradeBuffer),
		logger:     logger,
	}
}

// ---------------------------------------------------------------------------
// EventHandler implementation (write path -- acquires write lock)
// ---------------------------------------------------------------------------

func (h *Hub) OnOrderbook(assetID string, bids, asks []OrderLevel, ts int64) {
	h.mu.Lock()
	ob, ok := h.orderbooks[assetID]
	if !ok {
		ob = NewOrderbook()
		h.orderbooks[assetID] = ob
	}
	ob.Update(bids, asks, ts)
	h.mu.Unlock()
}

func (h *Hub) OnTrade(assetID string, price, size float64, side string, ts int64) {
	h.mu.Lock()
	tb, ok := h.trades[assetID]
	if !ok {
		tb = NewTradeBuffer(DefaultTradeBufCap)
		h.trades[assetID] = tb
	}
	tb.Add(TradeEntry{Price: price, Size: size, Side: side, Timestamp: ts})
	h.mu.Unlock()
}

func (h *Hub) OnBTCPrice(price float64, ts int64) {
	h.mu.Lock()
	h.priceBuf.Add(price, ts)
	h.mu.Unlock()
}

func (h *Hub) OnBestBidAsk(assetID string, bestBid, bestAsk, spread float64, ts int64) {
	h.logger.Debug("best_bid_ask",
		"asset", truncID(assetID),
		"bid", bestBid,
		"ask", bestAsk,
		"spread", spread,
	)
}

func (h *Hub) OnMarketResolved(id, slug, winningOutcome, winningAssetID string, ts int64) {
	h.mu.Lock()
	ms, ok := h.markets[slug]
	if !ok && winningAssetID != "" {
		for _, m := range h.markets {
			if m.UpTokenID == winningAssetID || m.DownTokenID == winningAssetID {
				ms = m
				ok = true
				break
			}
		}
	}
	if ok && ms != nil {
		ms.Status = MarketResolved
		ms.WinningOutcome = winningOutcome
		ms.ResolvedAt = ts
	}
	h.mu.Unlock()
	h.logger.Info("market_resolved",
		"slug", slug,
		"outcome", winningOutcome,
		"winning_asset", truncID(winningAssetID),
		"matched", ok,
	)
}

func (h *Hub) OnNewMarket(id, slug, question string, assetIDs []string, ts int64) {
	h.logger.Info("new_market_event",
		"id", id,
		"slug", slug,
		"question", question,
		"assets", len(assetIDs),
	)
}

// ---------------------------------------------------------------------------
// Query methods (read path -- acquires read lock)
// ---------------------------------------------------------------------------

func (h *Hub) GetOrderbook(assetID string) *Orderbook {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.orderbooks[assetID]
}

func (h *Hub) BTCPrice() (float64, int64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.priceBuf.Latest()
}

func (h *Hub) BTCPriceCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.priceBuf.Len()
}

func (h *Hub) BTCPriceSMA(window int) (float64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.priceBuf.SMA(window)
}

func (h *Hub) BTCPriceSlope(window int) (float64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.priceBuf.Slope(window)
}

func (h *Hub) TradeVWAP(assetID string, window time.Duration) (float64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	tb, ok := h.trades[assetID]
	if !ok {
		return 0, false
	}
	return tb.VWAP(window)
}

func (h *Hub) TradeVelocity(assetID string, window time.Duration) float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	tb, ok := h.trades[assetID]
	if !ok {
		return 0
	}
	return tb.Velocity(window)
}

func (h *Hub) ImpliedProbability(upTokenID string) (float64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ob, ok := h.orderbooks[upTokenID]
	if !ok {
		return 0, false
	}
	return ob.MidPrice()
}

// ---------------------------------------------------------------------------
// Market state management
// ---------------------------------------------------------------------------

func (h *Hub) RegisterMarket(ms MarketState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.markets[ms.Slug]; !exists {
		h.markets[ms.Slug] = &ms
		h.logger.Debug("market_registered", "slug", ms.Slug)
	}
}

func (h *Hub) CurrentMarket() *MarketState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	now := time.Now()
	for _, ms := range h.markets {
		if ms.Status == MarketTrading && !now.Before(ms.StartTime) && now.Before(ms.EndTime) {
			return ms
		}
	}
	return nil
}

func (h *Hub) GetMarketBySlug(slug string) *MarketState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.markets[slug]
}

// ForceResolve marks a market as resolved when the outcome is determined
// via the Gamma API (fallback for missing WebSocket resolution events).
func (h *Hub) ForceResolve(slug, outcome string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ms, ok := h.markets[slug]; ok && ms.Status != MarketResolved {
		ms.Status = MarketResolved
		ms.WinningOutcome = outcome
		ms.ResolvedAt = time.Now().Unix()
	}
}

func (h *Hub) KnownSlugs() map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	slugs := make(map[string]bool, len(h.markets))
	for s := range h.markets {
		slugs[s] = true
	}
	return slugs
}

// ---------------------------------------------------------------------------
// Snapshot for periodic status logging
// ---------------------------------------------------------------------------

type Snapshot struct {
	BTCPrice     float64
	BTCTrend     string // "rising", "falling", "flat"
	CurrentSlug  string
	TimeToExpiry time.Duration
	UpBid        float64
	UpAsk        float64
	UpSpread     float64
	DownBid      float64
	DownAsk      float64
	DownSpread   float64
	TradeCount   int
	PriceBufLen  int
}

func (h *Hub) TakeSnapshot() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()

	snap := Snapshot{PriceBufLen: h.priceBuf.Len()}

	if p, _, ok := h.priceBuf.Latest(); ok {
		snap.BTCPrice = p
	}

	if slope, ok := h.priceBuf.Slope(60); ok {
		switch {
		case slope > 0.5:
			snap.BTCTrend = "rising"
		case slope < -0.5:
			snap.BTCTrend = "falling"
		default:
			snap.BTCTrend = "flat"
		}
	} else {
		snap.BTCTrend = "n/a"
	}

	now := time.Now()
	for _, ms := range h.markets {
		if ms.Status == MarketTrading && !now.Before(ms.StartTime) && now.Before(ms.EndTime) {
			snap.CurrentSlug = ms.Slug
			snap.TimeToExpiry = time.Until(ms.EndTime)

			if ob := h.orderbooks[ms.UpTokenID]; ob != nil {
				if b, ok := ob.BestBid(); ok {
					snap.UpBid = b
				}
				if a, ok := ob.BestAsk(); ok {
					snap.UpAsk = a
				}
				if s, ok := ob.Spread(); ok {
					snap.UpSpread = s
				}
			}
			if ob := h.orderbooks[ms.DownTokenID]; ob != nil {
				if b, ok := ob.BestBid(); ok {
					snap.DownBid = b
				}
				if a, ok := ob.BestAsk(); ok {
					snap.DownAsk = a
				}
				if s, ok := ob.Spread(); ok {
					snap.DownSpread = s
				}
			}

			since := now.Unix() - 60
			if tb := h.trades[ms.UpTokenID]; tb != nil {
				snap.TradeCount += len(tb.RecentSince(since))
			}
			if tb := h.trades[ms.DownTokenID]; tb != nil {
				snap.TradeCount += len(tb.RecentSince(since))
			}
			break
		}
	}

	return snap
}

func (s Snapshot) String() string {
	if s.CurrentSlug == "" {
		return fmt.Sprintf("btc=$%.2f trend=%s buf=%d (no active market)", s.BTCPrice, s.BTCTrend, s.PriceBufLen)
	}
	return fmt.Sprintf(
		"btc=$%.2f trend=%s market=%s expiry=%s up=[%.4f/%.4f spread=%.4f] down=[%.4f/%.4f spread=%.4f] trades_60s=%d buf=%d",
		s.BTCPrice, s.BTCTrend, s.CurrentSlug,
		s.TimeToExpiry.Truncate(time.Second),
		s.UpBid, s.UpAsk, s.UpSpread,
		s.DownBid, s.DownAsk, s.DownSpread,
		s.TradeCount, s.PriceBufLen,
	)
}

func truncID(id string) string {
	if len(id) > 16 {
		return id[:8] + "..." + id[len(id)-8:]
	}
	return id
}
