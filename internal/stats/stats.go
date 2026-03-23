package stats

import (
	"fmt"
	"math"
	"strings"
	"time"

	"go-tec/internal/store"
)

// Summary holds aggregate trading performance metrics.
type Summary struct {
	Period        string  `json:"period"`
	MarketsTraded int     `json:"markets_traded"`
	Wins          int     `json:"wins"`
	Losses        int     `json:"losses"`
	WinRate       float64 `json:"win_rate"`
	TotalPnL      float64 `json:"total_pnl"`
	GrossWins     float64 `json:"gross_wins"`
	GrossLosses   float64 `json:"gross_losses"`
	ProfitFactor  float64 `json:"profit_factor"`
	AvgEdge       float64 `json:"avg_edge"`
	AvgConfidence float64 `json:"avg_confidence"`
	AvgKelly      float64 `json:"avg_kelly"`
	MaxDrawdown   float64 `json:"max_drawdown"`
	SharpeRatio   float64 `json:"sharpe_ratio"`
	BrierScore    float64 `json:"brier_score"`
}

func (s Summary) String() string {
	return fmt.Sprintf(
		"[%s] trades=%d W=%d L=%d wr=%.1f%% pnl=$%.2f pf=%.2f sharpe=%.2f maxdd=%.2f%% avg_edge=%.3f avg_conf=%.3f",
		s.Period, s.MarketsTraded, s.Wins, s.Losses,
		s.WinRate*100, s.TotalPnL, s.ProfitFactor, s.SharpeRatio,
		s.MaxDrawdown*100, s.AvgEdge, s.AvgConfidence,
	)
}

// CalibrationBucket compares model-predicted probabilities to actual outcomes.
type CalibrationBucket struct {
	Label     string  `json:"label"`
	Predicted float64 `json:"predicted"`
	Actual    float64 `json:"actual"`
	Count     int     `json:"count"`
}

func (b CalibrationBucket) String() string {
	return fmt.Sprintf("%s pred=%.2f actual=%.2f n=%d gap=%.2f",
		b.Label, b.Predicted, b.Actual, b.Count, b.Predicted-b.Actual)
}

// HourBucket holds P&L for a specific hour of day.
type HourBucket struct {
	Hour    int     `json:"hour"`
	Trades  int     `json:"trades"`
	PnL     float64 `json:"pnl"`
	WinRate float64 `json:"win_rate"`
}

// ComputeSummary calculates aggregate metrics from settled trades.
func ComputeSummary(trades []store.SettledTrade, period string) Summary {
	s := Summary{Period: period, MarketsTraded: len(trades)}
	if len(trades) == 0 {
		return s
	}

	var sumEdge, sumConf float64
	var pnls []float64
	var cumPnL, peak, maxDD float64

	for _, t := range trades {
		if t.Won {
			s.Wins++
			s.GrossWins += t.PnL
		} else {
			s.Losses++
			s.GrossLosses += math.Abs(t.PnL)
		}
		s.TotalPnL += t.PnL
		sumEdge += t.Edge
		sumConf += t.Confidence
		pnls = append(pnls, t.PnL)

		cumPnL += t.PnL
		if cumPnL > peak {
			peak = cumPnL
		}
		dd := peak - cumPnL
		if dd > maxDD {
			maxDD = dd
		}
	}

	total := s.Wins + s.Losses
	if total > 0 {
		s.WinRate = float64(s.Wins) / float64(total)
	}
	if s.GrossLosses > 0 {
		s.ProfitFactor = s.GrossWins / s.GrossLosses
	}
	s.AvgEdge = sumEdge / float64(len(trades))
	s.AvgConfidence = sumConf / float64(len(trades))

	if peak > 0 {
		s.MaxDrawdown = maxDD / (100 + peak) // relative to starting + peak gains
	}

	s.SharpeRatio = sharpe(pnls)
	s.BrierScore = BrierScore(trades)

	return s
}

