package hub

// OrderLevel represents a single price level in an orderbook.
type OrderLevel struct {
	Price float64
	Size  float64
}

// EventHandler is the contract between the stream layer and the processing layer.
// The stream layer parses raw SDK/WS string values into typed Go values and calls
// these methods. The Hub implements this interface to maintain centralized state.
type EventHandler interface {
	OnOrderbook(assetID string, bids, asks []OrderLevel, ts int64)
	OnTrade(assetID string, price, size float64, side string, ts int64)
	OnBTCPrice(price float64, ts int64)
	OnBestBidAsk(assetID string, bestBid, bestAsk, spread float64, ts int64)
	OnMarketResolved(id, slug, winningOutcome, winningAssetID string, ts int64)
	OnNewMarket(id, slug, question string, assetIDs []string, ts int64)
}
