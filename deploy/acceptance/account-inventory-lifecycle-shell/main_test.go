package lifecycleacceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func acceptanceRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
}

func runScript(t *testing.T, arguments ...string) (string, error) {
	t.Helper()
	command := exec.Command("bash", append([]string{"account-inventory-lifecycle-run.sh"}, arguments...)...)
	command.Dir = acceptanceRoot(t)
	environment := make([]string, 0, len(os.Environ())+6)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "CONTROL_LIFECYCLE_CANARY_") {
			environment = append(environment, item)
		}
	}
	command.Env = append(environment,
		"HTTP_PROXY=identity-sensitive-proxy-canary",
		"HTTPS_PROXY=identity-sensitive-proxy-canary",
		"ALL_PROXY=identity-sensitive-proxy-canary",
		"http_proxy=identity-sensitive-proxy-canary",
		"https_proxy=identity-sensitive-proxy-canary",
		"all_proxy=identity-sensitive-proxy-canary",
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestLifecycleRunnerFailsClosedForRealNodeWithoutRequest(t *testing.T) {
	output, err := runScript(t, "real-node")
	if err == nil {
		t.Fatal("real-node mode unexpectedly succeeded")
	}
	expected := "account_inventory_lifecycle_acceptance=failed reason=real_node_not_approved request_count=0\n"
	if output != expected {
		t.Fatalf("real-node output outside fixed schema: %q", output)
	}
}

func TestLifecycleRunnerRejectsIncompleteCanaryConfiguration(t *testing.T) {
	output, err := runScript(t, "scan")
	if err == nil {
		t.Fatal("scan mode unexpectedly succeeded")
	}
	if output != "account_inventory_lifecycle_acceptance=failed reason=invalid_canary_configuration\n" {
		t.Fatalf("scan output outside fixed schema: %q", output)
	}
}

func TestLifecycleRunnerScansSyntheticArtifactWithoutExternalConfiguration(t *testing.T) {
	output, err := runScript(t, "scan-smoke")
	if err != nil {
		t.Fatalf("synthetic scanner smoke failed: %v: %s", err, output)
	}
	if output != "account_inventory_lifecycle_canary_scan=success\n" {
		t.Fatalf("synthetic scanner output outside fixed schema: %q", output)
	}
}

func TestLifecycleRunnerScansComponentInputScenarioMatrix(t *testing.T) {
	output, err := runScript(t, "scan-matrix")
	if err != nil {
		t.Fatalf("component-input scenario scanner failed: %v: %s", err, output)
	}
	if output != "account_inventory_lifecycle_canary_scan=success\n" {
		t.Fatalf("component-input scenario scanner output outside fixed schema: %q", output)
	}
}

func TestLifecyclePostgresSeparatesCoreAndCapacityFailures(t *testing.T) {
	path := filepath.Join(acceptanceRoot(t), "account-inventory-lifecycle-postgres.sh")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(encoded)
	for _, required := range []string{
		"CONTROL_LIFECYCLE_CAPACITY_ACCEPTANCE=0",
		"CONTROL_LIFECYCLE_CAPACITY_ACCEPTANCE=1",
		"lifecycle_store_core_gate_failed",
		"lifecycle_capacity_gate_failed",
		"^TestAccountInventoryLifecycleCapacityOneTenFifty$",
	} {
		if !strings.Contains(contents, required) {
			t.Fatalf("PostgreSQL acceptance does not isolate %q", required)
		}
	}
}

func TestProviderPolicyTemplateGuardsMutationBeforeTransaction(t *testing.T) {
	path := filepath.Clean(filepath.Join(acceptanceRoot(t), "..", "asset-registry", "activate-provider-policy.sql"))
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(encoded)
	guard := strings.Index(contents, `\getenv policy_mutation_enabled CONTROL_PROVIDER_POLICY_MUTATION_ENABLED`)
	transaction := strings.Index(contents, "BEGIN ISOLATION LEVEL SERIALIZABLE")
	lifecycleCall := strings.Index(contents, "control_activate_provider_policy_with_lifecycle")
	reasonArgument := strings.Index(contents, `:'reason'`)
	legacyCall := strings.Index(contents, "control_activate_provider_policy(")
	if guard < 0 || transaction < 0 || lifecycleCall < 0 || reasonArgument < 0 ||
		guard > transaction || reasonArgument > lifecycleCall || legacyCall >= 0 {
		t.Fatal("Provider policy template does not fail closed before the lifecycle-aware mutation")
	}
}

func TestLifecycleRollbackPinsOldBinaryAndFreezesBothMutationPaths(t *testing.T) {
	path := filepath.Join(acceptanceRoot(t), "account-inventory-lifecycle-rollback.sh")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(encoded)
	for _, required := range []string{
		"e482d8eb19896a60b73a4144ee155d1f66a2b1d7",
		"git archive --format=tar",
		"CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED=false",
		"CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=false",
		"lifecycle_fingerprint",
		`start_control "$old_binary" false`,
		`start_control "$new_binary" true`,
		`"$harness" replay`,
		"fixture_prepare_timeout",
		"minimum_forward_schema=7",
		`[ "$schema_version" -lt "$minimum_forward_schema" ]`,
		"GOOSE_DBSTRING=\"$CONTROL_LIFECYCLE_ROLLBACK_OWNER_URL\"",
		"verify_old_readonly_route_closed",
		`--request POST`,
		`/api/account-inventory/query`,
		`[ "$status" != 404 ]`,
		"old_readonly_route_identity_leaked",
		`"$harness" verify-frozen >"$runtime_directory/old-audit-retained.log"`,
		"old_readonly_route_requests=1",
		"old_readonly_route_closed=true",
		"old_readonly_route_identity_occurrences=0",
		"readonly_view_audits_retained=1",
	} {
		if !strings.Contains(contents, required) {
			t.Fatalf("rollback acceptance missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"git checkout", "git switch", "git worktree",
		`go tool goose -dir ../migrations postgres "$CONTROL_LIFECYCLE_ROLLBACK_OWNER_URL"`,
	} {
		if strings.Contains(contents, forbidden) {
			t.Fatalf("rollback acceptance mutates the primary worktree with %q", forbidden)
		}
	}
	if strings.Contains(contents, `[ "$schema_version" != 7 ]`) {
		t.Fatal("rollback acceptance rejects additive schema versions newer than lifecycle foundation")
	}
	for _, forbidden := range []string{
		`cat "$runtime_directory/old-readonly-response.txt"`,
		`echo "$status"`,
	} {
		if strings.Contains(contents, forbidden) {
			t.Fatalf("rollback acceptance exposes bounded route probe output with %q", forbidden)
		}
	}

	harnessPath := filepath.Join(acceptanceRoot(t), "account-inventory-lifecycle-rollback", "main.go")
	harnessEncoded, err := os.ReadFile(harnessPath)
	if err != nil {
		t.Fatal(err)
	}
	harnessContents := string(harnessEncoded)
	for _, required := range []string{
		"fixtureAuditID",
		"fixtureAuditActor",
		"INSERT INTO audit_logs",
		"'account_inventory.view'",
		"'rollback-readonly-view'",
		"viewAuditRows != 1",
	} {
		if !strings.Contains(harnessContents, required) {
			t.Fatalf("rollback harness lacks bounded readonly audit retention contract %q", required)
		}
	}
	if strings.Count(harnessContents, "viewAuditRows != 1") < 2 {
		t.Fatal("rollback harness does not verify the readonly audit before and after state advancement")
	}
}

func TestLifecycleScriptsClearAllProxySpellingsAndPassSyntax(t *testing.T) {
	root := acceptanceRoot(t)
	for _, name := range []string{
		"account-inventory-lifecycle-run.sh",
		"account-inventory-lifecycle-postgres.sh",
		"account-inventory-lifecycle-rollback.sh",
	} {
		path := filepath.Join(root, name)
		encoded, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		contents := string(encoded)
		for _, spelling := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
			if !strings.Contains(contents, spelling) {
				t.Fatalf("%s does not clear %s", name, spelling)
			}
		}
		if strings.Contains(contents, "command -v rg") || strings.Contains(contents, "rg -q") {
			t.Fatalf("%s requires non-standard ripgrep on the acceptance runner", name)
		}
		command := exec.Command("bash", "-n", path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("bash -n %s: %v: %s", name, err, output)
		}
	}
}

func TestLifecycleSnapshotAndPollTemporaryDirectoriesFollowTMPDIR(t *testing.T) {
	root := acceptanceRoot(t)
	for _, name := range []string{
		"account-inventory-lifecycle-run.sh",
		"account-inventory-lifecycle-postgres.sh",
		"account-inventory-lifecycle-rollback.sh",
		"account-inventory-snapshot-postgres.sh",
		"account-inventory-snapshot-container.sh",
		"account-inventory-snapshot-real-node.sh",
		"account-inventory-poll-container.sh",
	} {
		encoded, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		contents := string(encoded)
		for _, required := range []string{
			`temporary_root="${TMPDIR:-/tmp}"`,
			`temporary_root="${temporary_root%/}"`,
			`mktemp -d "$temporary_root/relay-control-`,
			`"$temporary_root"/relay-control-`,
		} {
			if !strings.Contains(contents, required) {
				t.Errorf("%s lacks temporary-root contract %q", name, required)
			}
		}
		if strings.Contains(contents, "/tmp/relay-") || strings.Contains(contents, "/private/tmp/relay-") {
			t.Errorf("%s hard-codes a host relay temporary path", name)
		}
	}
}
