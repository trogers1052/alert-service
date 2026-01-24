# Alert Service

**Language:** Go
**Status:** Not Started
**Priority:** Phase 3 (After Analytics)

## Purpose

Monitors real-time price and indicator data, evaluates alert rules, and sends notifications via Telegram/Pushover when conditions are met.

## Responsibilities

- Consume events from multiple Kafka topics (prices + indicators)
- Maintain in-memory state of current prices and indicators
- Query alert rules from PostgreSQL
- Evaluate alert conditions in real-time
- Send notifications (Telegram, Pushover, SMS, Email)
- Respect cooldown periods to prevent spam
- Log triggered alerts to `alert_history` table

## Kafka Topics

**Consumes:**
- `stock.quotes.realtime` - Real-time price updates
- `stock.indicators` - Technical indicator updates

**Produces:**
- `trading.alerts` - Alert trigger events (for audit/replay)

## Alert Rule Types

| Rule Type | Description | Example |
|-----------|-------------|---------|
| PRICE_TARGET | Price hits specific level | "Alert when AAPL >= $180" |
| RSI_OVERSOLD | RSI below threshold | "Alert when RSI < 30" |
| RSI_OVERBOUGHT | RSI above threshold | "Alert when RSI > 70" |
| SUPPORT_BOUNCE | Price near support + RSI low | "Alert at buy zone with oversold RSI" |
| RESISTANCE_BREAK | Price breaks resistance | "Alert when price breaks $200" |
| VOLUME_SPIKE | Unusual volume | "Alert when volume > 2x average" |

## Configuration

```env
KAFKA_BROKERS=localhost:19092
KAFKA_CONSUMER_GROUP=alert-service
KAFKA_PRICE_TOPIC=stock.quotes.realtime
KAFKA_INDICATOR_TOPIC=stock.indicators
KAFKA_ALERT_TOPIC=trading.alerts

DB_HOST=localhost
DB_PORT=5432
DB_USER=trader
DB_PASSWORD=trader5
DB_NAME=trading_platform

TELEGRAM_BOT_TOKEN=your_bot_token
TELEGRAM_CHAT_ID=your_chat_id
PUSHOVER_USER_KEY=your_user_key
PUSHOVER_API_TOKEN=your_api_token
```

## Data Flow

```
Kafka (stock.quotes.realtime) ──┐
                                ├──► Alert Service
Kafka (stock.indicators) ───────┘         │
                                          ├─► Evaluate Rules
                                          │   (from PostgreSQL)
                                          │
                                          ├─► Check Cooldowns
                                          │
                                          └─► Send Notifications
                                              - Telegram
                                              - Pushover
                                              - Log to alert_history
```

## Alert Message Format

```
🚨 ALERT: SLV Buy Zone

Symbol: SLV
Price: $28.45
RSI: 27.3

Condition: Price in buy zone ($28-$28.50) with RSI < 30

Action: Consider entry per your trading plan
```

## Build & Run

```bash
# Build
go build -o bin/alert-service ./cmd/alerts

# Run
./bin/alert-service

# Docker
docker build -t alert-service .
docker run --network trading-network alert-service
```

## TODO

- [ ] Implement multi-topic Kafka consumer
- [ ] Implement alert rule evaluation engine
- [ ] Implement Telegram notification client
- [ ] Implement Pushover notification client
- [ ] Add cooldown tracking
- [ ] Add alert history logging
- [ ] Add health check endpoint
- [ ] Add graceful shutdown
