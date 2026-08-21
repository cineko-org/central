package central

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestHTTPContractDocumentsEveryServicePoint(t *testing.T) {
	root := contractRepositoryRoot(t)
	source := contractReadFile(t, filepath.Join(root, "internal/central/api/server.go"))
	document := contractReadFile(t, filepath.Join(root, "docs/api-contract.md"))

	staticRoute := regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+ /[^"]*)"`)
	static := make([]string, 0, 58)
	for _, line := range strings.Split(source, "\n") {
		if strings.Contains(line, "+resource") {
			continue
		}
		match := staticRoute.FindStringSubmatch(line)
		if len(match) == 2 {
			static = append(static, match[1])
		}
	}
	if len(static) != 58 {
		t.Fatalf("literal HTTP service points = %d, update contract inventory", len(static))
	}
	for _, route := range static {
		if !strings.Contains(document, "`"+route+"`") {
			t.Errorf("HTTP contract does not document %s", route)
		}
	}

	resources := []string{"presets", "monitors", "reservations", "external-operations", "app-events"}
	for _, resource := range resources {
		if !strings.Contains(source, `"`+resource+`"`) || !strings.Contains(document, "`"+resource+"`") {
			t.Errorf("resource family does not document %q", resource)
		}
	}
	families := []string{
		"GET /v1/{resource}", "POST /v1/{resource}",
		"GET /v1/{resource}/{resourceId}", "PUT /v1/{resource}/{resourceId}",
		"DELETE /v1/{resource}/{resourceId}",
	}
	for _, family := range families {
		if !strings.Contains(document, "`"+family+"`") {
			t.Errorf("HTTP contract does not document family %s", family)
		}
	}
	if concrete := len(static) + len(resources)*len(families); concrete != 83 ||
		!strings.Contains(document, "83 concrete method/path") {
		t.Fatalf("expanded HTTP service points = %d, update contract inventory", concrete)
	}
	methodCounts := map[string]int{}
	for _, route := range static {
		methodCounts[strings.SplitN(route, " ", 2)[0]]++
	}
	for _, family := range families {
		methodCounts[strings.SplitN(family, " ", 2)[0]] += len(resources)
	}
	wantMethodCounts := map[string]int{"GET": 35, "POST": 26, "PUT": 14, "DELETE": 8}
	for method, want := range wantMethodCounts {
		if methodCounts[method] != want {
			t.Errorf("%s service points = %d, want %d", method, methodCounts[method], want)
		}
	}
}

func TestSchemaContractMatchesMigrationHistory(t *testing.T) {
	root := contractRepositoryRoot(t)
	document := contractReadFile(t, filepath.Join(root, "docs/schema-contract.md"))
	migrations, err := filepath.Glob(filepath.Join(root, "internal/central/postgres/migrations/*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(migrations)
	if len(migrations) == 0 {
		t.Fatal("no migrations found")
	}
	for _, migration := range migrations {
		// #nosec G304 -- every path comes from the repository-owned migration glob above.
		contents, err := os.ReadFile(migration)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint := fmt.Sprintf("%s %x", filepath.Base(migration), sha256.Sum256(contents))
		if !strings.Contains(document, fingerprint) {
			t.Errorf("schema contract fingerprint missing or stale: %s", fingerprint)
		}
	}

	currentSchema := map[string][]string{
		"cineko_schema_migrations":         {"version", "applied_at"},
		"probe_runtimes":                   {"id", "installation_id", "kind", "network_id", "network_hint", "capabilities", "max_concurrency", "runtime_version", "protocol", "browser_revision", "platform", "architecture", "token_hash", "token_expires_at", "status", "draining", "available_slots", "health", "reason_code", "last_heartbeat_at", "created_at", "updated_at", "owner_user_id", "device_id", "available_capabilities"},
		"observation_policies":             {"id", "enabled", "revision", "task_kind", "theater_id", "theater_region", "theater_name", "target_date_mode", "target_dates", "horizon_days", "locale", "time_zone", "egress_policy_id", "priority", "min_interval_seconds", "max_interval_seconds", "execution_window_seconds", "next_run_at", "last_finished_at", "last_outcome", "last_error_code", "created_at", "updated_at", "deleted_at", "display_name", "demand_min_interval_seconds", "demand_max_interval_seconds", "burst_min_interval_seconds", "burst_max_interval_seconds", "burst_duration_seconds", "burst_until", "theater_provider_id", "theater_source_key"},
		"observation_assignments":          {"id", "task_kind", "theater_id", "theater_region", "theater_name", "target_dates", "locale", "time_zone", "egress_policy_id", "status", "not_before", "deadline", "probe_id", "lease_token_hash", "lease_expires_at", "run_id", "result_hash", "result_payload", "started_at", "finished_at", "created_at", "updated_at", "policy_id", "priority", "terminal_reason", "completed_by_probe_id", "theater_provider_id", "theater_source_key", "task_data"},
		"assignment_attempts":              {"assignment_id", "probe_id", "attempt", "started_at", "finished_at", "status", "error_code", "lease_token_hash", "network_id", "run_id", "result_hash", "result_payload"},
		"assignment_eligible_probes":       {"assignment_id", "probe_id", "network_id", "eligible_at"},
		"observation_payloads":             {"content_hash", "payload", "created_at"},
		"schedule_captures":                {"assignment_id", "run_id", "target_date", "observed_at", "complete", "error_code", "content_hash", "created_at"},
		"showtime_observations":            {"assignment_id", "run_id", "target_date", "source_key", "theater_id", "auditorium_name", "screen_types", "movie_id", "movie_title", "poster_url", "starts_at", "ends_at", "available_seats", "capacity", "sold_out", "observed_at", "auditorium_id"},
		"consumed_probe_bootstrap_tickets": {"ticket_id", "expires_at", "consumed_at"},
		"client_users":                     {"id", "display_name", "created_at", "updated_at"},
		"client_credentials":               {"user_id", "token_hash", "revoked_at", "created_at", "updated_at"},
		"client_sessions":                  {"id", "user_id", "token_hash", "expires_at", "revoked_at", "created_at", "refresh_token_hash", "refresh_expires_at"},
		"client_devices":                   {"installation_id", "user_id", "device_id", "platform", "architecture", "app_version", "last_seen_at", "created_at", "updated_at"},
		"client_resources":                 {"user_id", "kind", "id", "revision", "payload", "created_at", "updated_at", "deleted_at"},
		"client_commands":                  {"user_id", "command_id", "operation", "resource_kind", "resource_id", "result_revision", "created_at"},
		"client_events":                    {"sequence", "id", "user_id", "event_type", "resource_kind", "resource_id", "resource_revision", "payload", "occurred_at"},
		"client_event_cursors":             {"user_id", "pruned_through", "updated_at"},
		"client_launch_tickets":            {"id", "user_id", "installation_id", "device_id", "client_version", "artifact_sha256", "protocol", "browser_revision", "launcher_nonce", "client_nonce", "token_hash", "expires_at", "consumed_at", "created_at", "release_generation", "browser_artifact_sha256", "playwright_version", "playwright_artifact_sha256"},
		"client_execution_commands":        {"id", "user_id", "monitor_id", "showtime_id", "starts_at", "payload", "status", "leased_installation_id", "last_installation_id", "lease_token_hash", "lease_expires_at", "attempt_count", "reason_code", "completed_at", "created_at", "updated_at"},
		"client_pins":                      {"user_id", "pin_digest", "revoked_at", "created_at", "updated_at"},
		"client_pin_attempts":              {"scope_hash", "failure_count", "blocked_until", "updated_at"},
		"admin_sessions":                   {"token_hash", "user_id", "display_name", "expires_at", "revoked_at", "created_at"},
		"admin_credentials":                {"user_id", "display_name", "password_hash", "created_at", "updated_at"},
		"catalog_state":                    {"id", "generation", "updated_at", "refresh_requested_at"},
		"providers":                        {"id", "name", "content_hash", "first_seen_at", "last_seen_at", "updated_at"},
		"theaters":                         {"id", "provider_id", "source_key", "region", "name", "active", "content_hash", "first_seen_at", "last_seen_at", "updated_at"},
		"movies":                           {"id", "provider_id", "source_key", "title", "poster_url", "display_order", "active", "content_hash", "first_seen_at", "last_seen_at", "updated_at"},
		"auditoriums":                      {"id", "theater_id", "source_key", "name", "screen_types", "capacity", "active", "content_hash", "first_seen_at", "last_seen_at", "updated_at", "current_seat_map_version_id", "seat_map_requested_at"},
		"showtimes":                        {"id", "provider_id", "source_key", "theater_id", "movie_id", "auditorium_id", "starts_at", "ends_at", "active", "content_hash", "first_seen_at", "last_seen_at", "updated_at"},
		"seat_map_versions":                {"id", "auditorium_id", "layout_hash", "capacity", "layout", "observed_at", "first_seen_at", "last_seen_at"},
		"release_components":               {"kind", "channel", "platform", "architecture", "version", "schema_version", "payload", "published_at", "created_at"},
		"desktop_release_registry_state":   {"singleton", "generation", "active_manifest_sha256", "resolver_version", "updated_at"},
	}
	if len(currentSchema) != 33 {
		t.Fatalf("current schema tables = %d, want 33", len(currentSchema))
	}
	for table, columns := range currentSchema {
		row := schemaContractRow(t, document, table)
		for _, column := range columns {
			if !strings.Contains(row, "`"+column) {
				t.Errorf("schema contract table %s does not document column %s", table, column)
			}
		}
	}
}

func schemaContractRow(t *testing.T, document, table string) string {
	t.Helper()
	prefix := "| `" + table + "` |"
	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("schema contract does not document current table %s", table)
	return ""
}

func contractRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))
}

func contractReadFile(t *testing.T, path string) string {
	t.Helper()
	// #nosec G304 -- callers construct paths from contractRepositoryRoot and fixed repository files.
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
