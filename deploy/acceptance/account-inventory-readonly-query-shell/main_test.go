package readonlyqueryacceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const (
	runnerName    = "account-inventory-readonly-query-run.sh"
	postgresImage = "postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2"
	officialImage = "eceasy/cli-proxy-api@sha256:7f598ce64478a8a5f90ed76875e0e9b0e7d77b80e17184b13df18c3d5bdb3def"
	alpineImage   = "alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce"
	proxyPrefix   = "env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy "
)

var acceptanceScripts = []string{
	runnerName,
	"account-inventory-readonly-query-postgres.sh",
	"account-inventory-readonly-query-recovery.sh",
	"account-inventory-readonly-query-data-plane-probe.sh",
}

var resourceOwningScripts = []string{
	runnerName,
	"account-inventory-readonly-query-postgres.sh",
	"account-inventory-readonly-query-recovery.sh",
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func acceptanceRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "deploy", "acceptance")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func runRunner(t *testing.T, arguments ...string) (string, error) {
	t.Helper()
	before := readonlyTemporaryDirectories(t)
	command := exec.Command("bash", append([]string{runnerName}, arguments...)...)
	command.Dir = acceptanceRoot(t)
	environment := make([]string, 0, len(os.Environ())+12)
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		switch name {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
			"DATABASE_URL", "PGPASSWORD", "CONTROL_DATABASE_URL", "CONTROL_API_URL", "CONTROL_ADMIN_TOKEN":
			continue
		}
		environment = append(environment, item)
	}
	command.Env = append(environment,
		"HTTP_PROXY=readonly-query-proxy-canary.invalid",
		"HTTPS_PROXY=readonly-query-proxy-canary.invalid",
		"ALL_PROXY=readonly-query-proxy-canary.invalid",
		"http_proxy=readonly-query-proxy-canary.invalid",
		"https_proxy=readonly-query-proxy-canary.invalid",
		"all_proxy=readonly-query-proxy-canary.invalid",
		"DATABASE_URL=postgres://readonly-query-credential-canary.invalid/database",
		"PGPASSWORD=readonly-query-password-canary",
		"CONTROL_DATABASE_URL=postgres://readonly-query-control-canary.invalid/database",
		"CONTROL_API_URL=https://readonly-query-api-canary.invalid",
		"CONTROL_ADMIN_TOKEN=readonly-query-token-canary",
	)
	output, err := command.CombinedOutput()
	after := readonlyTemporaryDirectories(t)
	for path := range after {
		if _, existed := before[path]; !existed {
			t.Fatal("invalid runner invocation left a readonly-query temporary directory")
		}
	}
	for _, canary := range []string{
		"readonly-query-proxy-canary.invalid",
		"readonly-query-credential-canary.invalid",
		"readonly-query-password-canary",
		"readonly-query-control-canary.invalid",
		"readonly-query-api-canary.invalid",
		"readonly-query-token-canary",
	} {
		if strings.Contains(string(output), canary) {
			t.Fatal("invalid runner invocation echoed a sensitive canary")
		}
	}
	return string(output), err
}

func readonlyTemporaryDirectories(t *testing.T) map[string]struct{} {
	t.Helper()
	directories := make(map[string]struct{})
	for _, pattern := range []string{
		"/tmp/relay-control-readonly-query-*",
		"/private/tmp/relay-control-readonly-query-*",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal("invalid readonly-query temporary directory pattern")
		}
		for _, match := range matches {
			directories[match] = struct{}{}
		}
	}
	return directories
}

func TestReadonlyQueryRunnerRejectsUnknownModeWithFixedOutput(t *testing.T) {
	output, err := runRunner(t, "https://readonly-query-target-canary.invalid")
	if err == nil {
		t.Fatal("URL-shaped mode unexpectedly succeeded")
	}
	if output != "account_inventory_readonly_query_acceptance=failed reason=invalid_mode\n" {
		t.Fatal("invalid-mode output outside fixed schema")
	}
}

func TestReadonlyQueryRunnerRejectsAdditionalArgumentsWithFixedOutput(t *testing.T) {
	output, err := runRunner(t, "static", "readonly-query-credential-canary")
	if err == nil {
		t.Fatal("additional credential-shaped argument unexpectedly succeeded")
	}
	if output != "account_inventory_readonly_query_acceptance=failed reason=invalid_arguments\n" {
		t.Fatal("invalid-arguments output outside fixed schema")
	}
}

