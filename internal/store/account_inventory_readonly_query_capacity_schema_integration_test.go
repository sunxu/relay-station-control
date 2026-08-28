package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	readonlyQueryCapacityAccounts = 1000
	readonlyQueryCapacityActors   = 8
	readonlyQueryCapacitySamples  = 60
)

type readonlyQueryCapacityMatrix struct {
	database        *isolatedJobDatabase
	repository      *productstore.AccountInventoryRepository
	runtime         *pgxpool.Pool
	instanceIDs     []uuid.UUID
	actorIDs        []uuid.UUID
	accountsPerNode int
}

type readonlyQueryCapacityScenario struct {
	name  string
	query func(sample int) productstore.AccountInventoryQuery
	want  func(productstore.AccountInventoryQuery) int
}

type readonlyQueryCapacityExplain struct {
	Plan struct {
		SharedHitBlocks     int64 `json:"Shared Hit Blocks"`
		SharedReadBlocks    int64 `json:"Shared Read Blocks"`
		SharedDirtiedBlocks int64 `json:"Shared Dirtied Blocks"`
		SharedWrittenBlocks int64 `json:"Shared Written Blocks"`
		TempReadBlocks      int64 `json:"Temp Read Blocks"`
		TempWrittenBlocks   int64 `json:"Temp Written Blocks"`
	} `json:"Plan"`
	PlanningTime  float64 `json:"Planning Time"`
	ExecutionTime float64 `json:"Execution Time"`
}

func TestAccountInventoryReadonlyQueryCapacityOneTenFifty(t *testing.T) {
	if os.Getenv("CONTROL_READONLY_QUERY_CAPACITY_ACCEPTANCE") != "1" {
		t.Skip("readonly query capacity acceptance is opt-in")
	}
	for _, nodeCount := range []int{1, 10, 50} {
		nodeCount := nodeCount
		t.Run(fmt.Sprintf("nodes_%d", nodeCount), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
			defer cancel()
			matrix := newReadonlyQueryCapacityMatrix(t, ctx, nodeCount)
			defer matrix.runtime.Close()

			scenarios := []readonlyQueryCapacityScenario{
				{name: "unfiltered", query: matrix.unfilteredQuery, want: matrix.fullPageCount},
				{name: "provider", query: matrix.providerQuery, want: matrix.fullPageCount},
				{name: "lifecycle", query: matrix.lifecycleQuery, want: matrix.fullPageCount},
				{name: "basic_status", query: matrix.basicStatusQuery, want: matrix.fullPageCount},
				{name: "exact_email", query: matrix.emailQuery, want: func(productstore.AccountInventoryQuery) int { return 1 }},
				{name: "combined", query: matrix.combinedQuery, want: func(productstore.AccountInventoryQuery) int { return 1 }},
				{name: "deep_page", query: matrix.deepPageQuery, want: matrix.deepPageCount},
			}
			allDurations := make([]time.Duration, 0, len(scenarios)*readonlyQueryCapacitySamples+80)
			var unfilteredDurations []time.Duration
			for _, current := range scenarios {
				for sample := 0; sample < readonlyQueryCapacitySamples; sample++ {
					query := current.query(sample)
					started := time.Now()
					page, err := matrix.repository.QueryPageAndAudit(
						ctx, query, matrix.audit(sample%len(matrix.actorIDs), current.name, sample),
					)
					duration := time.Since(started)
					if err != nil || len(page.Items) != current.want(query) {
						t.Fatal("readonly query capacity scenario failed")
					}
					allDurations = append(allDurations, duration)
					if current.name == "unfiltered" {
						unfilteredDurations = append(unfilteredDurations, duration)
					}
				}
			}

			directDurations := matrix.measureDirectBaseline(t, ctx, readonlyQueryCapacitySamples)
			maximumBuffers, maximumPlanMilliseconds := matrix.explainMatrix(t, ctx, scenarios)
			concurrentDurations, concurrentErrors := matrix.runConcurrentAdmins(ctx, 10)
			if concurrentErrors != 0 {
				t.Fatal("readonly query capacity concurrent administrator query failed")
			}
			allDurations = append(allDurations, concurrentDurations...)

			expectedAudits := len(scenarios)*readonlyQueryCapacitySamples + readonlyQueryCapacityActors*10
			var auditRows, auditActors int
			if err := matrix.database.owner.QueryRow(ctx, `SELECT count(*),count(DISTINCT actor_admin_id)
				FROM audit_logs WHERE category='account_inventory' AND action='account_inventory.view'`).
				Scan(&auditRows, &auditActors); err != nil || auditRows != expectedAudits || auditActors != readonlyQueryCapacityActors {
				t.Fatal("readonly query capacity audit accounting failed")
			}
			p50, p95, p99 := readonlyQueryCapacityPercentiles(allDurations)
			directP95 := readonlyQueryCapacityPercentile(directDurations, 95)
			unfilteredP95 := readonlyQueryCapacityPercentile(unfilteredDurations, 95)
			auditAdapterOverhead := unfilteredP95 - directP95
			if auditAdapterOverhead < 0 {
				auditAdapterOverhead = 0
			}
			if p99 >= 10*time.Second || maximumPlanMilliseconds >= 10_000 || maximumBuffers > 100_000 {
				t.Fatal("readonly query capacity bound exceeded")
			}
			t.Logf("readonly_query_capacity=passed nodes=%d accounts=1000 scenarios=7 samples=%d concurrent_admins=8 concurrent_queries=80 errors=0 p50_us=%d p95_us=%d p99_us=%d max_plan_us=%d max_buffers=%d audit_rows=%d audit_adapter_overhead_p95_us=%d",
				nodeCount, len(allDurations), p50.Microseconds(), p95.Microseconds(), p99.Microseconds(),
				int64(maximumPlanMilliseconds*1000), maximumBuffers, auditRows, auditAdapterOverhead.Microseconds())
		})
	}
}

