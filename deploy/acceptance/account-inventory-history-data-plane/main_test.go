package historydataplaneacceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const officialCLIProxyAPIDigest = "eceasy/cli-proxy-api@sha256:7f598ce64478a8a5f90ed76875e0e9b0e7d77b80e17184b13df18c3d5bdb3def"

func TestAccountInventoryHistoryDataPlaneContract(t *testing.T) {
	root := acceptanceRoot(t)
	runner := readFile(t, filepath.Join(root, "account-inventory-history-data-plane.sh"))
	compose := readFile(t, filepath.Join(root, "account-inventory-readonly-query-postgres.compose.yaml"))
	probe := readFile(t, filepath.Join(root, "account-inventory-readonly-query-data-plane-probe.sh"))
	for _, required := range []string{
		"set -euo pipefail", "umask 077",
		"unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy",
		"compose run --rm data-plane-probe 1", "compose run --rm data-plane-probe 100",
		"stop_control", "compose stop --timeout 1 postgres",
		"INSERT INTO environments", "'history-data-plane'", "environment_seed_failed",
		"control_start_invalid_configuration", "control_start_database_initialization_failed",
		"control_start_database_unavailable", "control_start_environment_identity_failed",
		"control_start_listen_failed",
		"control_stopped=true", "postgres_stopped=true",
		"baseline_models=1/1", "outage_models=100/100",
		"scope=data_plane_isolation", "gateway_inference_e2e=not_covered",
		"cleanup_containers=0", "cleanup_volumes=0", "cleanup_networks=0",
	} {
		if !strings.Contains(runner, required) {
			t.Errorf("history data-plane runner lacks %q", required)
		}
	}
	combined := compose + "\n" + probe
	for _, required := range []string{
		officialCLIProxyAPIDigest, "/v1/models", "Authorization: Bearer %s",
		"official_data_plane_baseline=1", "official_data_plane_http=100/100",
	} {
		if !strings.Contains(combined, required) {
			t.Errorf("history data-plane fixture lacks %q", required)
		}
	}
	if baseline, stopControl, stopPostgres, outage := strings.Index(runner, "compose run --rm data-plane-probe 1"),
		strings.LastIndex(runner, "stop_control"), strings.Index(runner, "compose stop --timeout 1 postgres"),
		strings.Index(runner, "compose run --rm data-plane-probe 100"); baseline < 0 || stopControl <= baseline || stopPostgres <= stopControl || outage <= stopPostgres {
		t.Fatal("history data-plane stop window ordering is invalid")
	}
	if migrate, seed, start := strings.Index(runner, "migrate_database \"$owner_url\""),
		strings.LastIndex(runner, "seed_environment"), strings.Index(runner, `CONTROL_HTTP_ADDR="127.0.0.1:${control_port}"`); migrate < 0 || seed <= migrate || start <= seed {
		t.Fatal("history data-plane environment seed ordering is invalid")
	}
	for _, forbidden := range []string{
		"/v1/chat/completions", "GOCACHE=", "GOTMPDIR=", "TMPDIR=",
		"gateway_inference_e2e=covered", "gateway_inference_e2e=true",
	} {
		if strings.Contains(runner+"\n"+probe, forbidden) {
			t.Errorf("history data-plane gate contains forbidden %q", forbidden)
		}
	}
	if output, err := exec.Command("bash", "-n", filepath.Join(root, "account-inventory-history-data-plane.sh")).CombinedOutput(); err != nil {
		t.Fatalf("history data-plane runner syntax invalid: %v: %s", err, output)
	}
}

func acceptanceRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("history data-plane contract path unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("history data-plane contract file unavailable")
	}
	return string(contents)
}