func TestReadonlyQueryRunnerHasClosedModeMatrix(t *testing.T) {
	contents := readFile(t, filepath.Join(acceptanceRoot(t), runnerName))
	for _, required := range []string{
		`mode="${1:-static}"`,
		"static)",
		"postgres)",
		"recovery)",
		"all)",
		"account-inventory-readonly-query-postgres.sh",
		"account-inventory-readonly-query-recovery.sh",
		"go test",
		"npm",
		"web",
	} {
		if !strings.Contains(contents, required) {
			t.Fatalf("readonly query runner is missing %q", required)
		}
	}
	if !regexp.MustCompile(`(?m)(\[ "?\$#"? -gt 1 \]|\(\( *\$# *> *1 *\)\))`).MatchString(contents) {
		t.Fatal("readonly query runner does not reject more than one argument")
	}
	preamble := contents[:strings.Index(contents, "fixed_failure()")]
	for _, forbidden := range []string{"${2", "$2", "$@", "$*"} {
		if strings.Contains(preamble, forbidden) {
			t.Fatalf("readonly query runner accepts an unbounded argument through %q", forbidden)
		}
	}
}

func TestReadonlyQueryScriptsAreSyntacticallyValidAndFailClosed(t *testing.T) {
	root := acceptanceRoot(t)
	for _, name := range acceptanceScripts {
		path := filepath.Join(root, name)
		contents := readFile(t, path)
		for _, required := range []string{"set -euo pipefail", "umask 077"} {
			if !strings.Contains(contents, required) {
				t.Errorf("%s is missing %q", name, required)
			}
		}
		for _, forbidden := range []string{
			"set -x", "printenv", "export -p", "eval ", "wget ", "docker logs",
			"git checkout", "git switch", "git worktree",
		} {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s contains forbidden construct %q", name, forbidden)
			}
		}
		if regexp.MustCompile(`\$\{(?:DATABASE_URL|PGPASSWORD|CONTROL_DATABASE_URL|CONTROL_API_URL|CONTROL_ADMIN_TOKEN)(?::?[-+?=])`).MatchString(contents) {
			t.Errorf("%s accepts a URL or credential override", name)
		}
		if strings.Contains(contents, "${CONTROL_READONLY_QUERY_ACCEPTANCE_GOPROXY:-") {
			t.Errorf("%s accepts an arbitrary module proxy URL", name)
		}
		for _, line := range strings.Split(contents, "\n") {
			if strings.Contains(line, "curl ") && (!strings.Contains(line, "--noproxy '*'") || !strings.Contains(line, "http://127.0.0.1:")) {
				t.Errorf("%s contains a curl target outside fixed loopback", name)
			}
		}
		command := exec.Command("bash", "-n", path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("bash -n %s: %v: %s", name, err, output)
		}
	}
}

func TestReadonlyQueryCommandsExplicitlyClearAllProxySpellings(t *testing.T) {
	commandPattern := regexp.MustCompile(`(^|[;&|(!] *)((go|npm|npx|make|docker) +[^#\n]+)`)
	for _, name := range acceptanceScripts {
		contents := strings.ReplaceAll(readFile(t, filepath.Join(acceptanceRoot(t), name)), "\\\n", " ")
		for lineNumber, line := range strings.Split(contents, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.Contains(trimmed, "command -v ") {
				continue
			}
			matches := commandPattern.FindAllStringSubmatch(trimmed, -1)
			for _, match := range matches {
				command := strings.TrimSpace(match[2])
				prefixAt := strings.Index(trimmed, proxyPrefix)
				commandAt := strings.Index(trimmed, command)
				if prefixAt < 0 || prefixAt > commandAt {
					t.Errorf("%s:%d command does not explicitly clear all proxy spellings", name, lineNumber+1)
				}
			}
		}
	}
}

