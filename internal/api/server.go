package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"go-trading/internal/hub"
	"go-trading/internal/risk"
	"go-trading/internal/stats"
	"go-trading/internal/store"
)

// Server exposes bot status over HTTP.
// Bind to a port protected by EC2 security group (your IP only).
type Server struct {
	hub     *hub.Hub
	risk    *risk.Manager
	store   *store.Store
	startAt time.Time
	logger  *slog.Logger
}

func NewServer(h *hub.Hub, rm *risk.Manager, db *store.Store, logger *slog.Logger) *Server {
	return &Server{
		hub:     h,
		risk:    rm,
		store:   db,
		startAt: time.Now(),
		logger:  logger,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /trades", s.handleTrades)
	mux.HandleFunc("GET /stats", s.handleStats)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"ok":         true,
		"uptime_sec": int(time.Since(s.startAt).Seconds()),
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snap := s.hub.TakeSnapshot()
	st := s.risk.Status()

	resp := map[string]any{
		"btc_price":      snap.BTCPrice,
		"btc_trend":      snap.BTCTrend,
		"bankroll":       round2(st.Bankroll),
		"total_pnl":      round2(st.TotalPnL),
		"open_positions": st.OpenPositions,
		"exposure":       round2(st.Exposure),
		"wins":           st.Wins,
		"losses":         st.Losses,
		"win_rate":       round3(st.WinRate),
		"halted":         st.Halted,
	}

	if snap.CurrentSlug != "" {
		resp["current_market"] = map[string]any{
			"slug":        snap.CurrentSlug,
			"expiry_sec":  int(snap.TimeToExpiry.Seconds()),
			"up_bid":      snap.UpBid,
			"up_ask":      snap.UpAsk,
			"up_spread":   snap.UpSpread,
			"down_bid":    snap.DownBid,
			"down_ask":    snap.DownAsk,
			"down_spread": snap.DownSpread,
		}
	}

	writeJSON(w, resp)
}

func (s *Server) handleTrades(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	trades, err := s.store.RecentTrades(limit)
	if err != nil {
		s.logger.Warn("api trades query failed", "err", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"trades": trades,
		"count":  len(trades),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	allTrades, err := s.store.AllSettledTrades()
	if err != nil {
		s.logger.Warn("api stats query failed", "err", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}

	if len(allTrades) == 0 {
		writeJSON(w, map[string]any{"message": "no settled trades yet"})
		return
	}

	// All-time summary
	allSummary := stats.ComputeSummary(allTrades, "all-time")

	// Last-24h summary
	dayAgo := time.Now().Add(-24 * time.Hour)
	var recentTrades []store.SettledTrade
	for _, t := range allTrades {
		if !t.SettledAt.Before(dayAgo) {
			recentTrades = append(recentTrades, t)
		}
	}
	var recentSummary *stats.Summary
	if len(recentTrades) > 0 {
		s := stats.ComputeSummary(recentTrades, "last-24h")
		recentSummary = &s
	}

	writeJSON(w, map[string]any{
		"all_time":         allSummary,
		"last_24h":         recentSummary,
		"calibration":      stats.ComputeCalibration(allTrades),
		"streaks":          stats.ComputeStreaks(allTrades),
		"edge_buckets":     stats.ComputeEdgeBuckets(allTrades),
		"signal_win_rates": stats.ComputeSignalWinRates(allTrades),
		"hourly_pnl":       stats.ComputeHourlyPnL(allTrades),
		"time_in_window":   stats.ComputeTimeInWindow(allTrades),
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }
func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