// ComputeCalibration groups trades by model probability and compares predicted vs actual win rate.
func ComputeCalibration(trades []store.SettledTrade) []CalibrationBucket {
	type bucket struct {
		sumProb float64
		wins    int
		total   int
	}

	buckets := make([]bucket, 5) // 0.50-0.60, 0.60-0.70, 0.70-0.80, 0.80-0.90, 0.90-1.00
	labels := []string{"0.50-0.60", "0.60-0.70", "0.70-0.80", "0.80-0.90", "0.90-1.00"}

	for _, t := range trades {
		// Normalize: for BUY_DOWN trades, model_prob is prob(Up), so
		// the relevant probability is 1-model_prob if direction is BUY_DOWN
		p := t.ModelProb
		if t.Direction == "BUY_DOWN" {
			p = 1 - p
		}

		idx := int((p - 0.50) / 0.10)
		if idx < 0 {
			idx = 0
		}
		if idx > 4 {
			idx = 4
		}
		buckets[idx].sumProb += p
		buckets[idx].total++
		if t.Won {
			buckets[idx].wins++
		}
	}

	var result []CalibrationBucket
	for i, b := range buckets {
		if b.total == 0 {
			continue
		}
		result = append(result, CalibrationBucket{
			Label:     labels[i],
			Predicted: b.sumProb / float64(b.total),
			Actual:    float64(b.wins) / float64(b.total),
			Count:     b.total,
		})
	}
	return result
}

// ComputeHourlyPnL groups trades by hour of day (UTC).
func ComputeHourlyPnL(trades []store.SettledTrade) []HourBucket {
	type acc struct {
		trades int
		pnl    float64
		wins   int
	}
	hours := make(map[int]*acc)

	for _, t := range trades {
		h := t.OpenedAt.UTC().Hour()
		a, ok := hours[h]
		if !ok {
			a = &acc{}
			hours[h] = a
		}
		a.trades++
		a.pnl += t.PnL
		if t.Won {
			a.wins++
		}
	}

	var result []HourBucket
	for h := 0; h < 24; h++ {
		a, ok := hours[h]
		if !ok {
			continue
		}
		wr := 0.0
		if a.trades > 0 {
			wr = float64(a.wins) / float64(a.trades)
		}
		result = append(result, HourBucket{Hour: h, Trades: a.trades, PnL: a.pnl, WinRate: wr})
	}
	return result
}

// FormatCalibration returns a multi-line string of calibration buckets.
func FormatCalibration(buckets []CalibrationBucket) string {
	if len(buckets) == 0 {
		return "no calibration data"
	}
	var parts []string
	for _, b := range buckets {
		parts = append(parts, b.String())
	}
	return strings.Join(parts, " | ")
}

// FormatHourly returns a compact summary of hourly P&L.
func FormatHourly(buckets []HourBucket) string {
	if len(buckets) == 0 {
		return "no hourly data"
	}
	var parts []string
	for _, b := range buckets {
		parts = append(parts, fmt.Sprintf("%02d:00=$%.2f(%d)", b.Hour, b.PnL, b.Trades))
	}
	return strings.Join(parts, " ")
}

// BrierScore computes the mean squared error between predicted probabilities
// and actual outcomes. Lower is better; 0.25 = random for 50/50 events.
func BrierScore(trades []store.SettledTrade) float64 {
	if len(trades) == 0 {
		return 0
	}
	var sum float64
	for _, t := range trades {
		p := t.ModelProb
		if t.Direction == "BUY_DOWN" {
			p = 1 - p
		}
		outcome := 0.0
		if t.Won {
			outcome = 1.0
		}
		diff := p - outcome
		sum += diff * diff
	}
	return sum / float64(len(trades))
}

// StreakStats tracks consecutive win/loss patterns.
type StreakStats struct {
	MaxWinStreak  int `json:"max_win_streak"`
	MaxLossStreak int `json:"max_loss_streak"`
	CurrentStreak int `json:"current_streak"`
}

func (s StreakStats) String() string {
	return fmt.Sprintf("max_win=%d max_loss=%d current=%d", s.MaxWinStreak, s.MaxLossStreak, s.CurrentStreak)
}

func ComputeStreaks(trades []store.SettledTrade) StreakStats {
	var s StreakStats
	var current int
	for _, t := range trades {
		if t.Won {
			if current > 0 {
				current++
			} else {
				current = 1
			}
			if current > s.MaxWinStreak {
				s.MaxWinStreak = current
			}
		} else {
			if current < 0 {
				current--
			} else {
				current = -1
			}
			if -current > s.MaxLossStreak {
				s.MaxLossStreak = -current
			}
		}
	}
	s.CurrentStreak = current
	return s
}

