package stream

import (
	"context"
	"log/slog"
	"strconv"

	"go-trading/internal/hub"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

type CLOBStream struct {
	client  ws.Client
	handler hub.EventHandler
	logger  *slog.Logger
}

func NewCLOBStream(handler hub.EventHandler, logger *slog.Logger) (*CLOBStream, error) {
	client, err := ws.NewClient("", nil, nil)
	if err != nil {
		return nil, err
	}
	return &CLOBStream{client: client, handler: handler, logger: logger}, nil
}

func (s *CLOBStream) Close() error {
	return s.client.Close()
}

func (s *CLOBStream) StreamOrderbook(ctx context.Context, assetIDs []string) error {
	ch, err := s.client.SubscribeOrderbook(ctx, assetIDs)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					s.logger.Warn("orderbook channel closed")
					return
				}
				bids := make([]hub.OrderLevel, len(evt.Bids))
				for i, b := range evt.Bids {
					bids[i] = hub.OrderLevel{Price: parseFloat(b.Price), Size: parseFloat(b.Size)}
				}
				asks := make([]hub.OrderLevel, len(evt.Asks))
				for i, a := range evt.Asks {
					asks[i] = hub.OrderLevel{Price: parseFloat(a.Price), Size: parseFloat(a.Size)}
				}
				s.handler.OnOrderbook(evt.AssetID, bids, asks, parseTS(evt.Timestamp))
				s.logger.Debug("orderbook",
					"asset_id", truncID(evt.AssetID),
					"bids", len(evt.Bids),
					"asks", len(evt.Asks),
				)
			}
		}
	}()

	return nil
}

func (s *CLOBStream) StreamLastTradePrices(ctx context.Context, assetIDs []string) error {
	ch, err := s.client.SubscribeLastTradePrices(ctx, assetIDs)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					s.logger.Warn("last_trade_price channel closed")
					return
				}
				s.handler.OnTrade(
					evt.AssetID,
					parseFloat(evt.Price),
					parseFloat(evt.Size),
					evt.Side,
					parseTS(evt.Timestamp),
				)
				s.logger.Debug("trade",
					"asset_id", truncID(evt.AssetID),
					"price", evt.Price,
					"side", evt.Side,
				)
			}
		}
	}()

	return nil
}

func (s *CLOBStream) StreamBestBidAsk(ctx context.Context, assetIDs []string) error {
	ch, err := s.client.SubscribeBestBidAsk(ctx, assetIDs)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					s.logger.Warn("best_bid_ask channel closed")
					return
				}
				s.handler.OnBestBidAsk(
					evt.AssetID,
					parseFloat(evt.BestBid),
					parseFloat(evt.BestAsk),
					parseFloat(evt.Spread),
					parseTS(evt.Timestamp),
				)
			}
		}
	}()

	return nil
}

func (s *CLOBStream) StreamNewMarkets(ctx context.Context, assetIDs []string) error {
	ch, err := s.client.SubscribeNewMarkets(ctx, assetIDs)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					s.logger.Warn("new_market channel closed")
					return
				}
				s.handler.OnNewMarket(
					evt.ID, evt.Slug, evt.Question,
					evt.AssetIDs,
					parseTS(evt.Timestamp),
				)
			}
		}
	}()

	return nil
}

func (s *CLOBStream) StreamMarketResolutions(ctx context.Context, assetIDs []string) error {
	ch, err := s.client.SubscribeMarketResolutions(ctx, assetIDs)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					s.logger.Warn("market_resolved channel closed")
					return
				}
				s.handler.OnMarketResolved(
					evt.ID, evt.Slug,
					evt.WinningOutcome, evt.WinningAssetID,
					parseTS(evt.Timestamp),
				)
			}
		}
	}()

	return nil
}

// SubscribeAssets sets up all market data subscriptions for a set of asset IDs.
// Safe to call multiple times for new markets (market rotation).
func (s *CLOBStream) SubscribeAssets(ctx context.Context, assetIDs []string) error {
	if err := s.StreamOrderbook(ctx, assetIDs); err != nil {
		return err
	}
	if err := s.StreamLastTradePrices(ctx, assetIDs); err != nil {
		return err
	}
	if err := s.StreamBestBidAsk(ctx, assetIDs); err != nil {
		return err
	}
	if err := s.StreamMarketResolutions(ctx, assetIDs); err != nil {
		return err
	}
	if err := s.StreamNewMarkets(ctx, assetIDs); err != nil {
		return err
	}
	return nil
}

func truncID(id string) string {
	if len(id) > 16 {
		return id[:8] + "..." + id[len(id)-8:]
	}
	return id
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseTS(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
