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
	Period        string
	MarketsTraded int
	Wins          int
	Losses        int
	WinRate       float64
	TotalPnL      float64
	GrossWins     float64
	GrossLosses   float64
	ProfitFactor  float64 // gross_wins / abs(gross_losses)
	AvgEdge       float64
	AvgConfidence float64
	AvgKelly      float64
	MaxDrawdown   float64
	SharpeRatio   float64
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
	Label     string  // e.g., "0.50-0.60"
	Predicted float64 // average model probability in this bucket
	Actual    float64 // actual win rate
	Count     int
}

func (b CalibrationBucket) String() string {
	return fmt.Sprintf("%s pred=%.2f actual=%.2f n=%d gap=%.2f",
		b.Label, b.Predicted, b.Actual, b.Count, b.Predicted-b.Actual)
}

// HourBucket holds P&L for a specific hour of day.
type HourBucket struct {
	Hour    int
	Trades  int
	PnL     float64
	WinRate float64
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
	_ = time.Now() // keep time import used
	return (mean / stddev) * math.Sqrt(tradesPerYear)
}
