# Alert Service

A Go microservice in the trading platform that turns trade **decisions** and **rankings** into actionable Telegram alerts. It consumes scored decisions from Kafka, applies a set of delivery filters (confidence, risk/reward, checklist status, cooldowns, quiet hours), formats a rich alert with the full trade plan and pre-trade checklist, and ships it to Telegram with inline **feedback buttons**. When the user taps a button, the response is persisted back to `stock-service` so the platform can later correlate signals to outcomes.

It is deployed to a Raspberry Pi 5 alongside the other platform services.

## Role / Architecture

```
decision-engine ──(trading.decisions)──┐
                                        ├──► alert-service ──► Telegram (with trade plan,
decision-engine ──(trading.rankings)────┘          │            checklist, inline buttons)
                                                    │
                                  button tap ◄──────┘
                                                    │
                                                    └──► stock-service REST API (feedback persistence)
```

- **Consumes (Kafka):**
  - `trading.decisions` — per-symbol `DecisionEvent` with confidence, action (BUY/SELL/WATCH), an optional `TradePlan` (entry zone, ATR-based stop, targets, R:R) and an optional pre-trade `Checklist`.
  - `trading.rankings` — `RankingEvent` containing the current top-N ranked setups.
- **Produces:** nothing to Kafka. Output is Telegram messages.
- **State:** in-memory only (cooldown tracking, pending-feedback registry). No database.

## Features

- **Telegram delivery** of BUY / SELL / WATCH decision alerts and periodic top-N ranking summaries.
- **Rich formatting** — each decision alert renders the trade plan (entry, stop, targets, R:R) and the pre-trade checklist when present.
- **Inline feedback buttons** — *Traded* / *Skipped* buttons are attached to each alert; presses are captured via Telegram callback queries and forwarded to `stock-service` for signal-to-outcome analysis (with retry on transient failures).
- **Delivery filters:**
  - `MIN_CONFIDENCE` floor; per-action toggles (`ALERT_ON_BUY`, `ALERT_ON_SELL`, `ALERT_ON_WATCH`, `ALERT_ON_RANKINGS`).
  - **Minimum R:R** — BUY signals whose trade plan is below `MIN_RR_RATIO` are skipped.
  - **Checklist gate** — decisions with a `BLOCKED` checklist status are skipped.
  - **Cooldowns** — independent cooldowns for new signals, scale-ins, and rankings prevent spam.
  - **Quiet hours** (timezone-aware) and a **muted-symbols** list.
- **Operational endpoints** — `/health` and Prometheus metrics; a separate `healthcheck` binary for container health probes.

## Configuration

Configuration is via environment variables (see `.env.example`). Use placeholders — never commit real tokens.

| Variable | Default | Description |
|----------|---------|-------------|
| `KAFKA_BROKERS` | `localhost:19092` | Comma-separated Kafka/Redpanda brokers |
| `KAFKA_CONSUMER_GROUP` | `alert-service` | Consumer group ID |
| `KAFKA_DECISION_TOPIC` | `trading.decisions` | Decision events topic |
| `KAFKA_RANKING_TOPIC` | `trading.rankings` | Ranking events topic |
| `TELEGRAM_BOT_TOKEN` | _(required)_ | Telegram bot token |
| `TELEGRAM_CHAT_ID` | _(required)_ | Destination chat ID |
| `MIN_CONFIDENCE` | `0.6` | Minimum decision confidence to alert |
| `ALERT_ON_BUY` / `ALERT_ON_SELL` / `ALERT_ON_WATCH` | `true` / `true` / `false` | Per-action delivery toggles |
| `ALERT_ON_RANKINGS` | `true` | Deliver top-N ranking summaries |
| `RANKINGS_TOP_N` | `5` | Number of ranked setups per summary |
| `MIN_RR_RATIO` | `2.0` | Minimum risk/reward for BUY alerts |
| `COOLDOWN_MINUTES` | `30` | New-signal cooldown per symbol |
| `SCALE_IN_COOLDOWN_MINUTES` | `5` | Scale-in cooldown |
| `RANKING_COOLDOWN_MINUTES` | `60` | Ranking-summary cooldown |
| `ENABLE_QUIET_HOURS` | `false` | Suppress alerts during quiet hours |
| `QUIET_HOURS_START` / `QUIET_HOURS_END` | `22` / `7` | Quiet-hours window (hour of day) |
| `QUIET_HOURS_TIMEZONE` | `America/New_York` | Timezone for quiet hours |
| `MUTED_SYMBOLS` | _(empty)_ | Comma-separated symbols to suppress |
| `STOCK_SERVICE_URL` | `http://stock-service:8081` | Feedback persistence endpoint |
| `STOCK_SERVICE_API_KEY` | _(empty)_ | API key for `stock-service` (optional) |

## Running

**Local:**

```bash
cp .env.example .env   # fill in TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID
go run ./cmd/alerts
```

**Docker:**

```bash
docker build -t alert-service .
docker run --env-file .env alert-service
```

CI builds and publishes a multi-arch image (`linux/amd64,linux/arm64`) to `ghcr.io/trogers1052/alert-service`.

## Testing

```bash
go test ./...            # unit tests
go test -race -cover ./... # with race detector and coverage
```

Integration tests use the shared [`trading-testkit`](https://github.com/trogers1052/trading-testkit) (Redpanda/testcontainers); the Kafka contract test validates the decision/ranking event schemas.

## Project layout

```
cmd/
  alerts/        # service entry point + metrics
  healthcheck/   # container health-probe binary
internal/
  config/        # env-based configuration
  kafka/         # decision/ranking consumer (+ contract test)
  models/        # event, trade-plan, checklist, feedback types
  service/       # filtering, formatting, cooldowns, feedback loop
  telegram/      # Telegram client + inline keyboard / callback handling
  client/        # stock-service feedback client
  metrics/       # Prometheus metrics
```

---

## Built with Claude Code

A large portion of this project — implementation, tests, and documentation — was written in pair-programming sessions with [Claude Code](https://claude.com/claude-code), Anthropic's agentic command-line tool.
