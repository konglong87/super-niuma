package sessioncontrol

import "testing"

func TestParseSessionBackendDefaultsToJSONL(t *testing.T) {
	got, err := ParseSessionBackend("")
	if err != nil || got != SessionBackendJSONL {
		t.Fatalf("ParseSessionBackend(\"\") = %q, %v; want jsonl", got, err)
	}
}

func TestParseSessionBackendAcceptsSQLiteRollback(t *testing.T) {
	got, err := ParseSessionBackend(" SQLITE ")
	if err != nil || got != SessionBackendSQLite {
		t.Fatalf("ParseSessionBackend(sqlite) = %q, %v; want sqlite", got, err)
	}
}

func TestParseSessionBackendRejectsUnknownValues(t *testing.T) {
	if _, err := ParseSessionBackend("mysql"); err == nil {
		t.Fatal("ParseSessionBackend(mysql) error = nil")
	}
}
