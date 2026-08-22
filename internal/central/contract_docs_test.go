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
	static := make([]string, 0, 56)
	for _, line := range strings.Split(source, "\n") {
		if strings.Contains(line, "+resource") {
			continue
		}
		match := staticRoute.FindStringSubmatch(line)
		if len(match) == 2 {
			static = append(static, match[1])
		}
	}
	if len(static) != 56 {
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
	if concrete := len(static) + len(resources)*len(families); concrete != 81 ||
		!strings.Contains(document, "81 concrete method/path") {
		t.Fatalf("expanded HTTP service points = %d, update contract inventory", concrete)
	}
	methodCounts := map[string]int{}
	for _, route := range static {
		methodCounts[strings.SplitN(route, " ", 2)[0]]++
	}
	for _, family := range families {
		methodCounts[strings.SplitN(family, " ", 2)[0]] += len(resources)
	}
	wantMethodCounts := map[string]int{"GET": 35, "POST": 25, "PUT": 13, "DELETE": 8}
	for method, want := range wantMethodCounts {
		if methodCounts[method] != want {
			t.Errorf("%s service points = %d, want %d", method, methodCounts[method], want)
		}
	}
}

func TestOwnedBoundariesRejectDTOsAliasesAndLegacyEvents(t *testing.T) {
	root := contractRepositoryRoot(t)
	dtoDeclaration := regexp.MustCompile(`\btype\s+\w+(?:Request|Response|Payload|Envelope|DTO|Dto)\s+(?:struct|=)`)
	generatedAlias := regexp.MustCompile(`\btype\s+\w+\s*=\s*\*?\w+pb\.\w+`)
	versionedExecutionEvent := regexp.MustCompile(`execution\.ready\.[[:alnum:]]`)

	for _, relativeRoot := range []string{"cmd", "internal", "frontend/src", "docs"} {
		walkRoot := filepath.Join(root, relativeRoot)
		err := filepath.Walk(walkRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			extension := filepath.Ext(path)
			if extension != ".go" && extension != ".ts" && extension != ".tsx" && extension != ".md" {
				return nil
			}
			contents := contractReadFile(t, path)
			if dtoDeclaration.MatchString(contents) {
				t.Errorf("Cineko-owned boundary declares a DTO in %s", path)
			}
			if generatedAlias.MatchString(contents) {
				t.Errorf("generated protobuf alias found in %s", path)
			}
			if versionedExecutionEvent.MatchString(contents) {
				t.Errorf("legacy execution event name found in %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
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
		"cineko_schema_migrations":                 {"version", "applied_at", "checksum"},
		"probe_runtimes":                           {"id", "installation_id", "kind", "network_id", "network_hint", "capabilities", "max_concurrency", "runtime_version", "browser_revision", "platform", "architecture", "token_hash", "token_expires_at", "status", "draining", "available_slots", "health", "reason_code", "last_heartbeat_at", "created_at", "updated_at", "owner_user_id", "device_id", "available_capabilities"},
		"observation_policies":                     {"id", "enabled", "revision", "task_kind", "theater_id", "theater_region", "theater_name", "target_date_mode", "target_dates", "horizon_days", "locale", "time_zone", "egress_policy_id", "priority", "min_interval_seconds", "max_interval_seconds", "execution_window_seconds", "next_run_at", "last_finished_at", "last_outcome", "last_error_code", "created_at", "updated_at", "deleted_at", "display_name", "demand_min_interval_seconds", "demand_max_interval_seconds", "burst_min_interval_seconds", "burst_max_interval_seconds", "burst_duration_seconds", "burst_until", "theater_provider_id", "theater_source_key"},
		"observation_assignments":                  {"id", "task_kind", "theater_id", "theater_region", "theater_name", "target_dates", "locale", "time_zone", "egress_policy_id", "status", "not_before", "deadline", "probe_id", "lease_token_hash", "lease_expires_at", "run_id", "result_hash", "result_payload", "started_at", "finished_at", "created_at", "updated_at", "policy_id", "priority", "terminal_reason", "completed_by_probe_id", "theater_provider_id", "theater_source_key", "task_data", "lane", "hot_target_fingerprint", "auditorium_id", "showtime_id"},
		"assignment_attempts":                      {"assignment_id", "probe_id", "attempt", "started_at", "finished_at", "status", "error_code", "lease_token_hash", "network_id", "run_id", "result_hash", "result_payload"},
		"assignment_eligible_probes":               {"assignment_id", "probe_id", "network_id", "eligible_at"},
		"observation_payloads":                     {"content_hash", "payload", "created_at"},
		"schedule_captures":                        {"assignment_id", "run_id", "target_date", "observed_at", "complete", "error_code", "content_hash", "created_at"},
		"showtime_observations":                    {"assignment_id", "run_id", "target_date", "source_key", "theater_id", "auditorium_name", "screen_types", "movie_id", "movie_title", "poster_url", "starts_at", "ends_at", "available_seats", "capacity", "sold_out", "observed_at", "auditorium_id"},
		"consumed_probe_bootstrap_tickets":         {"ticket_id", "expires_at", "consumed_at"},
		"client_users":                             {"id", "display_name", "created_at", "updated_at"},
		"client_credentials":                       {"user_id", "token_hash", "revoked_at", "created_at", "updated_at"},
		"client_sessions":                          {"id", "user_id", "token_hash", "expires_at", "revoked_at", "created_at", "refresh_token_hash", "refresh_expires_at"},
		"client_devices":                           {"installation_id", "user_id", "device_id", "platform", "architecture", "app_version", "last_seen_at", "created_at", "updated_at"},
		"client_resources":                         {"user_id", "kind", "id", "revision", "created_at", "updated_at", "deleted_at"},
		"client_commands":                          {"user_id", "command_id", "operation", "resource_kind", "resource_id", "result_revision", "created_at"},
		"client_events":                            {"sequence", "id", "user_id", "event_type", "resource_kind", "resource_id", "resource_revision", "payload", "occurred_at"},
		"client_event_cursors":                     {"user_id", "pruned_through", "updated_at"},
		"client_launch_tickets":                    {"id", "user_id", "installation_id", "device_id", "client_version", "artifact_sha256", "browser_revision", "launcher_nonce", "client_nonce", "token_hash", "expires_at", "consumed_at", "created_at", "release_generation", "browser_artifact_sha256", "playwright_version", "playwright_artifact_sha256"},
		"client_execution_commands":                {"id", "user_id", "monitor_id", "showtime_id", "starts_at", "payload", "status", "leased_installation_id", "last_installation_id", "lease_token_hash", "lease_expires_at", "attempt_count", "reason_code", "completed_at", "created_at", "updated_at", "observed_at"},
		"client_pins":                              {"user_id", "pin_digest", "revoked_at", "created_at", "updated_at"},
		"client_pin_attempts":                      {"scope_hash", "failure_count", "blocked_until", "updated_at"},
		"admin_sessions":                           {"token_hash", "user_id", "display_name", "expires_at", "revoked_at", "created_at"},
		"admin_credentials":                        {"user_id", "display_name", "password_hash", "created_at", "updated_at"},
		"catalog_state":                            {"id", "generation", "updated_at", "refresh_requested_at"},
		"providers":                                {"id", "name", "content_hash", "first_seen_at", "last_seen_at", "updated_at"},
		"theaters":                                 {"id", "provider_id", "source_key", "region", "name", "active", "content_hash", "first_seen_at", "last_seen_at", "updated_at"},
		"movies":                                   {"id", "provider_id", "source_key", "title", "poster_url", "display_order", "active", "content_hash", "first_seen_at", "last_seen_at", "updated_at"},
		"auditoriums":                              {"id", "theater_id", "source_key", "name", "screen_types", "capacity", "active", "content_hash", "first_seen_at", "last_seen_at", "updated_at", "current_seat_map_version_id"},
		"seat_map_collection_states":               {"auditorium_id", "state", "trigger_kind", "priority", "assignment_id", "showtime_id", "reason_code", "requested_at", "last_attempt_at", "next_attempt_at", "consecutive_failures", "updated_at"},
		"showtimes":                                {"id", "provider_id", "source_key", "theater_id", "movie_id", "auditorium_id", "schedule_date", "starts_at", "ends_at", "active", "content_hash", "first_seen_at", "last_seen_at", "updated_at"},
		"seat_map_versions":                        {"id", "auditorium_id", "layout_hash", "capacity", "observed_at", "first_seen_at", "last_seen_at"},
		"seat_map_seats":                           {"version_id", "position", "seat_id", "label", "row_label", "seat_number", "x", "y", "seat_type", "zone_name", "zone_kind", "sale_form_code", "sale_form_name", "left_aisle", "right_aisle", "source_label", "source_seat_kind_code", "source_seat_kind_name"},
		"seat_map_seat_features":                   {"version_id", "seat_id", "position", "feature"},
		"seat_map_seat_source_classes":             {"version_id", "seat_id", "position", "source_class"},
		"seat_map_zones":                           {"version_id", "position", "code", "name", "kind_code", "kind_name", "min_x", "max_x", "min_y", "max_y", "capacity"},
		"seat_map_blocks":                          {"version_id", "position", "code", "name", "kind_code", "kind_name", "min_x", "max_x", "min_y", "max_y"},
		"release_components":                       {"kind", "channel", "platform", "architecture", "version", "payload", "published_at", "created_at"},
		"desktop_release_registry_state":           {"singleton", "generation", "active_manifest_sha256", "updated_at"},
		"client_settings":                          {"user_id", "resource_kind", "id", "network_mode", "proxy_username", "proxy_password", "proxy_has_password"},
		"client_setting_proxy_urls":                {"user_id", "settings_id", "position", "url"},
		"client_setting_webhooks":                  {"user_id", "settings_id", "position", "id", "name", "url", "secret", "enabled", "has_secret"},
		"client_setting_webhook_event_kinds":       {"user_id", "settings_id", "webhook_position", "position", "event_kind"},
		"client_presets":                           {"user_id", "resource_kind", "id", "name", "theater_id", "auditorium_id", "seat_count", "has_seat_preference", "together", "avoid_edges", "preset_created_at", "preset_updated_at"},
		"client_preset_explicit_seats":             {"user_id", "preset_id", "position", "seat_label"},
		"client_preset_preferred_rows":             {"user_id", "preset_id", "position", "row_label"},
		"client_preset_preferred_zones":            {"user_id", "preset_id", "position", "name", "min_x", "max_x", "min_y", "max_y", "weight"},
		"client_preset_preferred_types":            {"user_id", "preset_id", "position", "seat_type"},
		"client_monitors":                          {"user_id", "resource_kind", "id", "preset_id", "movie_id", "movie_title", "search_horizon_days", "earliest_minute", "latest_minute", "state", "state_reason", "last_checked_at", "reservation_id", "monitor_created_at", "monitor_updated_at"},
		"client_monitor_target_dates":              {"user_id", "monitor_id", "position", "target_date"},
		"client_monitor_target_weekdays":           {"user_id", "monitor_id", "position", "target_weekday"},
		"client_reservations":                      {"user_id", "resource_kind", "id", "monitor_id", "booking_number", "total_price", "booked_at", "cancelled_at", "refund_amount", "state"},
		"client_reservation_seats":                 {"user_id", "reservation_id", "position", "seat_label"},
		"client_reservation_showtimes":             {"user_id", "reservation_id", "showtime_id", "provider_id", "source_key", "theater_id", "movie_id", "movie_provider_id", "movie_source_key", "movie_title", "movie_poster_url", "auditorium_id", "auditorium_theater_id", "auditorium_source_key", "auditorium_name", "auditorium_capacity", "auditorium_layout_hash", "schedule_date", "starts_at", "ends_at", "available_seats", "capacity", "sold_out"},
		"client_reservation_showtime_screen_types": {"user_id", "reservation_id", "position", "screen_type"},
		"client_external_operations":               {"user_id", "resource_kind", "id", "monitor_id", "reservation_id", "kind", "state", "refund_amount", "last_error", "operation_created_at", "operation_updated_at"},
		"client_app_events":                        {"user_id", "resource_kind", "id", "kind", "message", "event_created_at", "read_at", "tone"},
		"seat_availability_snapshots":              {"id", "showtime_id", "auditorium_id", "layout_hash", "content_hash", "observed_at", "created_at"},
		"seat_availability_snapshot_seats":         {"snapshot_id", "position", "seat_id"},
		"monitor_showtime_availability":            {"user_id", "monitor_id", "showtime_id", "snapshot_id", "matched", "observed_at", "updated_at"},
	}
	if len(currentSchema) != 60 {
		t.Fatalf("current schema tables = %d, want 60", len(currentSchema))
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
