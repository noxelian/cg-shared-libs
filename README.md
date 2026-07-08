# CG Shared Libraries

Общие библиотеки для всех микросервисов CG Platform.

Go 1.25.0.

## Установка

```bash
go get github.com/4ubak/cg-shared-libs@latest
```

## Пакеты

| Пакет | Описание |
|-------|----------|
| `config` | Загрузка конфигураций из YAML с переопределением через env-переменные |
| `logger` | Структурированное логирование на основе zap (JSON формат) |
| `postgres` | PostgreSQL клиент с пулом соединений, миграциями и поддержкой реплик |
| `redis` | Redis клиент с поддержкой пулов и настройкой DB |
| `kafka` | Kafka producer/consumer с поддержкой batch и retry |
| `jwt` | Генерация и валидация JWT токенов (access + refresh) |
| `grpc` | gRPC server/client helpers, TLS, JWT adapter, interceptors |
| `crypto` | Шифрование, хэширование паролей, миграция хэшей |
| `validation` | Валидация входных данных (телефоны, email, UUID и др.) |
| `health` | Health check сервер с проверками PostgreSQL, Redis, Kafka |
| `metrics` | Prometheus метрики и HTTP endpoint /metrics |
| `audit` | Аудит-лог событий (создание, обновление, удаление) |
| `i18n` | Интернационализация (ru, kk, en) с gRPC middleware |
| `tracing` | OpenTelemetry трассировка с gRPC interceptors и logging |
| `circuitbreaker` | Circuit breaker для защиты от каскадных отказов |
| `ratelimit` | Rate limiting (token bucket, multi-limiter) |
| `middleware` | HTTP middleware: CSRF защита, rate limiting |
| `security` | URL валидация, whitelist хостов, защита от SSRF |
| `ws` | WebSocket: upgrader, аутентификация, конфигурация |
| `pushpublisher` | Типизированный Kafka-producer для топика `notification.push`. Уже импортируется несколькими сервисами (`cg-users/organization`, `cg-services/request+bid`, `cg-communication/chat`) и всегда должен включаться только через service-specific feature flag; перед выводом в прод проверяй chart/env конкретного сервиса и его push-cutover handoff. |

## Использование

```go
import (
    "github.com/4ubak/cg-shared-libs/logger"
    "github.com/4ubak/cg-shared-libs/postgres"
    "github.com/4ubak/cg-shared-libs/redis"
    "github.com/4ubak/cg-shared-libs/kafka"
)

// Logger
logger.Init(cfg.Logger)
logger.Info("Starting service", zap.String("name", "my-service"))

// PostgreSQL
db, err := postgres.New(ctx, cfg.Postgres)

// Redis
rdb, err := redis.New(ctx, cfg.Redis)

// Kafka Producer
producer := kafka.NewProducer(cfg.Kafka, "my-topic")
producer.Publish(ctx, "key", event)
```

## Версионирование

Используем семантическое версионирование:
- `v1.x.x` - стабильная версия
- Breaking changes = новая major версия
