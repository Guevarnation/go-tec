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

### Results (after 13 trades, halted at ~27% drawdown)

| Metric | Value |
|--------|-------|
| Total trades | 13 |
| Wins / Losses | 5W / 8L |
| Win rate | 38.5% |
| Total PnL | -$43.10 |
| Profit factor | ~0.45 |
| Brier score | 0.293 (all-time, worse than random) |
| Max drawdown | 93.2% (all-time) |
| Final bankroll | $73.02 (in-memory, reset to $100 at deploy) |

### Key Findings (v2 specific)
1. **Tighter drawdown (15%) halted too fast** — only 8 trades before halt, not enough to learn
2. **Model severely overconfident** — 0.80-0.90 predicted probability bucket had **0% actual win rate** (4 trades)
3. **Sensitivity=2.0 is too aggressive** — sigmoid maps small slopes to extreme probabilities
4. **TradeFlow still useless** — all 57 trades (v1+v2) had positive tradeflow, never negative
5. **Root cause of TradeFlow**: only measured buy/sell on Up token; in binary markets, Up token trades are mostly buys
6. **Large edge trades lose money** — 0.20+ edge bucket has 50% WR but -$1.59 avg PnL
7. **Small edge trades are the best** — 0.00-0.05 edge bucket has 100% WR, +$1.45 avg PnL
8. **Bankroll didn't persist across restart** — v2 reset to $100, losing continuity with v1 state
9. **No version tracking in database** — can't easily separate v1 vs v2 trade performance in SQL

---

## v3 — Overconfidence Fix + Data Collection Mode

**Deployed**: 2026-03-23 (pending deploy)
**Status**: Built locally, ready to deploy

### Changes from v2
| Parameter | v2 | v3 | Rationale |
|-----------|----|----|-----------|
| Momentum sensitivity | 2.0 | **1.0** | Sigmoid was too aggressive; small slopes → extreme probs |
| Edge sensitivity | 2.0 | **1.0** | Same issue — shared slope input |
| MaxModelProb | none | **0.80** | 0.80-0.90 calibration bucket had 0% actual WR |
| MaxEdge (new gate) | none | **0.15** | Large-edge trades (0.20+) lose money; reject overconfident |
| DrawdownLimit | 15% | **25%** | 15% was too tight, halted after 8 trades |
| HaltCooldown (new) | none | **10 min** | Auto-unhalt after 10m to keep collecting data |
| TradeFlow logic | Up buy/sell ratio | **Up vs Down volume** | Previous was always positive; now compares both tokens |
| Bankroll persistence | none | **SQLite restore** | Loads last bankroll_after from trades on startup |
| Bot version tracking | none | **bot_version column** | Every trade records "v3" for A/B comparison |

### Architecture Changes
- **`edge.go`**: Added `MaxModelProb` field, clamps model probability to [0.20, 0.80]
- **`momentum.go`**: Sensitivity 2.0 → 1.0
- **`tradeflow.go`**: Compares total volume on Up token vs Down token (both buy+sell)
- **`engine.go`**: Added `MaxEdge` config + gate: rejects trades where edge > 0.15
- **`manager.go`**: Added `HaltCooldown`, `haltedAt`, `haltCount`, `checkCooldown()`, `RestoreBankroll()`
- **`store.go`**: Added `bot_version` column, `LastBankroll()`, `TradeCount()` queries
- **`main.go`**: Added `BotVersion = "v3"` constant, bankroll restore on startup, version in LogTrade

### Hypothesis
- Reduced sensitivity + model prob cap should fix calibration (no more 95%+ predictions)
- MaxEdge gate should eliminate the worst overconfident bets (0.20+ edge, 50% WR)
- Fixed TradeFlow should add actual discriminating power (Up vs Down buying)
- Auto-unhalt ensures continuous data collection even during drawdowns
- Version tracking enables SQL-based A/B testing: `GROUP BY bot_version`

