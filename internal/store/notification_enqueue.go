package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sunxu/relay-station-control/internal/dingtalk"
	"github.com/sunxu/relay-station-control/internal/jobs"
)

var notificationNamespace = uuid.MustParse("94db90f6-d7e6-4cce-a045-890b63171d86")

// notificationTransition contains only the authoritative domain transition and
// display values read inside its owning transaction. It is never reconstructed
// by a worker or by an after-commit scan.
type notificationTransition struct {
	OccurrenceID    uuid.UUID   `json:"occurrence_id"`
	OccurrenceType  string      `json:"occurrence_type"`
	Transition      string      `json:"transition"`
	Reason          string      `json:"reason"`
	Severity        string      `json:"severity"`
	EnvironmentID   string      `json:"environment_id"`
	EnvironmentName string      `json:"environment_name"`
	AccountKey      string      `json:"account_key"`
	Email           string      `json:"email"`
	Provider        string      `json:"provider"`
	InstanceIDs     []uuid.UUID `json:"instance_ids"`
	NodeNames       []string    `json:"node_names"`
	StartedAt       time.Time   `json:"started_at"`
	TransitionedAt  time.Time   `json:"transitioned_at"`
}

type notificationDelivery struct {
	registry         *jobs.Registry
	publisherEnabled bool
}

// SetNotificationDelivery is startup-only wiring. A nil registry disables
// notification enqueue entirely; publisher suppression is not this gate.
func (r *AccountAvailabilityRepository) SetNotificationDelivery(registry *jobs.Registry, publisherEnabled bool) {
	r.notifications = notificationDeliveryFor(registry, publisherEnabled)
}

// SetNotificationDelivery is startup-only wiring, independent of the
// observability-only after-commit alert observer.
func (r *CrossNodeDuplicateOwnershipLifecycleRepository) SetNotificationDelivery(registry *jobs.Registry, publisherEnabled bool) {
	r.notifications = notificationDeliveryFor(registry, publisherEnabled)
}

func notificationDeliveryFor(registry *jobs.Registry, publisherEnabled bool) *notificationDelivery {
	if registry == nil {
		return nil
	}
	return &notificationDelivery{registry: registry, publisherEnabled: publisherEnabled}
}

func notificationEnqueueRequest(snapshot notificationTransition, publisherEnabled bool) (jobs.EnqueueRequest, error) {
	if snapshot.OccurrenceID == uuid.Nil || snapshot.StartedAt.IsZero() || snapshot.TransitionedAt.IsZero() ||
		snapshot.InstanceIDs == nil || snapshot.NodeNames == nil || len(snapshot.InstanceIDs) != len(snapshot.NodeNames) {
		return jobs.EnqueueRequest{}, jobs.ErrInvalidPayload
	}
	domain := "availability"
	switch snapshot.OccurrenceType {
	case "TOKEN_INVALID", "ACCOUNT_BLOCKED", "FORBIDDEN":
	case "CROSS_NODE_DUPLICATE_OWNERSHIP":
		domain = "duplicate"
	default:
		return jobs.EnqueueRequest{}, jobs.ErrInvalidPayload
	}
	if snapshot.Transition != "ACTIVE" && snapshot.Transition != "RESOLVED" {
		return jobs.EnqueueRequest{}, jobs.ErrInvalidPayload
	}
	// Sort indices, not either parallel array independently. Do not mutate the
	// domain result: observers and other callers retain their original snapshot.
	order := make([]int, len(snapshot.InstanceIDs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return snapshot.InstanceIDs[order[i]].String() < snapshot.InstanceIDs[order[j]].String()
	})
	ids, names := make([]string, len(order)), make([]string, len(order))
	for i, source := range order {
		ids[i], names[i] = snapshot.InstanceIDs[source].String(), snapshot.NodeNames[source]
	}
	payload, err := json.Marshal(dingtalk.Payload{
		OccurrenceID: snapshot.OccurrenceID.String(), OccurrenceType: snapshot.OccurrenceType,
		Transition: snapshot.Transition, Reason: snapshot.Reason, Severity: snapshot.Severity,
		EnvironmentID: snapshot.EnvironmentID, EnvironmentName: snapshot.EnvironmentName,
		AccountKey: snapshot.AccountKey, Email: snapshot.Email, Provider: snapshot.Provider,
		InstanceIDs: ids, NodeNames: names, StartedAt: snapshot.StartedAt.UTC().Format(time.RFC3339Nano),
		TransitionedAt: snapshot.TransitionedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return jobs.EnqueueRequest{}, jobs.ErrInvalidPayload
	}
	key := "dingtalk:" + domain + ":" + snapshot.OccurrenceID.String() + ":" + strings.ToLower(snapshot.Transition)
	return jobs.EnqueueRequest{
		Kind: dingtalk.JobKind, SchemaVersion: 1, Priority: 50, Actor: jobs.ActorService,
		IdempotencyKey: key, OperationID: uuid.NewSHA1(notificationNamespace, []byte(key)),
		Payload: payload, PublisherEnabled: publisherEnabled,
	}, nil
}

func enqueueNotificationTx(ctx context.Context, tx pgx.Tx, delivery *notificationDelivery, snapshot notificationTransition) error {
	if delivery == nil {
		return nil
	}
	request, err := notificationEnqueueRequest(snapshot, delivery.publisherEnabled)
	if err != nil {
		return err
	}
	txStore, err := NewJobTxStore(tx)
	if err != nil {
		return err
	}
	// The existing Registry and EnqueueTx remain the only canonicalization,
	// hashing, idempotency and atomic job/event/outbox insertion authority.
	_, err = jobs.EnqueueTx(ctx, txStore, delivery.registry, request)
	return err
}