func TestReadonlyQueryBundlePinsPostgresAndOwnsTemporaryCleanup(t *testing.T) {
	root := acceptanceRoot(t)
	var bundle strings.Builder
	for _, name := range acceptanceScripts {
		bundle.WriteString(readFile(t, filepath.Join(root, name)))
		bundle.WriteByte('\n')
	}
	contents := bundle.String()
	composeName := "account-inventory-readonly-query-postgres.compose.yaml"
	if !strings.Contains(contents, composeName) {
		t.Fatal("readonly query acceptance does not reference its isolated PostgreSQL compose file")
	}
	compose := readFile(t, filepath.Join(root, composeName))
	if !strings.Contains(compose, "image: "+postgresImage) {
		t.Fatal("readonly query compose file does not contain the approved PostgreSQL image digest")
	}
	if regexp.MustCompile(`(?m)^\s*image:\s*postgres:[^@\n]+$`).MatchString(compose) {
		t.Fatal("readonly query compose file contains an unpinned PostgreSQL image")
	}
	if strings.Contains(compose, "container_name:") {
		t.Fatal("readonly query compose file bypasses project-scoped naming")
	}
	if regexp.MustCompile(`127\.0\.0\.1:[0-9]+:`).MatchString(compose) {
		t.Fatal("readonly query compose file binds a fixed host port")
	}
	if !strings.Contains(compose, "image: "+officialImage) && !strings.Contains(contents, officialImage) {
		t.Fatal("readonly query recovery does not pin the approved official CLIProxyAPI image digest")
	}
	if !strings.Contains(compose, "image: "+alpineImage) {
		t.Fatal("readonly query data-plane probe does not pin the approved Alpine image digest")
	}
	if !strings.Contains(compose, "internal: true") || strings.Count(compose, "      - isolated") < 2 {
		t.Fatal("readonly query official data plane and its probe do not share an egress-isolated network")
	}
	for _, name := range []string{
		"account-inventory-readonly-query-postgres.sh",
		"account-inventory-readonly-query-recovery.sh",
	} {
		script := readFile(t, filepath.Join(root, name))
		if !strings.Contains(script, "GOOSE_DBSTRING=") || strings.Contains(script, `go tool goose -dir`) {
			t.Errorf("%s does not keep the migration database URL out of argv", name)
		}
		if !strings.Contains(script, "SELECT max(version_id) FROM goose_db_version WHERE is_applied") {
			t.Errorf("%s does not verify the applied Migration version", name)
		}
	}
	postgresScript := readFile(t, filepath.Join(root, "account-inventory-readonly-query-postgres.sh"))
	for _, required := range []string{
		"TestAccountInventoryReadonlyQueryMigrationEmptyDownUpRestoresCompatibility",
		"protected_down_empty_up=covered",
		"protected_down_audit_fail_closed=covered",
	} {
		if !strings.Contains(postgresScript, required) {
			t.Errorf("readonly query PostgreSQL acceptance lacks protected-down contract %q", required)
		}
	}
	for _, required := range []string{"docker compose --project-name", "down --volumes --remove-orphans"} {
		if !strings.Contains(contents, required) {
			t.Errorf("readonly query acceptance lacks bounded temporary cleanup contract %q", required)
		}
	}
	if strings.Contains(contents, `rm -rf -- "$HOME"`) || strings.Contains(contents, "rm -rf -- /") {
		t.Fatal("readonly query acceptance contains a broad cleanup target")
	}
	for _, name := range resourceOwningScripts {
		script := readFile(t, filepath.Join(root, name))
		for _, required := range []string{
			"mktemp -d",
			"/tmp/relay-control-readonly-query-",
			"/private/tmp/relay-control-readonly-query-",
			"trap cleanup EXIT",
			"rm -rf -- \"$runtime_directory\"",
			`[ -e "$runtime_directory" ]`,
		} {
			if !strings.Contains(script, required) {
				t.Errorf("%s lacks bounded temporary cleanup contract %q", name, required)
			}
		}
	}
}

func TestReadonlyQuerySuccessfulPathsStrictlyVerifyCleanup(t *testing.T) {
	root := acceptanceRoot(t)
	for _, name := range resourceOwningScripts {
		contents := readFile(t, filepath.Join(root, name))
		definition := strings.Index(contents, "strict_cleanup()")
		call := strings.LastIndex(contents, "\n  strict_cleanup\n")
		summary := strings.LastIndex(contents, "=success")
		if definition < 0 || call <= definition || summary <= call {
			t.Errorf("%s does not complete strict cleanup before its success summary", name)
		}
		strictBodyEnd := strings.Index(contents[definition:], "\n}")
		if strictBodyEnd < 0 {
			t.Errorf("%s has an invalid strict cleanup function", name)
			continue
		}
		strictBody := contents[definition : definition+strictBodyEnd]
		if !strings.Contains(strictBody, `rm -rf -- "$runtime_directory"`) || !strings.Contains(strictBody, `[ -e "$runtime_directory" ]`) {
			t.Errorf("%s strict cleanup neither removes nor verifies its temporary directory", name)
		}
	}
	for _, name := range []string{
		"account-inventory-readonly-query-postgres.sh",
		"account-inventory-readonly-query-recovery.sh",
	} {
		contents := readFile(t, filepath.Join(root, name))
		for _, required := range []string{
			"compose ps --all --quiet",
			"docker ps --all",
			"docker volume ls",
			"docker network ls",
			"label=com.docker.compose.project=",
		} {
			if !strings.Contains(contents, required) {
				t.Errorf("%s lacks project-scoped cleanup verification %q", name, required)
			}
		}
		if !regexp.MustCompile(`(?m)^\s*project_name=.*runtime_directory`).MatchString(contents) {
			t.Errorf("%s does not derive its compose project from the randomized temporary directory", name)
		}
		if strings.Count(contents, "label=com.docker.compose.project=") < 3 {
			t.Errorf("%s does not verify project-labeled container, network, and volume cleanup", name)
		}
		if regexp.MustCompile(`(?m)^project_name=.*\$\$`).MatchString(contents) {
			t.Errorf("%s derives its compose project only from the process ID", name)
		}
		strictTeardown := false
		for _, line := range strings.Split(contents, "\n") {
			if strings.Contains(line, "compose down --volumes --remove-orphans") && !strings.Contains(line, "|| true") {
				strictTeardown = true
			}
		}
		if !strictTeardown {
			t.Errorf("%s has no strict compose teardown", name)
		}
	}
}

