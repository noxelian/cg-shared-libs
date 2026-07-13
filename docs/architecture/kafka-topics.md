# Kafka Topic Catalog

Single source of truth: [`kafka/topics.go`](../../kafka/topics.go). Every producer and consumer in the ecosystem MUST import the constant — never hard-code a topic string.

## Naming convention

`<bounded-context>.<entity>.events` for domain streams, `<bounded-context>.events` for cross-cutting realtime fanout. Existing names that don't match the pattern are kept for backwards compatibility but new topics should follow it.

## Catalog

| Constant | Topic | Producer | Consumer(s) | Purpose |
|---|---|---|---|---|
| `TopicCRMDealEvents` | `crm.deal.events` | cg-crm `DealPublisher`, `PipelinePublisher` | bff-admin `dealEventsConsumer`, webhook delivery worker | Deal lifecycle: created/updated/stage_changed/won/lost/notes_appended/notes_acknowledged |
| `TopicCRMTaskEvents` | `crm.task.events` | cg-crm `TaskPublisher` | bff-admin `taskEventsConsumer` | Task lifecycle: created/updated/completed/due_soon |
| `TopicCRMLeadEvents` | `crm.lead.events` | cg-crm `LeadPublisher` | bff-admin (planned) | Lead lifecycle |
| `TopicCRMDealToWorkshop` | `crm.deal.to_workshop` | cg-crm (sync direct publish) | cg-workshop | Autobody pipeline_type → workshop handoff |
| `TopicCRMTelephonyEvents` | `crm.telephony.events` | cg-crm `TelephonyPublisher` (outbox-backed, migration 000063) | cg-ai transcriber, bff-admin `transcriptConsumer` | recording_ready, transcript_ready |
| `TopicOrderEvents` | `order.events` | cg-orders, cg-workshop | cg-communication/websocket | Marketplace order lifecycle |
| `TopicBidEvents` | `bid.events` | cg-services/bid | cg-communication/websocket | Marketplace bids |
| `TopicRequestEvents` | `request.events` | cg-services/request | cg-communication/websocket | Customer requests |
| `TopicWorkshopEvents` | `workshop.events` | cg-workshop | cg-communication/websocket | Workshop order status changes |
| `TopicChatEvents` | `chat.events` | cg-communication/chat | cg-communication/websocket | In-app chat messages |
| `TopicNotificationEvents` | `notification.events` | cg-communication/notification | cg-communication/websocket | Push/in-app notifications |
| `TopicAdminEvents` | `admin.events` | bff-admin `realtime.CRMBridge`, AI/Workshop bridges | cg-communication/websocket | Realtime fanout to staff browsers — payload carries `data.recipient_user_ids` for per-user routing |
| `TopicMemberEvents` | `member.events` | cg-users | bff-admin `revocationConsumer`, cg-communication/websocket | force_logout / role_revoked |

## Ordered aggregate topics

Producers that require per-aggregate ordering use `NewKeyedProducer` and a
non-empty stable aggregate key. It uses the Java-compatible Murmur2 partitioner.
The partition count of such a topic is an immutable runtime invariant: changing
it remaps keys under every modulo-based partitioner and can let new events pass
older records on the previous partition. Capacity changes require a controlled
drain and versioned-topic cutover, not in-place partition expansion.

Consumers normally exhaust bounded retries and then commit or route to DLQ.
When a valid message is blocked by a required dependency and commit would lose
it, the handler may return `kafka.RetryUntilCanceled(err)`. That explicit
disposition retains the offset and retries with bounded backoff until recovery
or consumer shutdown; it must not be used for malformed or poison messages.
Operators can alert on `kafka_consumer_retained_offsets` and diagnose retry
volume with `kafka_consumer_retained_retries_total`.

## How realtime events flow to the browser

```
cg-crm publish (e.g. crm.deal.updated)
  → Kafka topic crm.deal.events
    → bff-admin dealEventsConsumer
      resolves recipients (pipeline_members + admins via gRPC)
      → Kafka topic admin.events with data.recipient_user_ids
        → cg-communication/websocket
          mapKafkaEventToClientEvent whitelist
          → routeUserIDsFromEvent(recipient_user_ids)
            → user's wss://api.staging.ctogram.net/ws connection
```

## Adding a new topic

1. Declare the constant in [`kafka/topics.go`](../../kafka/topics.go).
2. Add a row to this table.
3. Tag a new shared-libs version, bump the producer + consumer services to it.
4. Producer: use `Producer.PublishJSONTo(ctx, topic, key, data)` (the bound-topic `PublishJSON` only writes to the topic the Producer was constructed with — see the migration notes for why all CRM domain events landed in the wrong topic before this change).
5. Consumer: subscribe via `kafka.NewConsumer(cfg, topic)` referencing the constant.
6. If the event should reach the unified websocket-service, add it to the whitelist in `cg-communication/services/websocket/internal/app/kafka.go` `mapKafkaEventToClientEvent`.

## Historical note: the `crm.events` dead-letter

Before shared-libs v1.34.0, `cg-crm`'s `BufferedPublisher.flushBatch` called `producer.PublishJSON(ctx, event.Topic, event.Data)` — passing `event.Topic` as the **message key**, not the topic. The Kafka producer was bound to `"crm.events"` at construction, so every domain event silently landed there regardless of intent. Consumers subscribed to `crm.deal.events` / `crm.task.events` saw nothing — which is why staff CRM realtime updates required F5 for months. The fix in shared-libs v1.34.0 added `Producer.PublishJSONTo`, and `cg-crm` was switched to use it.
