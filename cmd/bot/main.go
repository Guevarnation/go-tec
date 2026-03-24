package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	ossignal "os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/log"

	"go-trading/internal/api"
	"go-trading/internal/config"
	"go-trading/internal/hub"
	"go-trading/internal/market"
	"go-trading/internal/notify"
	"go-trading/internal/risk"
	"go-trading/internal/signal"
	"go-trading/internal/stats"
	"go-trading/internal/store"
	"go-trading/internal/stream"
)

// BotVersion tracks the iteration of the trading model for comparing
// performance across deployments. Bump this on each significant change.
const BotVersion = "v5"

func newLogger(cfg *config.Config) *slog.Logger {
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: cfg.LogLevel,
		}))
	}

	charm := log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		ReportCaller:   false,
	})

	switch cfg.LogLevel {
	case slog.LevelDebug:
		charm.SetLevel(log.DebugLevel)
	case slog.LevelWarn:
		charm.SetLevel(log.WarnLevel)
	case slog.LevelError:
		charm.SetLevel(log.ErrorLevel)
	default:
		charm.SetLevel(log.InfoLevel)
	}

	return slog.New(charm)
}

func main() {
	cfg := config.Load()
	logger := newLogger(cfg)
	slog.SetDefault(logger)

	ctx, cancel := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("starting polymarket btc 5m bot",
		"version", BotVersion,
		"log_level", cfg.LogLevel.String(),
		"log_format", cfg.LogFormat,
		"gamma_url", cfg.GammaAPIURL,
		"slug_prefix", cfg.MarketSlugPrefix,
		"data_dir", cfg.DataDir,
	)

	// --- SQLite trade log ---
	db, err := store.Open(cfg.DataDir)
	if err != nil {
		logger.Error("failed to open trade database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("trade database ready", "dir", cfg.DataDir)

	// --- Central state store ---
	h := hub.New(logger.With("component", "hub"))

	// --- Discover active BTC 5-min markets ---
	disc := market.NewDiscovery(cfg.GammaAPIURL, cfg.MarketSlugPrefix, logger)
	markets, err := disc.FindCurrentAndUpcoming(ctx, 3)
	if err != nil {
		logger.Error("market discovery failed", "err", err)
		os.Exit(1)
	}

	allIDs := market.AllAssetIDs(markets)
	if len(allIDs) == 0 {
		logger.Error("no BTC 5m markets found -- market may be inactive")
		os.Exit(1)
	}

	registerMarkets(h, markets)
	logger.Info("asset IDs ready", "total", len(allIDs), "open", len(market.OpenAssetIDs(markets)))

	// --- CLOB WebSocket streams ---
	clobStream, err := stream.NewCLOBStream(h, logger.With("component", "clob_ws"))
	if err != nil {
		logger.Error("clob ws connect failed", "err", err)
		os.Exit(1)
	}
	defer clobStream.Close()

	if err := clobStream.SubscribeAssets(ctx, allIDs); err != nil {
		logger.Error("clob subscription failed", "err", err)
	}

	// --- RTDS -- Live BTC/USD price ---
	rtdsStream, err := stream.NewRTDSStream(h, logger.With("component", "rtds"))
	if err != nil {
		logger.Error("rtds connect failed", "err", err)
		os.Exit(1)
	}
	defer rtdsStream.Close()

	if err := rtdsStream.StreamBTCPrice(ctx); err != nil {
		logger.Error("btc price subscription failed", "err", err)
	}

	logger.Info("all streams active -- hub collecting data")

	// --- Signal engine ---
	eng := signal.NewEngine(logger.With("component", "signal"), signal.DefaultEngineConfig())

	// --- Risk manager (paper trading) ---
	rm := risk.NewManager(logger.With("component", "risk"), risk.DefaultManagerConfig())

	// Restore bankroll from last settled trade if available
	if lastBankroll, ok := db.LastBankroll(); ok {
		rm.RestoreBankroll(lastBankroll)
	}

	rm.OnSettle = func(slug string, won bool, pnl float64, outcome string, bankrollAfter float64) {
		if err := db.SettleTrade(slug, won, pnl, outcome, bankrollAfter); err != nil {
			logger.Warn("failed to persist settlement", "slug", slug, "err", err)
		}
	}

	logger.Info("risk manager ready",
		"bankroll", rm.Status().Bankroll,
		"fractional_kelly", risk.DefaultManagerConfig().FractionalKelly,
		"max_position", risk.DefaultManagerConfig().MaxPosition,
		"drawdown_limit", risk.DefaultManagerConfig().DrawdownLimit,
	)

	// --- SNS alerter (optional) ---
	alerter := notify.New(cfg.SNSTopicARN, logger.With("component", "notify"))
	if alerter.Enabled() {
		logger.Info("sns alerting enabled", "topic", cfg.SNSTopicARN)
		alerter.Alert("Bot started", "Polymarket BTC 5m paper trading bot is online.")
	}

	rm.OnHalt = func(drawdown, bankroll, peak float64) {
		alerter.Alert("Drawdown breaker triggered",
			fmt.Sprintf("Trading halted. Drawdown=%.1f%% Bankroll=$%.2f Peak=$%.2f",
				drawdown*100, bankroll, peak))
	}

	// --- HTTP status API (optional) ---
	if cfg.APIPort != "" {
		apiSrv := api.NewServer(h, rm, db, logger.With("component", "api"))
		httpSrv := &http.Server{
			Addr:    ":" + cfg.APIPort,
			Handler: apiSrv.Handler(),
		}
		go func() {
			logger.Info("api server starting", "port", cfg.APIPort)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("api server failed", "err", err)
			}
		}()
		go func() {
			<-ctx.Done()
			httpSrv.Close()
		}()
	}

	// --- Market rotation + expired settlement every 60s ---
	go marketRotation(ctx, disc, h, clobStream, rm, logger)

	// --- Periodic snapshot log every 30s ---
	go snapshotLoop(ctx, h, rm, db, logger)

	// --- Signal + risk evaluation every 5s ---
	go signalLoop(ctx, eng, rm, h, db, logger)

	// --- Hourly stats summary ---
	go hourlyStatsLoop(ctx, db, logger)

	<-ctx.Done()
	logger.Info("shutting down gracefully")
}

// registerMarkets seeds the hub with discovered markets.
func registerMarkets(h *hub.Hub, markets []market.ActiveMarket) {
	for _, m := range markets {
		h.RegisterMarket(hub.MarketState{
			ID:          m.ID,
			Slug:        m.Slug,
			Question:    m.Question,
			UpTokenID:   m.UpTokenID,
			DownTokenID: m.DownTokenID,
			StartTime:   m.StartTime,
			EndTime:     m.EndTime,
			Status:      hub.MarketTrading,
		})
	}
}

// marketRotation periodically discovers new BTC 5-min windows and subscribes
// to their asset IDs so the bot runs continuously beyond the initial 20 minutes.
func marketRotation(ctx context.Context, disc *market.Discovery, h *hub.Hub, cs *stream.CLOBStream, rm *risk.Manager, logger *slog.Logger) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			markets, err := disc.FindCurrentAndUpcoming(ctx, 3)
			if err != nil {
				logger.Warn("rotation discovery failed", "err", err)
				continue
			}

			known := h.KnownSlugs()
			var newIDs []string

			for _, m := range markets {
				if known[m.Slug] {
					continue
				}
				h.RegisterMarket(hub.MarketState{
					ID:          m.ID,
					Slug:        m.Slug,
					Question:    m.Question,
					UpTokenID:   m.UpTokenID,
					DownTokenID: m.DownTokenID,
					StartTime:   m.StartTime,
					EndTime:     m.EndTime,
					Status:      hub.MarketTrading,
				})
				newIDs = append(newIDs, m.UpTokenID, m.DownTokenID)
				logger.Info("new market window discovered",
					"slug", m.Slug,
					"start", m.StartTime.Format("15:04:05"),
					"end", m.EndTime.Format("15:04:05"),
				)
			}

			if len(newIDs) > 0 {
				if err := cs.SubscribeAssets(ctx, newIDs); err != nil {
					logger.Warn("failed to subscribe new assets", "err", err)
				} else {
					logger.Info("subscribed to new market assets", "count", len(newIDs))
				}
			}

			rm.SettleExpired(ctx, h, disc)
		}
	}
}

