package postgres

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPostgresMigratedSchemaMatchesContractDocumentation(t *testing.T) {
	if testDatabaseURL == "" {
		t.Skip("CINEKO_CENTRAL_TEST_DATABASE_URL is not set")
	}
	store, err := Open(t.Context(), testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve schema contract test path")
	}
	contractPath := filepath.Join(filepath.Dir(filename), "../../../docs/schema-contract.md")
	// #nosec G304 -- the path is fixed relative to this repository-owned test file.
	document, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.pool.Query(t.Context(), `
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tables := map[string][]string{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		tables[table] = append(tables[table], column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 59 {
		t.Fatalf("migrated schema tables = %d, want 59", len(tables))
	}
	contract := string(document)
	for table, columns := range tables {
		row := schemaDocumentationRow(t, contract, table)
		for _, column := range columns {
			if !strings.Contains(row, "`"+column) {
				t.Errorf("schema contract table %s does not document migrated column %s", table, column)
			}
		}
	}
}

func schemaDocumentationRow(t *testing.T, document, table string) string {
	t.Helper()
	prefix := "| `" + table + "` |"
	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("schema contract does not document migrated table %s", table)
	return ""
}
