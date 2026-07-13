// Package pushpublisher provides a typed publisher for the notification.push
// Kafka topic consumed by cg-communication/services/notification.
//
// STATUS: dormant. As of 2026-07-02 no service in the monorepo imports this
// package, so nothing publishes to notification.push — the consumer side in
// cg-communication (services/notification/internal/consumer/push_consumer.go)
// currently has no producer at all. Kept (unlike other unwired packages)
// because the consumer already exists and depends on this exact schema; wire
// this into whichever service should trigger mobile pushes (orders/booking
// are the likely candidates) before relying on push notifications working.
//
// Usage:
//
//	pub := pushpublisher.New(kafkaProducer)
//	pub.Publish(ctx, pushpublisher.Event{
//	    UserID:     orderBuyerID,
//	    EventType:  "order.status_changed",
//	    Title:      "Статус заказа изменён",
//	    Body:       "Ваш заказ принят в обработку",
//	    TargetApps: pushpublisher.AppsClient,
//	})
package pushpublisher

import (
	"context"
	"fmt"

	"github.com/4ubak/cg-shared-libs/kafka"
	"github.com/4ubak/cg-shared-libs/logger"
	"go.uber.org/zap"
)

const topicPush = "notification.push"

// The keys and routes below are the public mobile-navigation contract for
// notification.push. Producers must use RouteData instead of legacy `type`
// discriminators: a route is stable across iOS/Android/Web and identifiers
// remain explicit UUID fields rather than overloaded legacy integer keys.
const (
	PushSchemaKey        = "schema"
	PushSchemaVersionKey = "schema_version"
	PushRouteKey         = "route"

	PushSchema        = "ctogram.push.v1"
	PushSchemaVersion = "1"
)

// Route identifies the destination a mobile application must resolve after a
// notification tap. It intentionally describes navigation, not the Kafka
// domain event: one business event can result in different recipient routes.
type Route string

const (
	RouteClientRequestResponses     Route = "client.request.responses"
	RouteClientPartsRequestDetail   Route = "client.parts_request.detail"
	RoutePartnerRequestDetail       Route = "partner.request.detail"
	RoutePartnerOrderDetail         Route = "partner.order.detail"
	RouteChatDetail                 Route = "chat.detail"
	RoutePartnerOrganizationReviews Route = "partner.organization.reviews"
)

// RouteData creates a versioned and immutable-at-the-boundary payload for
// Firebase. Reserved routing keys are always owned by this package, so a
// caller cannot accidentally downgrade the contract while adding entity data.
func RouteData(route Route, values map[string]string) map[string]string {
	data := make(map[string]string, len(values)+3)
	for key, value := range values {
		switch key {
		case PushSchemaKey, PushSchemaVersionKey, PushRouteKey:
			continue
		default:
			data[key] = value
		}
	}
	data[PushSchemaKey] = PushSchema
	data[PushSchemaVersionKey] = PushSchemaVersion
	data[PushRouteKey] = string(route)
	return data
}

// App identifies a mobile application variant.
type App = string

const (
	// AppClient is the client-facing mobile application.
	AppClient App = "client"
	// AppPartner is the partner/business mobile application.
	AppPartner App = "partner"
)

// AppsClient targets only the client app.
var AppsClient = []string{AppClient}

// AppsPartner targets only the partner app.
var AppsPartner = []string{AppPartner}

// AppsBoth targets both client and partner apps.
// Equivalent to nil TargetApps — kept for explicit documentation purposes.
var AppsBoth = []string{AppClient, AppPartner}

// Event is the schema for a notification.push Kafka message.
// Mirrors consumer.PushEvent in cg-communication — keep in sync.
type Event struct {
	// UserID is the recipient.
	UserID int64 `json:"user_id"`
	// EventType is a dot-separated identifier like "order.status_changed".
	EventType string `json:"event_type"`
	// Title is the push notification title (localised).
	Title string `json:"title"`
	// Body is the push notification body (localised).
	Body string `json:"body"`
	// Data carries arbitrary string key-values forwarded to the mobile client.
	Data map[string]string `json:"data,omitempty"`
	// Priority is "high" (default when empty) or "normal".
	Priority string `json:"priority,omitempty"`
	// DedupKey is an optional idempotency key.
	DedupKey string `json:"dedup_key,omitempty"`
	// TargetApps restricts delivery to specific app variants.
	// Use AppsClient, AppsPartner, or AppsBoth constants.
	// nil or empty means both apps — backward-compatible with legacy publishers.
	TargetApps []string `json:"target_apps,omitempty"`
}

// Publisher publishes typed push events to the notification.push Kafka topic.
// The underlying kafka.Producer is pre-configured with the notification.push topic.
type Publisher struct {
	producer *kafka.Producer
}

// New creates a Publisher backed by the given Kafka producer.
// The producer must be initialised with the notification.push topic.
// Pass nil to create a no-op publisher (useful in tests / staging environments
// where Kafka is unavailable).
func New(producer *kafka.Producer) *Publisher {
	return &Publisher{producer: producer}
}

// Publish sends a PushEvent to the notification.push topic.
// Errors are logged but not returned — push delivery is best-effort and
// must not block the caller's business transaction.
func (p *Publisher) Publish(ctx context.Context, event Event) {
	if p.producer == nil {
		return
	}

	key := fmt.Sprintf("push:%d", event.UserID)
	if err := p.producer.PublishJSON(ctx, key, event); err != nil {
		logger.Error("pushpublisher: publish to kafka",
			zap.String("topic", topicPush),
			zap.String("event_type", event.EventType),
			zap.Int64("user_id", event.UserID),
			zap.Error(err),
		)
	}
}

// PublishBatch sends push events for multiple users at once.
// Each event is published independently — partial failures are logged.
func (p *Publisher) PublishBatch(ctx context.Context, events []Event) {
	for _, e := range events {
		p.Publish(ctx, e)
	}
}