// EdgeBucket groups trades by predicted edge to verify higher edge = more profit.
type EdgeBucket struct {
	Label   string  `json:"label"`
	AvgEdge float64 `json:"avg_edge"`
	WinRate float64 `json:"win_rate"`
	AvgPnL  float64 `json:"avg_pnl"`
	Count   int     `json:"count"`
}

func ComputeEdgeBuckets(trades []store.SettledTrade) []EdgeBucket {
	type acc struct {
		sumEdge float64
		sumPnL  float64
		wins    int
		total   int
	}
	buckets := make([]acc, 4)
	labels := []string{"0.00-0.05", "0.05-0.10", "0.10-0.20", "0.20+"}

	for _, t := range trades {
		var idx int
		switch {
		case t.Edge < 0.05:
			idx = 0
		case t.Edge < 0.10:
			idx = 1
		case t.Edge < 0.20:
			idx = 2
		default:
			idx = 3
		}
		buckets[idx].sumEdge += t.Edge
		buckets[idx].sumPnL += t.PnL
		buckets[idx].total++
		if t.Won {
			buckets[idx].wins++
		}
	}

	var result []EdgeBucket
	for i, b := range buckets {
		if b.total == 0 {
			continue
		}
		result = append(result, EdgeBucket{
			Label:   labels[i],
			AvgEdge: b.sumEdge / float64(b.total),
			WinRate: float64(b.wins) / float64(b.total),
			AvgPnL:  b.sumPnL / float64(b.total),
			Count:   b.total,
		})
	}
	return result
}

func FormatEdgeBuckets(buckets []EdgeBucket) string {
	if len(buckets) == 0 {
		return "no edge data"
	}
	var parts []string
	for _, b := range buckets {
		parts = append(parts, fmt.Sprintf("%s wr=%.0f%% pnl=$%.2f n=%d",
			b.Label, b.WinRate*100, b.AvgPnL, b.Count))
	}
	return strings.Join(parts, " | ")
}

// SignalWinRate shows how each signal direction correlates with outcomes.
type SignalWinRate struct {
	Name     string  `json:"name"`
	Positive WinLoss `json:"positive"`
	Negative WinLoss `json:"negative"`
}

type WinLoss struct {
	Wins    int     `json:"wins"`
	Total   int     `json:"total"`
	WinRate float64 `json:"win_rate"`
}

func ComputeSignalWinRates(trades []store.SettledTrade) []SignalWinRate {
	names := []string{"momentum", "imbalance", "edge", "tradeflow"}
	results := make([]SignalWinRate, len(names))

	for i, name := range names {
		results[i].Name = name
		for _, t := range trades {
			var val float64
			switch name {
			case "momentum":
				val = t.Momentum
			case "imbalance":
				val = t.Imbalance
			case "edge":
				val = t.EdgeSignal
			case "tradeflow":
				val = t.TradeFlow
			}
			if val > 0 {
				results[i].Positive.Total++
				if t.Won {
					results[i].Positive.Wins++
				}
			} else if val < 0 {
				results[i].Negative.Total++
				if t.Won {
					results[i].Negative.Wins++
				}
			}
		}
		if results[i].Positive.Total > 0 {
			results[i].Positive.WinRate = float64(results[i].Positive.Wins) / float64(results[i].Positive.Total)
		}
		if results[i].Negative.Total > 0 {
			results[i].Negative.WinRate = float64(results[i].Negative.Wins) / float64(results[i].Negative.Total)
		}
	}
	return results
}

func FormatSignalWinRates(rates []SignalWinRate) string {
	var parts []string
	for _, r := range rates {
		parts = append(parts, fmt.Sprintf("%s[+wr=%.0f%%(%d) -wr=%.0f%%(%d)]",
			r.Name, r.Positive.WinRate*100, r.Positive.Total,
			r.Negative.WinRate*100, r.Negative.Total))
	}
	return strings.Join(parts, " ")
}

