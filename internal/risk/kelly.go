package risk

// KellyFraction computes the optimal bet fraction for a binary outcome.
//
// In Polymarket 5-min markets, buying a share at price `cost` pays $1 if
// the outcome wins, $0 if it loses. The Kelly formula for this setup:
//
//	f* = (p - cost) / (1 - cost)
//
// where p = model-predicted probability of winning.
//
// Returns 0 if there's no edge (p <= cost) or inputs are invalid.
func KellyFraction(p, cost float64) float64 {
	if cost <= 0 || cost >= 1 || p <= 0 || p >= 1 {
		return 0
	}
	f := (p - cost) / (1 - cost)
	if f <= 0 {
		return 0
	}
	return f
}