// snapshotLoop logs a summary of current hub state every 30 seconds
// and persists each snapshot to SQLite.
func snapshotLoop(ctx context.Context, h *hub.Hub, rm *risk.Manager, db *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := h.TakeSnapshot()
			st := rm.Status()

			logger.Info("snapshot", "state", snap.String(),
				"bankroll", st.Bankroll,
				"pnl", st.TotalPnL,
				"record", st.String(),
			)

			if err := db.LogSnapshot(store.SnapshotRecord{
				BTCPrice:      snap.BTCPrice,
				BTCTrend:      snap.BTCTrend,
				MarketSlug:    snap.CurrentSlug,
				ExpirySec:     int(snap.TimeToExpiry.Seconds()),
				UpBid:         snap.UpBid,
				UpAsk:         snap.UpAsk,
				UpSpread:      snap.UpSpread,
				DownBid:       snap.DownBid,
				DownAsk:       snap.DownAsk,
				DownSpread:    snap.DownSpread,
				TradeCount:    snap.TradeCount,
				PriceBufLen:   snap.PriceBufLen,
				Bankroll:      st.Bankroll,
				TotalPnL:      st.TotalPnL,
				OpenPositions: st.OpenPositions,
				Exposure:      st.Exposure,
				Wins:          st.Wins,
				Losses:        st.Losses,
			}); err != nil {
				logger.Warn("failed to persist snapshot", "err", err)
			}
		}
	}
}

