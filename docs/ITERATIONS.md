# Iteration Log

Tracks model versions, parameter changes, and performance across testing phases.

---

## v1 — Initial Deployment

**Deployed**: 2026-03-23 06:38 UTC
**Halted**: 2026-03-23 ~16:00 UTC (drawdown breaker)
**Duration**: ~10 hours active trading

### Parameters
| Parameter | Value |
|-----------|-------|
| Momentum weight | 0.40 |
| Imbalance weight | 0.15 |
| Edge weight | 0.25 |
| TradeFlow weight | 0.20 |
| MinConfidence | 0.15 |
| MinEdge | 0.03 |
| MinEntryPrice | none |
| FractionalKelly | 0.25 |
| MaxPosition | $10 |
| MaxExposure | $25 |
| DrawdownLimit | 20% |
| MaxVolatility (CV) | 0.3% |

### Results
| Metric | Value |
|--------|-------|
| Total trades | 44 |
| Wins / Losses | 23W / 21L |
| Win rate | 52.3% |
| Total PnL | -$48.89 |
| Profit factor | 0.72 |
| Brier score | 0.283 (near random) |
| Sharpe ratio | -49.6 |
| Max drawdown | 56.5% |
| Avg win | +$5.42 |
| Avg loss | -$8.26 |
| Final bankroll | $67.42 (halted at ~$83) |

### Signal Performance
| Signal | +WR | -WR | Separation |
|--------|-----|-----|------------|
| Momentum | 62% | 43% | **strong** |
| Edge | 58% | 45% | moderate |
| Imbalance | 50% | 55% | **inverted** |
| TradeFlow | 52% | n/a | no negative trades |

### Key Findings
1. **BUY_DOWN is bleeding**: 45% WR, -$55.92 PnL vs BUY_UP 58% WR, +$7.03
2. **BUY_DOWN underpriced tokens (< $0.50) = 0% WR**: 3 trades, all lost = -$30
3. **Losses 1.5-2x bigger than wins**: asymmetric risk from buying expensive asks
4. **Imbalance signal is inverted**: actively hurting, not helping
5. **TradeFlow never fires negative**: zero discrimination, not useful at current weight
6. **Hour 8 UTC best** (+$20.32, 67% WR), **Hours 9/15/16 worst**
7. **All trades enter early** (180-300s window), no mid/late diversification
8. **Calibration off**: 0.50-0.60 bucket predicted avg 0.24 but actual WR 0.44

---

## v2 — Post-Analysis Tuning

**Deployed**: 2026-03-23 18:01 UTC
**Status**: Running

### Changes from v1
| Parameter | v1 | v2 | Rationale |
|-----------|----|----|-----------|
| Momentum weight | 0.40 | **0.55** | Strongest signal (62% vs 43% separation) |
| Imbalance weight | 0.15 | **0.05** | Inverted signal, near-zero useful |
| Edge weight | 0.25 | **0.30** | Second-best signal (58% vs 45%) |
| TradeFlow weight | 0.20 | **0.10** | No negative trades, can't discriminate |
| MinConfidence | 0.15 | **0.20** | Filter marginal low-confidence trades |
| MinEdge | 0.03 | **0.05** | Filter tiny-edge trades that aren't profitable |
| MinEntryPrice | none | **0.45** | Reject cheap tokens (< $0.45 entry = 0% WR on BUY_DOWN) |
| DrawdownLimit | 20% | **15%** | Halt sooner, preserve bankroll for iteration |

### Hypothesis
- Heavier momentum weight should improve BUY_UP trades (already +PnL)
- MinEntryPrice 0.45 should eliminate the worst BUY_DOWN trades
- Tighter drawdown limit catches problems faster for quicker iteration
- Reducing imbalance/tradeflow noise should improve composite signal quality

### Results
_Pending — check back after 50+ trades_

| Metric | Value |
|--------|-------|
| Total trades | |
| Wins / Losses | |
| Win rate | |
| Total PnL | |
| Profit factor | |
| Brier score | |
| Max drawdown | |
| Final bankroll | |

---

## Metrics to Track Per Iteration

### Core Performance
- Total trades, Win/Loss count, Win rate
- Total PnL, Avg win, Avg loss
- Profit factor (gross_wins / gross_losses, need > 1.0)
- Sharpe ratio
- Max drawdown (% from peak)
- Final bankroll

### Model Quality
- Brier score (< 0.25 = better than random for 50/50 events)
- Calibration buckets (predicted vs actual by probability range)
- Edge bucket profitability (does higher computed edge = more profit?)

### Signal Quality
- Per-signal positive/negative win rates and separation
- Signal correlation with outcomes over time

### Directional
- BUY_UP vs BUY_DOWN win rate and PnL
- Entry price distribution and outcomes

### Temporal
- Hourly PnL (which UTC hours are profitable?)
- Time-in-window (early/mid/late entry performance)
- Duration active before halt

### Risk
- Drawdown trajectory (how fast does it bleed?)
- Position sizing distribution
- Streak analysis (max consecutive wins/losses)

---

## Future Tuning Ideas (Backlog)

- [ ] Hour-of-day filter: skip hours 9, 15, 16 UTC (worst in v1 data)
- [ ] Invert imbalance signal (if still inverted after more data)
- [ ] Add CLOB trade velocity signal (trades/min acceleration)
- [ ] Add spread-based confidence: wider spread = lower confidence
- [ ] Dynamic position sizing: reduce size when on a losing streak
- [ ] Add WebSocket reconnection for CLOB stream (currently only RTDS reconnects)
- [ ] Consider removing imbalance entirely if v2 confirms it's noise
