package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRetiredSSHLegacyReadAndMigration(t *testing.T) {
	c := DefaultConfig()
	raw, _ := json.Marshal(c)
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	fields["schemaVersion"] = json.RawMessage(`1`)
	fields["ssh"] = json.RawMessage(`{"enable":true,"port":2222,"authorizedKeys":["old-key"]}`)
	raw, _ = json.Marshal(fields)
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, raw, 0600)
	historical, err := LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := json.Marshal(historical)
	if bytes.Contains(exported, []byte(`"ssh"`)) {
		t.Fatal("retired SSH survives read")
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(raw, unchanged) {
		t.Fatal("read-only snapshot was rewritten")
	}
	next, err := LoadDraft(path)
	if err != nil {
		t.Fatal(err)
	}
	if next.SchemaVersion != 2 {
		t.Fatalf("version %d", next.SchemaVersion)
	}
	backup, err := os.ReadFile(path + ".v1.bak")
	if err != nil || !bytes.Equal(backup, raw) {
		t.Fatal("legacy bytes not preserved", err)
	}
	again, err := LoadDraft(path)
	if err != nil || again.Revision != next.Revision {
		t.Fatal("migration not stable", err)
	}
}

func TestRetiredSSHRejectedInCurrentDocument(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"schemaVersion":2,"ssh":{"enable":true}}`), &c); err == nil {
		t.Fatal("current SSH field accepted")
	}
	raw, _ := json.Marshal(DefaultConfig())
	if bytes.Contains(raw, []byte(`"ssh"`)) {
		t.Fatal("default exports SSH")
	}
}

func TestLegacySSHBackupEnvelopeRead(t *testing.T) {
	raw := `{"config":{"schemaVersion":1,"ssh":{"enable":true,"port":2222},"plugins":{"custom":{"enable":false,"config":{"large":9007199254740993}}}},"kind":"config-only"}`
	cfg, err := DecodeDocument([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := json.Marshal(cfg)
	if bytes.Contains(exported, []byte(`"ssh"`)) || !bytes.Contains(exported, []byte(`9007199254740993`)) {
		t.Fatal("backup migration changed unrelated data", string(exported))
	}
}
