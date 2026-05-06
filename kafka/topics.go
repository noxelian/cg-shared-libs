package kafka

// Platform Kafka topics. This is the single source of truth — every
// producer and consumer in the ecosystem MUST import the constant rather
// than hard-code a string. Adding a new topic is a two-step change:
//
//  1. Declare the constant here.
//  2. Document it in docs/architecture/kafka-topics.md (cg-shared-libs).
//
// Naming convention: <bounded-context>.<entity>.events for domain
// streams; <bounded-context>.events for cross-cutting realtime fanout
// (admin.events, notification.events). Past names that don't match the
// pattern stay for backwards compatibility but new topics should follow
// the convention.
const (
	// CRM domain (cg-crm publishes, bff-admin / analytics consume).
	TopicCRMDealEvents = "crm.deal.events"
	TopicCRMTaskEvents = "crm.task.events"
	TopicCRMLeadEvents = "crm.lead.events"

	// CRM → workshop handoff. Direct publish (sync) for the autobody
	// pipeline_type flow — see cg-crm DealPublisher.PublishDealToWorkshop.
	TopicCRMDealToWorkshop = "crm.deal.to_workshop"

	// CRM telephony recording lifecycle. Outbox-backed (migration 000063)
	// because transcript pipeline must not lose events on Kafka blips.
	TopicCRMTelephonyRecordingReady = "crm.telephony.recording_ready"

	// Marketplace / autobody (cg-orders + cg-services + workshop).
	TopicOrderEvents    = "order.events"
	TopicBidEvents      = "bid.events"
	TopicRequestEvents  = "request.events"
	TopicWorkshopEvents = "workshop.events"

	// Communication side-channels.
	TopicChatEvents         = "chat.events"
	TopicNotificationEvents = "notification.events"

	// Realtime fanout to the unified websocket-service. bff-admin
	// publishes here with data.recipient_user_ids set; the gateway
	// consumes and routes per user. See realtime.CRMBridge in cg-bff.
	TopicAdminEvents = "admin.events"

	// Auth / session lifecycle. cg-users publishes member-revoked /
	// force-logout events; bff-admin and websocket-service consume to
	// kick the user out of long-lived connections.
	TopicMemberEvents = "member.events"
)

// AllRealtimeTopics returns the topics the unified websocket-service
// consumes by default. Kept here so the chart's KAFKA_TOPICS env doesn't
// drift from what the producers actually publish.
func AllRealtimeTopics() []string {
	return []string{
		TopicChatEvents,
		TopicOrderEvents,
		TopicBidEvents,
		TopicRequestEvents,
		TopicWorkshopEvents,
		TopicNotificationEvents,
		TopicAdminEvents,
	}
}