func TestReadonlyQueryRecoveryUsesOfficialDataPlane(t *testing.T) {
	root := acceptanceRoot(t)
	script := readFile(t, filepath.Join(root, "account-inventory-readonly-query-recovery.sh"))
	harness := readFile(t, filepath.Join(root, "account-inventory-readonly-query-postgres-recovery", "main.go"))
	probe := readFile(t, filepath.Join(root, "account-inventory-readonly-query-data-plane-probe.sh"))
	if strings.Contains(harness, `"net/http/httptest"`) {
		t.Fatal("readonly query recovery substitutes httptest for the official data plane")
	}
	combined := script + "\n" + harness + "\n" + probe
	for _, required := range []string{
		"/v1/models",
		"official_data_plane_baseline=1",
		"official_data_plane_http=100/100",
		"management_query_fail_closed=1",
		"pool_connection_exhaustion_fail_closed=1",
	} {
		if !strings.Contains(combined, required) {
			t.Errorf("official data-plane recovery contract is missing %q", required)
		}
	}
	if strings.Contains(script, "CONTROL_READONLY_QUERY_RECOVERY_CONTROL_PORT") {
		t.Fatal("readonly query recovery accepts an arbitrary Control listen port")
	}
	usesGlobalLock := strings.Contains(script, "flock") ||
		(strings.Contains(script, `mkdir "$lock_directory"`) && strings.Contains(script, `rmdir "$lock_directory"`))
	if strings.Contains(script, "18082") && !usesGlobalLock {
		t.Fatal("readonly query recovery fixes an unlocked shared Control host port")
	}
	for _, overstated := range []string{"http_query_restart=1", "http_query_recovered=1"} {
		if strings.Contains(combined, overstated) {
			t.Errorf("readonly query recovery overstates restart coverage with %q", overstated)
		}
	}
}

func TestReadonlyQueryCIJobIsPinnedAndComplete(t *testing.T) {
	workflow := readFile(t, filepath.Join(repositoryRoot(t), ".github", "workflows", "ci.yml"))
	start := strings.Index(workflow, "  postgres_readonly_query:")
	end := strings.Index(workflow, "  official_snapshot:")
	if start < 0 || end <= start {
		t.Fatal("isolated readonly query CI job is missing")
	}
	job := workflow[start:end]
	for _, required := range []string{
		"PostgreSQL 18 account inventory readonly query acceptance",
		"timeout-minutes: 60",
		"actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd",
		"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
		"actions/setup-node@48b55a011bda9f5d6aeb4c2d9c7362e8dae4041e",
		`node-version: "24"`,
		proxyPrefix + "npm ci",
		proxyPrefix + "docker pull " + officialImage,
		proxyPrefix + "docker pull " + postgresImage,
		proxyPrefix + "docker pull " + alpineImage,
		proxyPrefix + "deploy/acceptance/" + runnerName + " all",
	} {
		if !strings.Contains(job, required) {
			t.Errorf("readonly query CI job is missing %q", required)
		}
	}
	for _, required := range []string{
		"add-control-account-inventory-readonly-query --strict",
		"openspec/specs/account-inventory-readonly-query/spec.md",
		"*-add-control-account-inventory-readonly-query",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("active/archive OpenSpec validation is missing %q", required)
		}
	}
}