func sharpe(pnls []float64) float64 {
	n := len(pnls)
	if n < 2 {
		return 0
	}

	var sum float64
	for _, p := range pnls {
		sum += p
	}
	mean := sum / float64(n)

	var sumSq float64
	for _, p := range pnls {
		d := p - mean
		sumSq += d * d
	}
	stddev := math.Sqrt(sumSq / float64(n-1))

	if stddev == 0 {
		return 0
	}

	// Annualize: ~300 markets/day * 365 = ~109,500 trades/year
	tradesPerYear := 109500.0
	return (mean / stddev) * math.Sqrt(tradesPerYear)
}

// TimeWindowBucket groups trades by how much time remained in the 5-min window
// when the trade was opened. Helps identify if the bot performs better entering
// early vs late in a market window.
type TimeWindowBucket struct {
	Label   string  `json:"label"`
	MinSec  int     `json:"min_sec"`
	MaxSec  int     `json:"max_sec"`
	Trades  int     `json:"trades"`
	Wins    int     `json:"wins"`
	WinRate float64 `json:"win_rate"`
	AvgPnL  float64 `json:"avg_pnl"`
	TotalPnL float64 `json:"total_pnl"`
}

// ComputeTimeInWindow buckets trades by time-to-expiry at entry.
// Slug format: btc-updown-5m-{unix_timestamp}, window = 300s.
func ComputeTimeInWindow(trades []store.SettledTrade) []TimeWindowBucket {
	type acc struct {
		wins   int
		total  int
		sumPnL float64
	}

	// Buckets: early (180-300s), mid (90-180s), late (0-90s)
	buckets := []struct {
		label    string
		minSec   int
		maxSec   int
	}{
		{"late (0-90s)", 0, 90},
		{"mid (90-180s)", 90, 180},
		{"early (180-300s)", 180, 300},
	}
	accs := make([]acc, len(buckets))

	for _, t := range trades {
		ttx := timeToExpiry(t.Slug, t.OpenedAt)
		if ttx < 0 {
			continue // couldn't parse slug
		}

		for i, b := range buckets {
			if ttx >= b.minSec && ttx < b.maxSec {
				accs[i].total++
				accs[i].sumPnL += t.PnL
				if t.Won {
					accs[i].wins++
				}
				break
			}
		}
	}

	var result []TimeWindowBucket
	for i, b := range buckets {
		a := accs[i]
		if a.total == 0 {
			continue
		}
		wr := float64(a.wins) / float64(a.total)
		result = append(result, TimeWindowBucket{
			Label:    b.label,
			MinSec:   b.minSec,
			MaxSec:   b.maxSec,
			Trades:   a.total,
			Wins:     a.wins,
			WinRate:  wr,
			AvgPnL:   a.sumPnL / float64(a.total),
			TotalPnL: a.sumPnL,
		})
	}
	return result
}

// FormatTimeInWindow returns a compact summary of time-in-window analysis.
func FormatTimeInWindow(buckets []TimeWindowBucket) string {
	if len(buckets) == 0 {
		return "no time-in-window data"
	}
	var parts []string
	for _, b := range buckets {
		parts = append(parts, fmt.Sprintf("%s wr=%.0f%% pnl=$%.2f n=%d",
			b.Label, b.WinRate*100, b.AvgPnL, b.Trades))
	}
	return strings.Join(parts, " | ")
}

// timeToExpiry extracts seconds remaining in the 5-min window when a trade was opened.
// Returns -1 if the slug can't be parsed.
func timeToExpiry(slug string, openedAt time.Time) int {
	// Slug format: btc-updown-5m-{unix_timestamp}
	parts := strings.Split(slug, "-")
	if len(parts) < 4 {
		return -1
	}
	// The timestamp is the last part
	tsStr := parts[len(parts)-1]
	var ts int64
	for _, c := range tsStr {
		if c < '0' || c > '9' {
			return -1
		}
		ts = ts*10 + int64(c-'0')
	}
	if ts == 0 {
		return -1
	}

	endTime := time.Unix(ts+300, 0)
	remaining := int(endTime.Sub(openedAt).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 300 {
		remaining = 300
	}
	return remaining
}
