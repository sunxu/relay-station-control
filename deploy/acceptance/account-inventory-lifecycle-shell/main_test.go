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

func TestLifecycleRunnerScansProjectedScenarioMatrix(t *testing.T) {
	output, err := runScript(t, "scan-matrix")
	if err != nil {
		t.Fatalf("projected scenario scanner failed: %v: %s", err, output)
	}
	if output != "account_inventory_lifecycle_canary_scan=success\n" {
		t.Fatalf("projected scenario scanner output outside fixed schema: %q", output)
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
	} {
		if !strings.Contains(contents, required) {
			t.Fatalf("rollback acceptance missing %q", required)
		}
	}
	for _, forbidden := range []string{"git checkout", "git switch", "git worktree"} {
		if strings.Contains(contents, forbidden) {
			t.Fatalf("rollback acceptance mutates the primary worktree with %q", forbidden)
		}
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
