package store_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

type readonlyQueryFixture struct {
	database   *isolatedJobDatabase
	lifecycle  *lifecycleSchemaFixture
	repository *productstore.AccountInventoryRepository
	audit      productstore.AccountInventoryViewAudit
}

func newReadonlyQueryFixture(t *testing.T, ctx context.Context, accounts []lifecycleAccount) *readonlyQueryFixture {
	t.Helper()
	database := newIsolatedJobDatabase(t)
	lifecycle := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(
		node_type,driver_contract_version,capability
	) VALUES ($1,$2,'management_account_inventory_read')`, lifecycle.nodeType, lifecycle.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(
		instance_id,node_type,driver_contract_version,capability
	) VALUES ($1,$2,$3,'management_account_inventory_read')`,
		lifecycle.instanceID, lifecycle.nodeType, lifecycle.contract); err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(
		admin_id,login_name,display_name
	) VALUES ($1,$2,'Readonly Transaction Operator')`,
		actorID, "readonly_tx_"+assetFixtureSuffix(t)); err != nil {
		t.Fatal(err)
	}
	if accounts != nil {
		lifecycle.finalize(t, ctx, database, accounts)
	}
	repository, err := productstore.NewAccountInventoryRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CheckCompatibility(ctx); err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256([]byte("readonly-query-transaction-integration"))
	return &readonlyQueryFixture{
		database: database, lifecycle: lifecycle, repository: repository,
		audit: productstore.AccountInventoryViewAudit{
			ActorAdminID: actorID, SourceFingerprint: fingerprint[:],
			RequestID: "readonly-query-transaction-test",
		},
	}
}

func (fixture *readonlyQueryFixture) query(
	t *testing.T, ctx context.Context, lifecycle productstore.AccountInventoryLifecycle,
) productstore.AccountInventoryPage {
	t.Helper()
	page, err := fixture.repository.QueryPageAndAudit(ctx, productstore.AccountInventoryQuery{
		InstanceID: fixture.lifecycle.instanceID, Limit: 100,
		Filters: productstore.AccountInventoryQueryFilters{Lifecycle: lifecycle},
	}, fixture.audit)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func requireReadonlyQueryPageLifecycle(
	t *testing.T, page productstore.AccountInventoryPage, count int,
	lifecycle productstore.AccountInventoryLifecycle,
) {
	t.Helper()
	if len(page.Items) != count {
		t.Fatalf("page item count=%d, want %d", len(page.Items), count)
	}
	for _, item := range page.Items {
		if item.Lifecycle != lifecycle || item.InstanceID == uuid.Nil || item.Email == "" {
			t.Fatal("query returned a partial or default-filled current-state item")
		}
	}
}

func TestAccountInventoryReadonlyQuerySeesOnlyCommittedPromotionsScopeAndRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{
		email: "committed-a@example.invalid", successCount: 1,
	}})
	baseSlot := fixture.lifecycle.baseSlot
	nextSlot := fixture.lifecycle.nextPoll

	fullPayload, err := makeLifecycleFinalizePayload([]lifecycleAccount{
		{email: "committed-a@example.invalid", successCount: 2},
		{email: "committed-b@example.invalid", successCount: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	fullPoll := prepareLifecyclePoll(t, ctx, fixture.database, fixture.lifecycle,
		baseSlot.Add(time.Duration(nextSlot)*5*time.Minute))
	fullTx, err := fixture.database.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finalized, err := finalizeLifecyclePoll(ctx, fullTx, fullPoll, fullPayload)
	if err != nil || finalized != 1 {
		_ = fullTx.Rollback(ctx)
		t.Fatalf("stage full promotion: finalized=%d err=%v", finalized, err)
	}
	queryContext, queryCancel := context.WithTimeout(ctx, 2*time.Second)
	requireReadonlyQueryPageLifecycle(t, fixture.query(t, queryContext, productstore.AccountInventoryPresent), 1,
		productstore.AccountInventoryPresent)
	queryCancel()
	commitStarted := time.Now()
	if err := fullTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(commitStarted) > time.Second {
		t.Fatal("query transaction blocked full promotion commit")
	}
	requireReadonlyQueryPageLifecycle(t, fixture.query(t, ctx, productstore.AccountInventoryPresent), 2,
		productstore.AccountInventoryPresent)

	emptyPayload, err := makeLifecycleFinalizePayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	emptyPoll := prepareLifecyclePoll(t, ctx, fixture.database, fixture.lifecycle,
		baseSlot.Add(time.Duration(nextSlot+1)*5*time.Minute))
	emptyTx, err := fixture.database.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finalized, err = finalizeLifecyclePoll(ctx, emptyTx, emptyPoll, emptyPayload)
	if err != nil || finalized != 1 {
		_ = emptyTx.Rollback(ctx)
		t.Fatalf("stage empty promotion: finalized=%d err=%v", finalized, err)
	}
	requireReadonlyQueryPageLifecycle(t, fixture.query(t, ctx, productstore.AccountInventoryPresent), 2,
		productstore.AccountInventoryPresent)
	if err := emptyTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	requireReadonlyQueryPageLifecycle(t, fixture.query(t, ctx, productstore.AccountInventorySuspectedMissing), 2,
		productstore.AccountInventorySuspectedMissing)

	rollbackPoll := prepareLifecyclePoll(t, ctx, fixture.database, fixture.lifecycle,
		baseSlot.Add(time.Duration(nextSlot+2)*5*time.Minute))
	rollbackTx, err := fixture.database.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finalized, err = finalizeLifecyclePoll(ctx, rollbackTx, rollbackPoll, fullPayload)
	if err != nil || finalized != 1 {
		_ = rollbackTx.Rollback(ctx)
		t.Fatalf("stage rollback promotion: finalized=%d err=%v", finalized, err)
	}
	requireReadonlyQueryPageLifecycle(t, fixture.query(t, ctx, productstore.AccountInventorySuspectedMissing), 2,
		productstore.AccountInventorySuspectedMissing)
	if err := rollbackTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	requireReadonlyQueryPageLifecycle(t, fixture.query(t, ctx, productstore.AccountInventorySuspectedMissing), 2,
		productstore.AccountInventorySuspectedMissing)

	scopeTx, err := fixture.database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var activationID uuid.UUID
	err = scopeTx.QueryRow(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,ARRAY['legacy'],ARRAY['openai'],
		'readonly-query-test','readonly query committed scope transition',NULL
	)`, fixture.lifecycle.nodeType, fixture.lifecycle.contract).Scan(&activationID)
	if err != nil || activationID == uuid.Nil {
		_ = scopeTx.Rollback(ctx)
		t.Fatalf("stage Provider scope transition: %v", err)
	}
	requireReadonlyQueryPageLifecycle(t, fixture.query(t, ctx, productstore.AccountInventorySuspectedMissing), 2,
		productstore.AccountInventorySuspectedMissing)
	if err := scopeTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	requireReadonlyQueryPageLifecycle(t, fixture.query(t, ctx, productstore.AccountInventoryOutOfScope), 2,
		productstore.AccountInventoryOutOfScope)

	var partialRows int
	if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory
		WHERE instance_id=$1 AND (
			(lifecycle='present' AND consecutive_missing_count<>0)
			OR (lifecycle='suspected_missing' AND consecutive_missing_count<>1)
			OR (lifecycle='out_of_scope' AND out_of_scope_since IS NULL)
		)`, fixture.lifecycle.instanceID).Scan(&partialRows); err != nil || partialRows != 0 {
		t.Fatalf("partial current-state rows=%d err=%v", partialRows, err)
	}
}

func TestAccountInventoryReadonlyQueryAuditCommitAndDisconnectSemantics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{
		{email: "page-a@example.invalid", successCount: 1},
		{email: "page-b@example.invalid", successCount: 2},
		{email: "page-c@example.invalid", successCount: 3},
	})
	query := productstore.AccountInventoryQuery{InstanceID: fixture.lifecycle.instanceID, Limit: 1}
	pages := 0
	for {
		page, err := fixture.repository.QueryPageAndAudit(ctx, query, fixture.audit)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if !page.HasMore {
			break
		}
		if page.ContinuationAccountKey == "" {
			t.Fatal("subsequent page omitted its internal continuation key")
		}
		query.AfterAccountKey = page.ContinuationAccountKey
	}
	if pages != 3 {
		t.Fatalf("page count=%d, want 3", pages)
	}
	emptyQuery := productstore.AccountInventoryQuery{
		InstanceID: fixture.lifecycle.instanceID, AfterAccountKey: "openai:zzzz@example.invalid", Limit: 1,
	}
	empty, err := fixture.repository.QueryPageAndAudit(ctx, emptyQuery, fixture.audit)
	if err != nil || len(empty.Items) != 0 || empty.HasMore {
		t.Fatalf("empty page=%+v err=%v", empty, err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		if _, err := fixture.repository.QueryPageAndAudit(ctx, emptyQuery, fixture.audit); err != nil {
			t.Fatal(err)
		}
	}
	var committedBeforeFailure int
	if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE request_id=$1 AND action='account_inventory.view'`, fixture.audit.RequestID).
		Scan(&committedBeforeFailure); err != nil || committedBeforeFailure != pages+3 {
		t.Fatalf("committed page audits=%d err=%v", committedBeforeFailure, err)
	}

	if _, err := fixture.database.owner.Exec(ctx, `CREATE FUNCTION public.control_test_reject_view_audit_commit()
		RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
		BEGIN
			IF NEW.action='account_inventory.view' THEN
				RAISE EXCEPTION 'injected deferred view audit failure' USING ERRCODE='P0503';
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE CONSTRAINT TRIGGER zz_control_test_reject_view_audit_commit
		AFTER INSERT ON audit_logs DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW EXECUTE FUNCTION public.control_test_reject_view_audit_commit()`); err != nil {
		t.Fatal(err)
	}
	failedPage, err := fixture.repository.QueryPageAndAudit(ctx, emptyQuery, fixture.audit)
	if err == nil || len(failedPage.Items) != 0 || failedPage.HasMore || failedPage.ContinuationAccountKey != "" {
		t.Fatal("deferred audit commit failure returned a page")
	}
	var committedAfterFailure int
	if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE request_id=$1 AND action='account_inventory.view'`, fixture.audit.RequestID).
		Scan(&committedAfterFailure); err != nil || committedAfterFailure != committedBeforeFailure {
		t.Fatalf("failed commit audit count=%d err=%v", committedAfterFailure, err)
	}
	if _, err := fixture.database.owner.Exec(ctx, `DROP TRIGGER zz_control_test_reject_view_audit_commit ON audit_logs;
		DROP FUNCTION public.control_test_reject_view_audit_commit()`); err != nil {
		t.Fatal(err)
	}

	connection, err := fixture.database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	var resultCount int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM public.control_query_current_account_inventory_v1(
		$1,'','','','','',2)`, fixture.lifecycle.instanceID).Scan(&resultCount); err != nil || resultCount != 2 {
		_, _ = connection.Exec(ctx, `ROLLBACK`)
		t.Fatalf("stage disconnected query count=%d err=%v", resultCount, err)
	}
	disconnectRequestID := "readonly-query-commit-disconnect"
	_, err = connection.Exec(ctx, `INSERT INTO audit_logs(
		audit_id,category,action,result,actor_admin_id,source_fingerprint,request_id,details
	) VALUES ($1,'account_inventory','account_inventory.view','success',$2,$3,$4,
		jsonb_build_object('instance_id',$5::text,'provider_filter_used',false,
		'lifecycle_filter_used',false,'basic_status_filter_used',false,
		'email_filter_used',false,'cursor_used',false,'result_count',$6::int))`,
		uuid.New(), fixture.audit.ActorAdminID, fixture.audit.SourceFingerprint,
		disconnectRequestID, fixture.lifecycle.instanceID, resultCount)
	if err != nil {
		_, _ = connection.Exec(ctx, `ROLLBACK`)
		t.Fatal(err)
	}
	disconnectContext, disconnectCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, disconnectErr := connection.Exec(disconnectContext, `COMMIT; SELECT pg_sleep(30)`)
	disconnectCancel()
	if disconnectErr == nil {
		t.Fatal("post-commit client cancellation unexpectedly returned success")
	}
	var durableAfterDisconnect int
	if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE request_id=$1 AND action='account_inventory.view'`, disconnectRequestID).
		Scan(&durableAfterDisconnect); err != nil || durableAfterDisconnect != 1 {
		t.Fatalf("post-commit disconnect audit count=%d err=%v", durableAfterDisconnect, err)
	}
}

func TestAccountInventoryReadonlyQueryDatabaseFaultsFailClosedAndRecover(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{
		email: "recovery@example.invalid", successCount: 1,
	}})
	poolConfig, err := pgxpool.ParseConfig(fixture.database.runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MaxConns = 1
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "readonly_query_fault_test"
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = "100ms"
	faultPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer faultPool.Close()
	repository, err := productstore.NewAccountInventoryRepository(faultPool)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CheckCompatibility(ctx); err != nil {
		t.Fatal(err)
	}
	query := productstore.AccountInventoryQuery{InstanceID: fixture.lifecycle.instanceID, Limit: 10}
	audit := fixture.audit
	audit.RequestID = "readonly-query-database-fault"

	lock, err := fixture.database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `LOCK TABLE account_inventory IN ACCESS EXCLUSIVE MODE`); err != nil {
		_ = lock.Rollback(ctx)
		t.Fatal(err)
	}
	timedOutPage, timedOutErr := repository.QueryPageAndAudit(ctx, query, audit)
	if timedOutErr == nil || len(timedOutPage.Items) != 0 {
		_ = lock.Rollback(ctx)
		t.Fatal("statement timeout returned account inventory")
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.QueryPageAndAudit(ctx, query, audit)
	if err != nil || len(recovered.Items) != 1 {
		t.Fatalf("query did not recover after statement timeout: page=%+v err=%v", recovered, err)
	}

	held, err := faultPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exhaustedContext, exhaustedCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	exhaustedPage, exhaustedErr := repository.QueryPageAndAudit(exhaustedContext, query, audit)
	exhaustedCancel()
	if exhaustedErr == nil || len(exhaustedPage.Items) != 0 {
		held.Release()
		t.Fatal("connection exhaustion returned account inventory")
	}
	held.Release()
	recovered, err = repository.QueryPageAndAudit(ctx, query, audit)
	if err != nil || len(recovered.Items) != 1 {
		t.Fatalf("query did not recover after pool exhaustion: page=%+v err=%v", recovered, err)
	}

	lock, err = fixture.database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `LOCK TABLE account_inventory IN ACCESS EXCLUSIVE MODE`); err != nil {
		_ = lock.Rollback(ctx)
		t.Fatal(err)
	}
	result := make(chan struct {
		page productstore.AccountInventoryPage
		err  error
	}, 1)
	go func() {
		page, queryErr := repository.QueryPageAndAudit(ctx, query, audit)
		result <- struct {
			page productstore.AccountInventoryPage
			err  error
		}{page: page, err: queryErr}
	}()
	waitContext, waitCancel := context.WithTimeout(ctx, 2*time.Second)
	defer waitCancel()
	if err := waitForLifecycleLock(waitContext, fixture.database, "readonly_query_fault_test"); err != nil {
		_ = lock.Rollback(ctx)
		t.Fatal(err)
	}
	var terminated bool
	if err := fixture.database.owner.QueryRow(ctx, `SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity WHERE datname=current_database()
		AND application_name='readonly_query_fault_test' AND wait_event_type='Lock'
		LIMIT 1`).Scan(&terminated); err != nil || !terminated {
		_ = lock.Rollback(ctx)
		t.Fatalf("terminate blocked query backend: terminated=%v err=%v", terminated, err)
	}
	terminatedResult := <-result
	if terminatedResult.err == nil || len(terminatedResult.page.Items) != 0 {
		_ = lock.Rollback(ctx)
		t.Fatal("terminated database connection returned account inventory")
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err = repository.QueryPageAndAudit(ctx, query, audit)
	if err != nil || len(recovered.Items) != 1 {
		t.Fatalf("query did not recover after backend termination: page=%+v err=%v", recovered, err)
	}

	var successfulAudits int
	if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE request_id=$1 AND action='account_inventory.view'`, audit.RequestID).
		Scan(&successfulAudits); err != nil || successfulAudits != 3 {
		t.Fatalf("successful recovery audits=%d err=%v", successfulAudits, err)
	}
	var failedAuditRows int
	if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE request_id=$1 AND action='account_inventory.view' AND result<>'success'`, audit.RequestID).
		Scan(&failedAuditRows); err != nil || failedAuditRows != 0 {
		t.Fatalf("failed fault-path audits=%d err=%v", failedAuditRows, err)
	}
}

func TestAccountInventoryReadonlyQueryTransactionDTOsRemainRedacted(t *testing.T) {
	page := productstore.AccountInventoryPage{Items: []productstore.AccountInventoryItem{{
		Email: "transaction-canary@example.invalid",
	}}}
	if text := fmt.Sprintf("%+v", page); text != "[REDACTED AccountInventoryPage]" {
		t.Fatal("transaction test page formatter did not remain redacted")
	}
}