func newReadonlyQueryCapacityMatrix(
	t *testing.T, ctx context.Context, nodeCount int,
) *readonlyQueryCapacityMatrix {
	t.Helper()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if readonlyQueryCapacityAccounts%nodeCount != 0 {
		t.Fatal("readonly query capacity matrix is not evenly divisible")
	}
	accountsPerNode := readonlyQueryCapacityAccounts / nodeCount
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(
		node_type,driver_contract_version,capability
	) VALUES($1,$2,'management_account_inventory_read')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal("readonly query capacity driver capability setup failed")
	}
	instanceIDs := make([]uuid.UUID, nodeCount)
	for node := 0; node < nodeCount; node++ {
		instanceID := fixture.instanceID
		if node > 0 {
			instanceID = uuid.New()
			if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
				instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref
			) VALUES($1,'Readonly Query Capacity Node',$2,$3,$4,'docker-secret://synthetic/readonly-query-capacity')`,
				instanceID, fixture.nodeType, fixture.contract,
				fmt.Sprintf("http://readonly-query-capacity-%02d.example.invalid", node)); err != nil {
				t.Fatal("readonly query capacity node setup failed")
			}
		}
		instanceIDs[node] = instanceID
		if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(
			instance_id,node_type,driver_contract_version,capability
		) VALUES($1,$2,$3,'management_account_inventory_read')`,
			instanceID, fixture.nodeType, fixture.contract); err != nil {
			t.Fatal("readonly query capacity node capability setup failed")
		}
		readonlyQueryCapacityFinalizeNode(t, ctx, database, fixture, instanceID, node, accountsPerNode)
	}
	actorIDs := make([]uuid.UUID, readonlyQueryCapacityActors)
	for index := range actorIDs {
		actorIDs[index] = uuid.New()
		if _, err := database.owner.Exec(ctx, `INSERT INTO control_admin_users(
			admin_id,login_name,display_name
		) VALUES($1,$2,'Readonly Query Capacity Operator')`, actorIDs[index],
			fmt.Sprintf("readonly_capacity_%02d_%s", index, assetFixtureSuffix(t))); err != nil {
			t.Fatal("readonly query capacity actor setup failed")
		}
	}
	runtimeConfig, err := pgxpool.ParseConfig(database.runtimeURL)
	if err != nil {
		t.Fatal("readonly query capacity runtime configuration failed")
	}
	runtimeConfig.MaxConns = 16
	runtimeConfig.MinConns = 0
	runtime, err := pgxpool.NewWithConfig(ctx, runtimeConfig)
	if err != nil {
		t.Fatal("readonly query capacity runtime pool failed")
	}
	repository, err := productstore.NewAccountInventoryRepository(runtime)
	if err != nil || repository.CheckCompatibility(ctx) != nil {
		runtime.Close()
		t.Fatal("readonly query capacity repository compatibility failed")
	}
	return &readonlyQueryCapacityMatrix{
		database: database, repository: repository, runtime: runtime,
		instanceIDs: instanceIDs, actorIDs: actorIDs, accountsPerNode: accountsPerNode,
	}
}

func readonlyQueryCapacityFinalizeNode(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, fixture *lifecycleSchemaFixture,
	instanceID uuid.UUID, node, accountsPerNode int,
) {
	t.Helper()
	pollID, fence := uuid.New(), uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, pollID, instanceID,
		fixture.nodeType, fixture.contract, fixture.baseSlot, fixture.policyID); err != nil {
		t.Fatal("readonly query capacity poll setup failed")
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),last_started_at=clock_timestamp(),
		lease_expires_at=clock_timestamp()+interval '60 seconds',lease_fencing_token=$2
		WHERE poll_run_id=$1`, pollID, fence); err != nil {
		t.Fatal("readonly query capacity poll arm failed")
	}
	providers, snapshots := readonlyQueryCapacityPayload(t, node, accountsPerNode)
	var finalized int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,true,true,true,'runtime',true,true,false,'success','none',
			$3,$3,0,0,0,'v1.0.0','abcdef1',$4::jsonb,$5::jsonb,'[]'::jsonb
		)`, pollID, fence, accountsPerNode, providers, snapshots).Scan(&finalized); err != nil || finalized != 1 {
		t.Fatal("readonly query capacity promotion failed")
	}
}

