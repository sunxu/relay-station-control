package inventorypoll

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

type lifecycleCanaryEventObserver struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (observer *lifecycleCanaryEventObserver) Observe(_ context.Context, event Event) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	_, _ = fmt.Fprintf(&observer.buffer, "component=%s action=%s result=%s reason=%s state=%s attempt=%s\n",
		event.Component, event.Action, event.Result, event.Reason, event.State, event.AttemptBucket)
}

func (observer *lifecycleCanaryEventObserver) Bytes() []byte {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]byte(nil), observer.buffer.Bytes()...)
}

func TestAccountInventoryLifecycleCanariesTraversePolicyRaceRollbackArtifacts(t *testing.T) {
	artifactRoot := os.Getenv("CONTROL_LIFECYCLE_CANARY_ARTIFACT_DIR")
	if artifactRoot == "" {
		t.Skip("lifecycle canary artifact acceptance is opt-in")
	}
	pollID, err := uuid.Parse(os.Getenv("CONTROL_LIFECYCLE_CANARY_POLL_ID"))
	if err != nil {
		t.Fatal("poll canary invalid")
	}
	policyID, err := uuid.Parse(os.Getenv("CONTROL_LIFECYCLE_CANARY_POLICY_ID"))
	if err != nil {
		t.Fatal("policy canary invalid")
	}
	sqlParameter := os.Getenv("CONTROL_LIFECYCLE_CANARY_SQL_PARAMETER")
	rawError := os.Getenv("CONTROL_LIFECYCLE_CANARY_RAW_ERROR")
	endpoint := os.Getenv("CONTROL_LIFECYCLE_CANARY_ENDPOINT")
	secretReference := os.Getenv("CONTROL_LIFECYCLE_CANARY_SECRET_REFERENCE")
	email := os.Getenv("CONTROL_LIFECYCLE_CANARY_EMAIL")
	accountKey := os.Getenv("CONTROL_LIFECYCLE_CANARY_ACCOUNT_KEY")
	version := os.Getenv("CONTROL_LIFECYCLE_CANARY_VERSION")
	commit := os.Getenv("CONTROL_LIFECYCLE_CANARY_COMMIT")
	if sqlParameter == "" || rawError == "" || endpoint == "" || secretReference == "" ||
		email == "" || accountKey == "" || version == "" || commit == "" {
		t.Fatal("policy-race/rollback canary configuration incomplete")
	}

	for _, scenario := range []struct {
		name        string
		finalizeErr error
		wantResult  EventResult
		wantReason  ControlReason
	}{
		{name: "policy-race", finalizeErr: ErrLostLease, wantResult: EventResultSkipped, wantReason: ControlReasonLostLease},
		{name: "rollback", finalizeErr: errors.New(sqlParameter), wantResult: EventResultFailure, wantReason: ControlReasonDatabaseUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := filepath.Join(artifactRoot, scenario.name)
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal("scenario artifact directory unavailable")
			}
			instanceID := uuid.New()
			repository := &fakeRepository{claims: []ClaimedRun{{
				PollRunID: pollID, InstanceID: instanceID, PolicyVersionID: policyID,
				ScheduledAt: time.Unix(1800, 0).UTC(), Attempt: 1, MaxAttempts: 2,
				GraceRemaining: 5 * time.Second,
				Target: drivers.NodeTarget{
					InstanceID: instanceID, NodeType: drivers.NodeTypeCLIProxyAPI,
					DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
					ManagementEndpoint:    "http://" + endpoint,
					ReaderSecretReference: drivers.NewSecretReference(secretReference),
					Capabilities:          []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead},
				},
				ProviderPolicy: drivers.ProviderPolicySnapshot{VersionID: policyID, ActiveProviders: []string{"openai"}},
			}}, finalizeErr: scenario.finalizeErr}
			driver := &fakeDriver{invoke: func(context.Context, drivers.InventoryRequest) (drivers.InventoryObservation, error) {
				return drivers.InventoryObservation{
					TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
					Mode: drivers.InventoryModeRuntime, Version: version, Commit: commit,
					NodeIdentityComplete: true, Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
					Providers: []drivers.ProviderObservation{{
						Provider: "openai", SnapshotComplete: true,
						Accounts: []drivers.AccountObservation{{
							Provider: "openai", Email: email, State: drivers.AccountStateActive, OccurrenceCount: 1,
						}},
					}},
				}, errors.New(rawError)
			}}
			observer := &lifecycleCanaryEventObserver{}
			configuration := smallTestConfig()
			configuration.Observer = observer
			worker, err := NewWorker(repository, driver, configuration)
			if err != nil {
				t.Fatal("canary Worker construction failed")
			}
			workerContext, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- worker.Run(workerContext) }()
			waitFor(t, 2*time.Second, func() bool {
				_, _, finalizes, _ := repository.counts()
				return finalizes == 1
			})
			cancel()
			if err := <-done; err != nil {
				t.Fatal("canary Worker shutdown failed")
			}
			if driver.calls.Load() != 1 {
				t.Fatal("policy-race/rollback request count invalid")
			}
			logs := observer.Bytes()
			if !bytes.Contains(logs, []byte("result="+string(scenario.wantResult))) ||
				!bytes.Contains(logs, []byte("reason="+string(scenario.wantReason))) {
				t.Fatal("policy-race/rollback did not emit the fixed observer classification")
			}
			for _, sensitive := range []string{sqlParameter, rawError, endpoint, secretReference, email, accountKey,
				pollID.String(), policyID.String(), version, commit} {
				if strings.Contains(string(logs), sensitive) {
					t.Fatal("policy-race/rollback observer leaked a canary")
				}
			}
			if err := os.WriteFile(filepath.Join(directory, "log.log"), logs, 0o600); err != nil {
				t.Fatal("scenario log artifact unavailable")
			}
			fixed := []byte("worker_finalize=" + string(scenario.wantResult) + " reason=" + string(scenario.wantReason) + " request_count=1\n")
			if err := os.WriteFile(filepath.Join(directory, "test.log"), fixed, 0o600); err != nil {
				t.Fatal("scenario test artifact unavailable")
			}
		})
	}
}
