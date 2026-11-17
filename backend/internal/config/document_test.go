package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsAmbiguousOrUnsupportedDocuments(t *testing.T) {
	for name, data := range map[string]string{
		"unknown root":     `{"unexpected":"private-secret"}`,
		"unknown nested":   `{"wan":{"mode":"dhcp","passwordTypo":"private-secret"}}`,
		"duplicate root":   `{"wan":{},"wan":{}}`,
		"duplicate plugin": `{"plugins":{"custom":{"config":{"secret":"private-secret","secret":"other"}}}}`,
		"future schema":    `{"schemaVersion":3}`,
		"negative schema":  `{"schemaVersion":-1}`,
		"invalid revision": `{"schemaVersion":1,"revision":"private-secret"}`,
		"null":             `null`,
	} {
		t.Run(name, func(t *testing.T) {
			var c Config
			err := json.Unmarshal([]byte(data), &c)
			if err == nil {
				t.Fatal("ambiguous document accepted")
			}
			if strings.Contains(err.Error(), "private-secret") {
				t.Fatal("error leaked secret")
			}
		})
	}
}

func TestConfigAcceptsLegacyAndOpenPluginConfig(t *testing.T) {
	raw := `{"wan":{"mode":"dhcp"},"plugins":{"custom":{"enable":true,"config":{"arbitrary":{"items":[1,{"secret":"private-secret"}]}}}}}`
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(c)
	if err != nil || !strings.Contains(string(data), "private-secret") {
		t.Fatalf("open plugin data lost: %v", err)
	}
}

func TestLoadDraftMigratesOnceAndPreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original, _ := json.Marshal(DefaultConfig())
	original = append(original, '\n')
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	readOnly, err := LoadFromFile(path)
	if err != nil || readOnly.SchemaVersion != 0 {
		t.Fatalf("legacy read: %+v %v", readOnly, err)
	}
	c, err := LoadDraft(path)
	if err != nil || c.SchemaVersion != CurrentSchemaVersion || len(c.Revision) != 32 {
		t.Fatalf("migration: %+v %v", c, err)
	}
	backup, err := os.ReadFile(path + ".v0.bak")
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("original not preserved: %v", err)
	}
	info, _ := os.Stat(path + ".v0.bak")
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup permissions %v", info.Mode())
	}
	again, err := LoadDraft(path)
	if err != nil || again.Revision != c.Revision {
		t.Fatalf("repeat migration: %+v %v", again, err)
	}
	next, err := WithNewRevision(c)
	if err != nil || next.Revision == c.Revision {
		t.Fatal("new edit reused revision")
	}
}

func TestDraftMigrationRefusesInvalidSourceAndBackupConflict(t *testing.T) {
	for _, kind := range []string{"unknown", "conflict", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			original, _ := json.Marshal(DefaultConfig())
			if kind == "unknown" {
				original = []byte(`{"futureSetting":"private-secret"}`)
			}
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "conflict" {
				if err := os.WriteFile(path+".v0.bak", []byte("another configuration"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink" {
				if err := os.Symlink(path, path+".v0.bak"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := LoadDraft(path); err == nil {
				t.Fatal("unsafe migration accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, original) {
				t.Fatal("source overwritten")
			}
		})
	}
}

func TestConfigStrictTagsAndNumericPreservation(t *testing.T) {
	for _, raw := range []string{`{"wan":{},"WAN":{}}`, `{"system":{"hostname":"a","Hostname":"b"}}`} {
		var c Config
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Error("case alias accepted")
		}
	}
	c := DefaultConfig()
	data, _ := json.Marshal(c)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	fields["plugins"] = json.RawMessage(`{"custom":{"enable":false,"config":{"large":9007199254740993,"fraction":0.123456789012345678901,"A":1,"a":2}}}`)
	data, _ = json.Marshal(fields)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDraft(path); err != nil {
		t.Fatal(err)
	}
	migrated, _ := os.ReadFile(path)
	for _, number := range []string{"9007199254740993", "0.123456789012345678901"} {
		if !bytes.Contains(migrated, []byte(number)) {
			t.Fatalf("number changed: %s", migrated)
		}
	}
}

func TestSaveRejectsDocumentTooLargeToReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := DefaultConfig()
	if err := SaveToFile(path, c); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A compact request can fit while the durable, indented document does not.
	c.Plugins["archive"] = PluginConfig{Config: map[string]interface{}{"note": strings.Repeat("x", (16<<20)-len(original))}}
	compact, err := json.Marshal(c)
	if err != nil || len(compact) > 16<<20 {
		t.Fatalf("fixture must fit in the API request: bytes=%d err=%v", len(compact), err)
	}
	if err := SaveToFile(path, c); err == nil {
		t.Fatal("saved a document that LoadDraft cannot reload")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("oversized save changed the previous configuration")
	}
}