func readonlyQueryCapacityPayload(t *testing.T, node, accountsPerNode int) ([]byte, []byte) {
	t.Helper()
	providers, err := json.Marshal([]map[string]any{{
		"provider": fixtureProviderName, "identifiable_count": accountsPerNode,
		"missing_identity_count": 0, "duplicate_identity_count": 0,
		"identity_complete": true, "snapshot_complete": true, "degraded": false, "reason": "complete",
	}})
	if err != nil {
		t.Fatal("readonly query capacity Provider payload failed")
	}
	items := make([]map[string]any, 0, accountsPerNode)
	for account := 0; account < accountsPerNode; account++ {
		email := readonlyQueryCapacityEmail(node, account)
		items = append(items, map[string]any{
			"provider": fixtureProviderName, "account_key": fixtureProviderName + ":" + email,
			"email": email, "basic_status": "active", "success_count": int64(account),
			"failed_count": int64(0), "recent_request_count": int64(0),
			"last_refresh_unix": nil, "next_retry_unix": nil, "updated_at_unix": nil,
		})
	}
	snapshots, err := json.Marshal(items)
	if err != nil {
		t.Fatal("readonly query capacity snapshot payload failed")
	}
	return providers, snapshots
}

func readonlyQueryCapacityEmail(node, account int) string {
	return fmt.Sprintf("capacity-%02d-%04d@example.invalid", node, account)
}

func (matrix *readonlyQueryCapacityMatrix) instance(sample int) (int, uuid.UUID) {
	index := sample % len(matrix.instanceIDs)
	return index, matrix.instanceIDs[index]
}

func (matrix *readonlyQueryCapacityMatrix) unfilteredQuery(sample int) productstore.AccountInventoryQuery {
	_, instanceID := matrix.instance(sample)
	return productstore.AccountInventoryQuery{InstanceID: instanceID, Limit: 100}
}

func (matrix *readonlyQueryCapacityMatrix) providerQuery(sample int) productstore.AccountInventoryQuery {
	query := matrix.unfilteredQuery(sample)
	query.Filters.Provider = fixtureProviderName
	return query
}

func (matrix *readonlyQueryCapacityMatrix) lifecycleQuery(sample int) productstore.AccountInventoryQuery {
	query := matrix.unfilteredQuery(sample)
	query.Filters.Lifecycle = productstore.AccountInventoryPresent
	return query
}

func (matrix *readonlyQueryCapacityMatrix) basicStatusQuery(sample int) productstore.AccountInventoryQuery {
	query := matrix.unfilteredQuery(sample)
	query.Filters.BasicStatus = productstore.AccountInventoryBasicStatusReportedActive
	return query
}

func (matrix *readonlyQueryCapacityMatrix) emailQuery(sample int) productstore.AccountInventoryQuery {
	node, instanceID := matrix.instance(sample)
	query := productstore.AccountInventoryQuery{InstanceID: instanceID, Limit: 100}
	query.Filters.Email = readonlyQueryCapacityEmail(node, matrix.accountsPerNode/2)
	return query
}

func (matrix *readonlyQueryCapacityMatrix) combinedQuery(sample int) productstore.AccountInventoryQuery {
	query := matrix.emailQuery(sample)
	query.Filters.Provider = fixtureProviderName
	query.Filters.Lifecycle = productstore.AccountInventoryPresent
	query.Filters.BasicStatus = productstore.AccountInventoryBasicStatusReportedActive
	return query
}

func (matrix *readonlyQueryCapacityMatrix) deepPageQuery(sample int) productstore.AccountInventoryQuery {
	node, instanceID := matrix.instance(sample)
	position := matrix.accountsPerNode*8/10 - 1
	return productstore.AccountInventoryQuery{
		InstanceID: instanceID, Limit: 100,
		AfterAccountKey: fixtureProviderName + ":" + readonlyQueryCapacityEmail(node, position),
	}
}

func (matrix *readonlyQueryCapacityMatrix) fullPageCount(productstore.AccountInventoryQuery) int {
	if matrix.accountsPerNode > 100 {
		return 100
	}
	return matrix.accountsPerNode
}