// signalLoop evaluates trading signals every 5 seconds, passes tradeable
// decisions through the risk manager for Kelly sizing, and tracks paper P&L.
func signalLoop(ctx context.Context, eng *signal.Engine, rm *risk.Manager, h *hub.Hub, db *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rm.SettleResolved(h)

			dec := eng.Evaluate(h)
			if dec.Dir == signal.Hold {
				logger.Debug("signal", "decision", dec.String())
				continue
			}

			if !dec.ShouldTrade {
				logger.Debug("signal", "decision", dec.String())
				continue
			}

			order := rm.Evaluate(dec, h)
			if order != nil {
				rm.OpenPosition(order)
				logger.Info("paper_trade", "order", order.String())

				btcPrice, _, _ := h.BTCPrice()
				var momentum, imbalance, edgeSig, tradeflow float64
				for _, s := range dec.Signals {
					switch s.Name {
					case "momentum":
						momentum = s.Value
					case "imbalance":
						imbalance = s.Value
					case "edge":
						edgeSig = s.Value
					case "tradeflow":
						tradeflow = s.Value
					}
				}

				if err := db.LogTrade(store.TradeRecord{
					Slug:          order.Market,
					Direction:     order.Direction.String(),
					TokenID:       order.TokenID,
					EntryPrice:    order.Price,
					Shares:        order.Size,
					Cost:          order.Cost,
					KellyFrac:     order.KellyFrac,
					ModelProb:     order.ModelProb,
					Confidence:    dec.Confidence,
					Edge:          dec.Edge,
					Momentum:      momentum,
					Imbalance:     imbalance,
					EdgeSignal:    edgeSig,
					TradeFlow:     tradeflow,
					BTCPrice:      btcPrice,
					BTCVolatility: dec.VolatilityCV,
					OpenedAt:      time.Now(),
					BotVersion:    BotVersion,
				}); err != nil {
					logger.Warn("failed to persist trade", "err", err)
				}
			}

			st := rm.Status()
			logger.Debug("risk_status", "status", st.String())
		}
	}
}

// hourlyStatsLoop computes and logs aggregate performance metrics every hour.
func hourlyStatsLoop(ctx context.Context, db *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			allTrades, err := db.AllSettledTrades()
			if err != nil {
				logger.Warn("stats query failed", "err", err)
				continue
			}
			if len(allTrades) == 0 {
				logger.Info("hourly_stats", "status", "no settled trades yet")
				continue
			}

			hourAgo := time.Now().Add(-1 * time.Hour)
			var recentTrades []store.SettledTrade
			for _, t := range allTrades {
				if !t.SettledAt.Before(hourAgo) {
					recentTrades = append(recentTrades, t)
				}
			}

			allSummary := stats.ComputeSummary(allTrades, "all-time")
			logger.Info("stats_all_time", "summary", allSummary.String())

			if len(recentTrades) > 0 {
				hourSummary := stats.ComputeSummary(recentTrades, "last-hour")
				logger.Info("stats_last_hour", "summary", hourSummary.String())
			}

			cal := stats.ComputeCalibration(allTrades)
			if len(cal) > 0 {
				logger.Info("calibration", "buckets", stats.FormatCalibration(cal))
			}

			hourly := stats.ComputeHourlyPnL(allTrades)
			if len(hourly) > 0 {
				logger.Info("pnl_by_hour", "hours", stats.FormatHourly(hourly))
			}

			streaks := stats.ComputeStreaks(allTrades)
			logger.Info("streaks", "stats", streaks.String())

			edgeBuckets := stats.ComputeEdgeBuckets(allTrades)
			if len(edgeBuckets) > 0 {
				logger.Info("edge_analysis", "buckets", stats.FormatEdgeBuckets(edgeBuckets))
			}

			signalWR := stats.ComputeSignalWinRates(allTrades)
			if len(signalWR) > 0 {
				logger.Info("signal_analysis", "signals", stats.FormatSignalWinRates(signalWR))
			}

			tiw := stats.ComputeTimeInWindow(allTrades)
			if len(tiw) > 0 {
				logger.Info("time_in_window", "buckets", stats.FormatTimeInWindow(tiw))
			}
		}
	}
}