### Useful Queries for v3 Analysis
```sql
-- Compare v2 vs v3 performance
SELECT bot_version, COUNT(*) trades, SUM(CASE WHEN won=1 THEN 1 ELSE 0 END) wins,
       ROUND(AVG(CASE WHEN won=1 THEN 1.0 ELSE 0.0 END), 3) wr, ROUND(SUM(pnl), 2) pnl
FROM trades WHERE settled_at IS NOT NULL GROUP BY bot_version;

-- v3 calibration check (is the prob cap working?)
SELECT CASE
  WHEN model_prob < 0.6 THEN '0.50-0.60'
  WHEN model_prob < 0.7 THEN '0.60-0.70'
  WHEN model_prob < 0.8 THEN '0.70-0.80'
  ELSE '0.80+'
END bucket, COUNT(*) n, ROUND(AVG(model_prob),3) pred, ROUND(AVG(CASE WHEN won=1 THEN 1.0 ELSE 0.0 END),3) actual
FROM trades WHERE settled_at IS NOT NULL AND bot_version='v3' GROUP BY bucket;

-- Check if MaxEdge gate is helping (should see no edge > 0.15)
SELECT ROUND(edge, 2) edge, won, pnl FROM trades
WHERE bot_version='v3' AND settled_at IS NOT NULL ORDER BY edge DESC LIMIT 20;
```

### Results (v3 session: 53 trades via /status)
| Metric | Value |
|--------|-------|
| Total trades | 53 (session) |
| Wins / Losses | 34W / 19L |
| Win rate | 64.2% |
| Total PnL | +$4.36 (session) |
| Bankroll | $77.37 |

### Key Findings (v3)
1. **Session positive**: 64.2% WR, +$4.36 — significant improvement over v1/v2
2. **TradeFlow is inverted**: negative tradeflow has 78% WR (21/27) vs positive at 49% (41/83) — strong contrarian signal
3. **Imbalance still inverted**: negative 60% WR vs positive 53%
4. **Edge 0.10+ still loses money**: 0.10-0.20 bucket has 50% WR, -$1.19 avg PnL
5. **Clear hour-of-day pattern**: hours 15-21 UTC are all negative P&L
6. **Best hours**: 23 (+$17.6, 90% WR), 8 (+$20.3, 67%), 1 (+$6.8, 80%)
7. **Calibration improved in middle**: 0.60-0.70 bucket is perfectly calibrated (predicted 0.65, actual 0.65)

---

## v4 — Signal Inversions + Hour Filter + Tighter MaxEdge

**Deployed**: 2026-03-24 03:05 UTC
**Halted**: 2026-03-24 ~16:30 UTC (replaced by v5)
**Duration**: ~13 hours active trading

### Changes from v3
| Parameter | v3 | v4 | Rationale |
|-----------|----|----|-----------|
| TradeFlow direction | normal | **inverted** | 78% WR when negative vs 49% positive — strongest signal when flipped |
| Imbalance direction | normal | **inverted** | 60% WR when negative vs 53% positive — contrarian signal |
| MaxEdge | 0.15 | **0.10** | 0.10-0.15 edge still loses money; 0.05-0.10 is the sweet spot |
| Hour-of-day gate | none | **skip 15-21 UTC** | Hours 15-21 consistently negative P&L (-$100+ combined losses) |

### Architecture Changes
- **`tradeflow.go`**: Negated output — more Down volume now produces positive (bullish) signal
- **`imbalance.go`**: Negated output — more ask depth on Up token now produces positive signal
- **`engine.go`**: Added `SkipHoursUTC` config + gate before signal evaluation; MaxEdge 0.15 → 0.10
- **`main.go`**: BotVersion "v3" → "v4"

### Hypothesis
- Inverted TradeFlow should capture the strong contrarian pattern (78% WR → aligned with composite)
- Inverted Imbalance should stop hurting composite signal (was fighting correct direction)
- Tighter MaxEdge (0.10) eliminates remaining overconfident trades that lose money
- Hour filter avoids ~$100+ in losses from consistently unprofitable UTC afternoon/evening hours
- Combined effect: higher WR, lower loss rate, positive profit factor

### Results (95 trades, 13 hours)

| Metric | Value |
|--------|-------|
| Total trades | 95 |
| Wins / Losses | 57W / 38L |
| Win rate | 60.0% |
| Total PnL | -$7.18 |
| Avg win | +$1.62 |
| Avg loss | -$2.62 |
| Profit factor | ~0.75 |
| Final bankroll | $61.07 |