func (matrix *readonlyQueryCapacityMatrix) deepPageCount(productstore.AccountInventoryQuery) int {
	position := matrix.accountsPerNode*8/10 - 1
	remaining := matrix.accountsPerNode - position - 1
	if remaining > 100 {
		return 100
	}
	return remaining
}

func (matrix *readonlyQueryCapacityMatrix) audit(actor int, scenario string, sample int) productstore.AccountInventoryViewAudit {
	fingerprint := sha256.Sum256([]byte(fmt.Sprintf("readonly-capacity-%d", actor)))
	return productstore.AccountInventoryViewAudit{
		ActorAdminID: matrix.actorIDs[actor], SourceFingerprint: fingerprint[:],
		RequestID: fmt.Sprintf("readonly-capacity-%s-%d", scenario, sample),
	}
}

func (matrix *readonlyQueryCapacityMatrix) measureDirectBaseline(
	t *testing.T, ctx context.Context, samples int,
) []time.Duration {
	t.Helper()
	durations := make([]time.Duration, 0, samples)
	for sample := 0; sample < samples; sample++ {
		_, instanceID := matrix.instance(sample)
		started := time.Now()
		var rows int
		if err := matrix.runtime.QueryRow(ctx, `SELECT count(*) FROM public.control_query_current_account_inventory_v1(
			$1,'','','','','',101)`, instanceID).Scan(&rows); err != nil || rows != min(matrix.accountsPerNode, 101) {
			t.Fatal("readonly query capacity direct baseline failed")
		}
		durations = append(durations, time.Since(started))
	}
	return durations
}

func (matrix *readonlyQueryCapacityMatrix) explainMatrix(
	t *testing.T, ctx context.Context, scenarios []readonlyQueryCapacityScenario,
) (int64, float64) {
	t.Helper()
	var maximumBuffers int64
	var maximumMilliseconds float64
	for index, current := range scenarios {
		query := current.query(index)
		var encoded []byte
		if err := matrix.database.owner.QueryRow(ctx, `EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON)
			SELECT * FROM public.control_query_current_account_inventory_v1($1,$2,$3,$4,$5,$6,$7)`,
			query.InstanceID, query.Filters.Provider, string(query.Filters.Lifecycle),
			string(query.Filters.BasicStatus), query.Filters.Email, query.AfterAccountKey, query.Limit+1).
			Scan(&encoded); err != nil {
			t.Fatal("readonly query capacity explain failed")
		}
		var documents []readonlyQueryCapacityExplain
		if err := json.Unmarshal(encoded, &documents); err != nil || len(documents) != 1 {
			t.Fatal("readonly query capacity explain decode failed")
		}
		plan := documents[0]
		buffers := plan.Plan.SharedHitBlocks + plan.Plan.SharedReadBlocks + plan.Plan.SharedDirtiedBlocks +
			plan.Plan.SharedWrittenBlocks + plan.Plan.TempReadBlocks + plan.Plan.TempWrittenBlocks
		if buffers > maximumBuffers {
			maximumBuffers = buffers
		}
		milliseconds := plan.PlanningTime + plan.ExecutionTime
		if milliseconds > maximumMilliseconds {
			maximumMilliseconds = milliseconds
		}
	}
	return maximumBuffers, maximumMilliseconds
}

func (matrix *readonlyQueryCapacityMatrix) runConcurrentAdmins(
	ctx context.Context, queriesPerActor int,
) ([]time.Duration, int64) {
	var workers sync.WaitGroup
	var errorsSeen atomic.Int64
	durations := make(chan time.Duration, readonlyQueryCapacityActors*queriesPerActor)
	for actor := 0; actor < readonlyQueryCapacityActors; actor++ {
		actor := actor
		workers.Add(1)
		go func() {
			defer workers.Done()
			for queryIndex := 0; queryIndex < queriesPerActor; queryIndex++ {
				query := matrix.combinedQuery(actor*queriesPerActor + queryIndex)
				started := time.Now()
				page, err := matrix.repository.QueryPageAndAudit(
					ctx, query, matrix.audit(actor, "concurrent", actor*queriesPerActor+queryIndex),
				)
				if err != nil || len(page.Items) != 1 {
					errorsSeen.Add(1)
					continue
				}
				durations <- time.Since(started)
			}
		}()
	}
	workers.Wait()
	close(durations)
	result := make([]time.Duration, 0, readonlyQueryCapacityActors*queriesPerActor)
	for duration := range durations {
		result = append(result, duration)
	}
	return result, errorsSeen.Load()
}

func readonlyQueryCapacityPercentiles(values []time.Duration) (time.Duration, time.Duration, time.Duration) {
	return readonlyQueryCapacityPercentile(values, 50),
		readonlyQueryCapacityPercentile(values, 95),
		readonlyQueryCapacityPercentile(values, 99)
}

func readonlyQueryCapacityPercentile(values []time.Duration, percentile int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	index := (len(ordered)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	return ordered[index-1]
}
