package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/dingtalk"
	jobcore "github.com/sunxu/relay-station-control/internal/jobs"
	productstore "github.com/sunxu/relay-station-control/internal/store"
)

func duplicateNotificationRegistry(t *testing.T) *jobcore.Registry {
	t.Helper()
	registry, err := jobcore.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{})))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func durableNotificationCounts(t *testing.T, ctx context.Context, database *isolatedJobDatabase) (jobs, events, outbox int) {
	t.Helper()
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM async_jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM async_job_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM operation_outbox`).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	return
}

func TestCrossNodeDuplicateNotificationMembershipOnlyDoesNotCreateIntent(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "duplicate-notify-membership"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	group := newProblemOwnershipNodeGroup(t, ctx, database, "notify-membership", 3)
	accountKey := "antigravity:membership@example.invalid"
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, []string{"membership@example.invalid"}, 1, false)
	}
	repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	repository.SetNotificationDelivery(duplicateNotificationRegistry(t), false)
	created, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || !created.Created || created.Status != "ACTIVE" {
		t.Fatalf("create result = %+v", created)
	}
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("initial durable notification counts = %d/%d/%d, want 1/1/1", jobs, events, outbox)
	}

	group.finalize(t, ctx, database, group.nodes[0], nil, 0, false)
	membershipOnly, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if membershipOnly.Status != "ACTIVE" || len(membershipOnly.Removed) != 1 {
		t.Fatalf("membership-only result = %+v", membershipOnly)
	}
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("membership-only changed durable notification counts = %d/%d/%d", jobs, events, outbox)
	}

	group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
	resolved, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "RESOLVED" || len(resolved.Removed) != 1 {
		t.Fatalf("resolve result = %+v", resolved)
	}
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 2 || events != 2 || outbox != 2 {
		t.Fatalf("resolve durable notification counts = %d/%d/%d, want 2/2/2", jobs, events, outbox)
	}
}

func TestCrossNodeDuplicateNotificationDisabledEnableDoesNotBackfill(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "duplicate-notify-no-backfill"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	group := newProblemOwnershipNodeGroup(t, ctx, database, "notify-no-backfill", 3)
	accountKey := "antigravity:no-backfill@example.invalid"
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, []string{"no-backfill@example.invalid"}, 1, false)
	}
	repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	created, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || created == nil || !created.Created {
		t.Fatalf("disabled create result = %+v, err=%v", created, err)
	}
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 0 || events != 0 || outbox != 0 {
		t.Fatalf("disabled create left durable notifications = %d/%d/%d", jobs, events, outbox)
	}

	repository.SetNotificationDelivery(duplicateNotificationRegistry(t), false)
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, []string{"no-backfill@example.invalid"}, 2, false)
	}
	refreshed, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || refreshed.Status != "ACTIVE" || refreshed.Created {
		t.Fatalf("enabled refresh result = %+v, err=%v", refreshed, err)
	}
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 0 || events != 0 || outbox != 0 {
		t.Fatalf("enable backfilled durable notifications = %d/%d/%d", jobs, events, outbox)
	}

	group.finalize(t, ctx, database, group.nodes[0], nil, 0, false)
	if _, err := repository.Evaluate(ctx, environmentID, accountKey); err != nil {
		t.Fatal(err)
	}
	group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
	resolved, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || resolved.Status != "RESOLVED" {
		t.Fatalf("resolved result = %+v, err=%v", resolved, err)
	}
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("resolved durable notification counts = %d/%d/%d, want 1/1/1", jobs, events, outbox)
	}
}

func readDuplicateNotificationPayloads(t *testing.T, ctx context.Context, database *isolatedJobDatabase) map[string]dingtalk.Payload {
	t.Helper()
	rows, err := database.owner.Query(ctx, `SELECT idempotency_key, payload FROM async_jobs ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]dingtalk.Payload{}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			t.Fatal(err)
		}
		var payload dingtalk.Payload
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		result[key] = payload
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCrossNodeDuplicateNotificationUsesTransitionTimeRenameSnapshot(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "duplicate-notify-rename"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	group := newProblemOwnershipNodeGroup(t, ctx, database, "notify-rename", 2)
	accountKey := "antigravity:rename@example.invalid"
	for i, node := range group.nodes {
		if _, err := database.owner.Exec(ctx, `UPDATE relay_node_assets SET display_name=$2 WHERE instance_id=$1`, node, fmt.Sprintf("Old-%d", i)); err != nil {
			t.Fatal(err)
		}
		group.finalize(t, ctx, database, node, []string{"rename@example.invalid"}, 1, false)
	}
	repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	repository.SetNotificationDelivery(duplicateNotificationRegistry(t), false)
	created, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || !created.Created {
		t.Fatalf("create result = %+v", created)
	}

	for i, node := range group.nodes {
		if _, err := database.owner.Exec(ctx, `UPDATE relay_node_assets SET display_name=$2 WHERE instance_id=$1`, node, fmt.Sprintf("New-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
	resolved, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || resolved.Status != "RESOLVED" {
		t.Fatalf("resolve result = %+v, err=%v", resolved, err)
	}
	if len(resolved.AffectedNodes) != 1 || resolved.AffectedNodes[0] != group.nodes[0] {
		t.Fatalf("resolution membership = %v, want [%s]", resolved.AffectedNodes, group.nodes[0])
	}
	payloads := readDuplicateNotificationPayloads(t, ctx, database)
	active := payloads["dingtalk:duplicate:"+created.OccurrenceID.String()+":active"]
	resolvedPayload := payloads["dingtalk:duplicate:"+created.OccurrenceID.String()+":resolved"]
	if len(active.InstanceIDs) != 2 || len(active.NodeNames) != 2 {
		t.Fatalf("active snapshot arrays = ids=%v names=%v", active.InstanceIDs, active.NodeNames)
	}
	activeIDs := []string{group.nodes[0].String(), group.nodes[1].String()}
	sort.Strings(activeIDs)
	for i, id := range activeIDs {
		if active.InstanceIDs[i] != id || active.NodeNames[i] != fmt.Sprintf("Old-%d", indexOfUUID(group.nodes, id)) {
			t.Fatalf("active ID/name pairing = ids=%v names=%v", active.InstanceIDs, active.NodeNames)
		}
	}
	if len(resolvedPayload.InstanceIDs) != 1 || len(resolvedPayload.NodeNames) != 1 ||
		resolvedPayload.InstanceIDs[0] != group.nodes[0].String() || resolvedPayload.NodeNames[0] != "New-0" {
		t.Fatalf("resolved rename snapshot = ids=%v names=%v", resolvedPayload.InstanceIDs, resolvedPayload.NodeNames)
	}
}

func indexOfUUID(ids []uuid.UUID, target string) int {
	for i, id := range ids {
		if id.String() == target {
			return i
		}
	}
	return -1
}

func TestCrossNodeDuplicateNotificationEnqueueFailureRollsBackAllRows(t *testing.T) {
	for _, faultTable := range []string{"async_jobs", "async_job_events", "operation_outbox"} {
		t.Run(faultTable, func(t *testing.T) {
			ctx := context.Background()
			database := newIsolatedJobDatabase(t)
			environmentID := "duplicate-notify-trigger-failure"
			newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
			group := newProblemOwnershipNodeGroup(t, ctx, database, "notify-trigger-failure", 2)
			accountKey := "antigravity:trigger-failure@example.invalid"
			for _, node := range group.nodes {
				group.finalize(t, ctx, database, node, []string{"trigger-failure@example.invalid"}, 1, false)
			}
			triggerSuffix := assetFixtureSuffix(t)
			functionName := "test_notification_enqueue_failure_" + triggerSuffix
			triggerName := "test_notification_enqueue_failure_trigger_" + triggerSuffix
			if _, err := database.owner.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION public.%s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'notification enqueue integration failure'; END; $$;
		CREATE TRIGGER %s BEFORE INSERT ON public.%s
		FOR EACH ROW EXECUTE FUNCTION public.%s();`, functionName, triggerName, faultTable, functionName)); err != nil {
				t.Fatal(err)
			}
			repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
			repository.SetNotificationDelivery(duplicateNotificationRegistry(t), false)
			if result, err := repository.Evaluate(ctx, environmentID, accountKey); err == nil || result != nil {
				t.Fatalf("trigger failure result=%+v err=%v, want rollback error", result, err)
			}
			if count := occurrenceRowCount(t, ctx, database, environmentID, accountKey); count != 0 {
				t.Fatalf("rollback left %d occurrence rows", count)
			}
			if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 0 || events != 0 || outbox != 0 {
				t.Fatalf("rollback left durable rows = %d/%d/%d", jobs, events, outbox)
			}
			for _, table := range []string{"cross_node_duplicate_occurrence_nodes", "cross_node_duplicate_occurrence_evidence"} {
				var count int
				if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s rollback residue=%d err=%v", table, count, err)
				}
			}
			if _, err := database.owner.Exec(ctx, fmt.Sprintf(`DROP TRIGGER %s ON public.%s; DROP FUNCTION public.%s()`, triggerName, faultTable, functionName)); err != nil {
				t.Fatal(err)
			}
			if result, err := repository.Evaluate(ctx, environmentID, accountKey); err != nil || result == nil || !result.Created {
				t.Fatalf("post-rollback retry=%+v err=%v", result, err)
			}
			if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 1 || events != 1 || outbox != 1 {
				t.Fatalf("post-rollback retry counts=%d/%d/%d", jobs, events, outbox)
			}
		})
	}
}

func TestCrossNodeDuplicateNotificationSuccessMakesJobEventOutboxVisible(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "duplicate-notify-success"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	group := newProblemOwnershipNodeGroup(t, ctx, database, "notify-success", 2)
	accountKey := "antigravity:success@example.invalid"
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, []string{"success@example.invalid"}, 1, false)
	}
	repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	repository.SetNotificationDelivery(duplicateNotificationRegistry(t), false)
	created, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	var jobID, operationID uuid.UUID
	var key string
	if err := database.owner.QueryRow(ctx, `SELECT job_id, operation_id, idempotency_key FROM async_jobs`).Scan(&jobID, &operationID, &key); err != nil {
		t.Fatal(err)
	}
	wantKey := "dingtalk:duplicate:" + created.OccurrenceID.String() + ":active"
	if key != wantKey || operationID != uuid.NewSHA1(uuid.MustParse("94db90f6-d7e6-4cce-a045-890b63171d86"), []byte(wantKey)) {
		t.Fatalf("job identity key=%q operation=%s", key, operationID)
	}
	var eventJobID, outboxJobID uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT job_id FROM async_job_events WHERE job_id=$1`, jobID).Scan(&eventJobID); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT job_id FROM operation_outbox WHERE job_id=$1`, jobID).Scan(&outboxJobID); err != nil {
		t.Fatal(err)
	}
	if eventJobID != jobID || outboxJobID != jobID {
		t.Fatalf("job linkage event=%s outbox=%s job=%s", eventJobID, outboxJobID, jobID)
	}
}

func TestCrossNodeDuplicateNotificationProblemsMembershipAndAvailabilityCoexist(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "duplicate-notify-problems"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	group := newProblemOwnershipNodeGroup(t, ctx, database, "notify-problems", 3)
	accountKey := "antigravity:problems@example.invalid"
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, []string{"problems@example.invalid"}, 1, false)
	}
	if _, err := database.owner.Exec(ctx, `
		INSERT INTO account_availability_occurrences(
			node_id, account_key, reason, severity, status, first_seen_at,
			last_failure_at, confirmed_at
		) VALUES ($1,$2,'token_invalid','Critical','ACTIVE',statement_timestamp(),statement_timestamp(),statement_timestamp())`,
		group.nodes[0], accountKey); err != nil {
		t.Fatal(err)
	}
	repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	repository.SetNotificationDelivery(duplicateNotificationRegistry(t), false)
	created, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || !created.Created {
		t.Fatalf("create result = %+v", created)
	}
	problems, err := productstore.NewProblemAccountRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	assertProblemDuplicateCounts := func(wantDuplicateRows int, wantAvailabilityNode bool) {
		t.Helper()
		page, err := problems.ListProblemAccounts(ctx, productstore.ProblemAccountQuery{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		duplicateRows := 0
		availabilityRows := 0
		for _, item := range page.Items {
			for _, issue := range item.Issues {
				if issue.Type == "CROSS_NODE_DUPLICATE_OWNERSHIP" && issue.OccurrenceID == created.OccurrenceID {
					duplicateRows++
				}
				if issue.Type == "TOKEN_INVALID" && item.InstanceID == group.nodes[0] {
					availabilityRows++
				}
			}
		}
		if duplicateRows != wantDuplicateRows {
			t.Fatalf("Problems duplicate rows=%d, want %d; page=%+v", duplicateRows, wantDuplicateRows, page.Items)
		}
		if wantAvailabilityNode && availabilityRows != 1 {
			t.Fatalf("Problems lost coexisting Availability issue: page=%+v", page.Items)
		}
	}
	assertProblemDuplicateCounts(3, true)
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("initial durable counts=%d/%d/%d", jobs, events, outbox)
	}

	// Re-evaluating unchanged ACTIVE evidence is idempotent and does not add
	// another notification job/event/outbox row.
	for _, node := range group.nodes {
		group.finalize(t, ctx, database, node, []string{"problems@example.invalid"}, 2, false)
	}
	refreshed, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || refreshed.Created || refreshed.Status != "ACTIVE" {
		t.Fatalf("refresh result=%+v err=%v", refreshed, err)
	}
	assertProblemDuplicateCounts(3, true)
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("refresh duplicated durable counts=%d/%d/%d", jobs, events, outbox)
	}

	group.finalize(t, ctx, database, group.nodes[0], nil, 0, false)
	shrunk, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || shrunk.Status != "ACTIVE" || len(shrunk.Removed) != 1 {
		t.Fatalf("shrink result=%+v err=%v", shrunk, err)
	}
	assertProblemDuplicateCounts(2, true)
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("shrink created unexpected intent counts=%d/%d/%d", jobs, events, outbox)
	}

	group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
	resolved, err := repository.Evaluate(ctx, environmentID, accountKey)
	if err != nil || resolved == nil || resolved.Status != "RESOLVED" || len(resolved.AffectedNodes) != 1 || resolved.AffectedNodes[0] != group.nodes[2] {
		t.Fatalf("resolve result=%+v err=%v", resolved, err)
	}
	assertProblemDuplicateCounts(0, true)
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 2 || events != 2 || outbox != 2 {
		t.Fatalf("resolve durable counts=%d/%d/%d, want 2/2/2", jobs, events, outbox)
	}

	// A subsequent pass has no ACTIVE occurrence and must not recreate the
	// resolved notification or alter the independent Availability issue.
	if result, err := repository.Evaluate(ctx, environmentID, accountKey); err != nil || result != nil {
		t.Fatalf("post-resolve evaluation=%+v err=%v, want nil", result, err)
	}
	assertProblemDuplicateCounts(0, true)
	if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 2 || events != 2 || outbox != 2 {
		t.Fatalf("post-resolve duplicated durable counts=%d/%d/%d", jobs, events, outbox)
	}

	t.Run("all affected nodes absent before one resolve evaluation yields empty snapshot", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		environmentID := "duplicate-notify-problems-zero"
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
		group := newProblemOwnershipNodeGroup(t, ctx, database, "notify-problems-zero", 3)
		accountKey := "antigravity:problems-zero@example.invalid"
		for _, node := range group.nodes {
			group.finalize(t, ctx, database, node, []string{"problems-zero@example.invalid"}, 1, false)
		}
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		repository.SetNotificationDelivery(duplicateNotificationRegistry(t), false)
		created, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil || created == nil || !created.Created {
			t.Fatalf("create result=%+v err=%v", created, err)
		}
		for _, node := range group.nodes {
			group.finalize(t, ctx, database, node, nil, 0, false)
		}
		resolved, err := repository.Evaluate(ctx, environmentID, accountKey)
		if err != nil || resolved == nil || resolved.Status != "RESOLVED" || len(resolved.AffectedNodes) != 0 {
			t.Fatalf("zero resolve result=%+v err=%v", resolved, err)
		}
		payloads := readDuplicateNotificationPayloads(t, ctx, database)
		resolvedPayload := payloads["dingtalk:duplicate:"+created.OccurrenceID.String()+":resolved"]
		if resolvedPayload.InstanceIDs == nil || resolvedPayload.NodeNames == nil || len(resolvedPayload.InstanceIDs) != 0 || len(resolvedPayload.NodeNames) != 0 {
			t.Fatalf("zero resolve payload arrays ids=%v names=%v", resolvedPayload.InstanceIDs, resolvedPayload.NodeNames)
		}
		if jobs, events, outbox := durableNotificationCounts(t, ctx, database); jobs != 2 || events != 2 || outbox != 2 {
			t.Fatalf("zero resolve durable counts=%d/%d/%d, want 2/2/2", jobs, events, outbox)
		}
	})
}