### Key Findings (v4)
1. **Hour filter working**: zero trades in hours 15-21, confirmed by DB
2. **MaxEdge 0.10 working**: all 95 trades in 0.05-0.10 bucket, no leaks
3. **BUY_UP profitable, BUY_DOWN bleeding**: BUY_UP 69% WR, +$11.11 vs BUY_DOWN 52% WR, -$18.29
4. **Entry price is the key variable**: cheap entries (<0.55) have 62% WR, +$10.90 PnL; expensive (0.55+) have 58% WR, -$18.09
5. **BUY_UP cheap (<0.55) is the golden trade**: 83% WR, +$14.62 over 12 trades
6. **Imbalance still inverted after double-inversion**: proven noise across 4 versions — no real signal
7. **TradeFlow inversion neutralized it**: was 49/78% split, now 60/60% — removed harm but gained nothing
8. **Strong momentum loses money despite higher WR**: 62% WR but -$13.70 (expensive entries); moderate (0.2-0.5) has 57% WR but +$6.52
9. **Low confidence trades are most profitable**: <0.25 conf has 62% WR, +$5.85; 0.50+ conf has 64% WR, -$6.28
10. **Hours 3-6 UTC also unprofitable**: 27 trades, 44% WR, -$19.62 — should be added to skip list
11. **Structural issue**: losses 1.6x bigger than wins — need 62% WR to break even, not just 60%
12. **Root cause is payoff asymmetry**: at entry price 0.60, risk $0.60 to win $0.40 — only cheap entries have favorable math

---

## v5 — Payoff Asymmetry Fix + Imbalance Removal + Momentum Cap

**Deployed**: 2026-03-24 ~16:30 UTC
**Status**: Running

### Changes from v4
| Parameter | v4 | v5 | Rationale |
|-----------|----|----|-----------|
| MaxEntryPrice | none | **0.55** | Cheap entries (<0.55) have +$10.90 PnL, expensive -$18.09. Caps payoff asymmetry |
| Imbalance weight | 0.05 | **0.00 (removed)** | Still inverted after double-inversion — proven noise across 4 versions |
| Momentum weight | 0.55 | **0.60** | Absorbed imbalance's weight. Strongest signal (17% separation in v4) |
| MaxMomentum | none | **0.50** | Strong momentum (>0.5) has 62% WR but -$13.70 PnL; moderate is +$6.52 |
| SkipHoursUTC | 15-21 | **3-6, 15-21** | Hours 3-6 had 44% WR, -$19.62 in v4 — consistently unprofitable |

### Architecture Changes
- **`engine.go`**: Removed imbalance from weighted signals, added `MaxMomentum` config field, caps momentum signal magnitude before weighting. Hours 3-6 added to skip list.
- **`manager.go`**: Added `MaxEntryPrice` field (default 0.55), rejects entry prices above threshold after MinEntryPrice check. Entry price now gated to [0.45, 0.55] band.
- **`main.go`**: BotVersion "v4" → "v5"

### Hypothesis
- MaxEntryPrice 0.55 eliminates the structural payoff problem: only trade when risk/reward is favorable
- Removing imbalance stops a noise signal from corrupting the composite (4 versions of evidence)
- Momentum cap prevents high-momentum → expensive entry → bad payoff pipeline
- Narrower hour window avoids ~$20 more in losses from hours 3-6
- Combined: bot should only trade the "golden" cheap-entry, moderate-momentum setups
- **Expected trade volume reduction**: fewer but higher quality trades (~4-6/hr in 7-14 UTC window)

### Useful Queries for v5 Analysis
```sql
-- v5 only performance
SELECT bot_version, COUNT(*) trades,
  SUM(CASE WHEN won=1 THEN 1 ELSE 0 END) wins,
  ROUND(AVG(CASE WHEN won=1 THEN 1.0 ELSE 0.0 END), 3) wr,
  ROUND(SUM(pnl), 2) pnl,
  ROUND(AVG(CASE WHEN won=1 THEN pnl ELSE NULL END), 3) avg_win,
  ROUND(AVG(CASE WHEN won=0 THEN pnl ELSE NULL END), 3) avg_loss
FROM trades WHERE settled_at IS NOT NULL AND bot_version='v5';

-- v5 entry price distribution (should all be 0.45-0.55)
SELECT ROUND(entry_price, 2) price, COUNT(*) n,
  ROUND(AVG(CASE WHEN won=1 THEN 1.0 ELSE 0.0 END), 3) wr,
  ROUND(SUM(pnl), 2) pnl
FROM trades WHERE bot_version='v5' AND settled_at IS NOT NULL
GROUP BY ROUND(entry_price, 2) ORDER BY price;

-- v5 direction breakdown
SELECT direction, COUNT(*) trades,
  ROUND(AVG(CASE WHEN won=1 THEN 1.0 ELSE 0.0 END), 3) wr,
  ROUND(SUM(pnl), 2) pnl
FROM trades WHERE bot_version='v5' AND settled_at IS NOT NULL GROUP BY direction;

-- Key metric: is profit factor > 1.0?
SELECT ROUND(SUM(CASE WHEN won=1 THEN pnl ELSE 0 END) / ABS(SUM(CASE WHEN won=0 THEN pnl ELSE 0 END)), 3) profit_factor
FROM trades WHERE bot_version='v5' AND settled_at IS NOT NULL;
```

### Results
_Pending — check back after 50+ trades (expect ~12 hours at reduced trade rate)_

| Metric | Value |
|--------|-------|
| Total trades | |
| Wins / Losses | |
| Win rate | |
| Total PnL | |
| Profit factor | |
| Avg win | |
| Avg loss | |
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

### Signal Improvements
- [ ] Refine hour-of-day filter with more data (currently skipping 15-21 UTC)
- [ ] Add CLOB trade velocity signal (trades/min acceleration)
- [ ] Add spread-based confidence: wider spread = lower confidence
- [ ] Add uncorrelated signals — momentum+edge share same slope input (85% weight on 1 input)
- [ ] VWAP divergence signal: is VWAP drifting from current price?
- [ ] Multi-timeframe momentum: compare 30s vs 60s vs 120s slopes
- [ ] External sentiment API as regime filter (trade/don't-trade, not directional)

### Risk Improvements
- [ ] Dynamic position sizing: reduce size when on a losing streak
- [ ] Bankroll floor: stop trading below $20 regardless of drawdown %
- [ ] Separate drawdown tracking per direction (BUY_UP vs BUY_DOWN)

### Infrastructure
- [ ] Add WebSocket reconnection for CLOB stream (currently only RTDS reconnects)
- [ ] Backtest engine: replay snapshots table to test new parameters offline
- [ ] Regret logging: log rejected decisions and check if they would have won
- [ ] Win rate by spread size (are illiquid markets worse?)
- [ ] Win rate by BTC volatility bucket (is vol gate threshold optimal?)

### Completed (moved from backlog)
- [x] ~~Fix TradeFlow signal~~ (v3: compares Up vs Down volume; v4: inverted based on 78% WR data)
- [x] ~~Consider removing imbalance~~ (v5: removed entirely — proven noise across 4 versions)
- [x] ~~Invert imbalance signal~~ (v4: inverted, v5: removed — still inverted after double-inversion)
- [x] ~~Hour-of-day filter~~ (v4: skip 15-21 UTC; v5: expanded to skip 3-6 UTC too)
- [x] ~~Tighten MaxEdge~~ (v4: 0.15 → 0.10, edge 0.10+ still loses money)
- [x] ~~Track bot version per trade~~ (v3: bot_version column in trades table)
- [x] ~~Persist bankroll across restarts~~ (v3: LastBankroll from SQLite)
- [x] ~~Auto-unhalt for data collection~~ (v3: 10min cooldown)
- [x] ~~Entry price filter~~ (v5: MaxEntryPrice 0.55 — expensive entries have bad payoff asymmetry)
- [x] ~~Cap momentum magnitude~~ (v5: MaxMomentum 0.50 — strong momentum = expensive entry = losses)
