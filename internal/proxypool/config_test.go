package proxypool

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSettingsFileOverridesStoreAndSaves(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := defaultSettings()
	cfg.WebAddr = "127.0.0.1:19090"
	cfg.AdminUser = "root"
	cfg.AdminPassword = "secret"
	if err := SaveSettingsFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := SyncSettingsFromFile(ctx, store, path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WebAddr != cfg.WebAddr || loaded.AdminUser != "root" || !VerifyPassword("secret", loaded) {
		t.Fatalf("config was not loaded first: %#v", loaded)
	}
	fromStore, err := store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fromStore.WebAddr != cfg.WebAddr || fromStore.AdminUser != "root" || !VerifyPassword("secret", fromStore) {
		t.Fatalf("store was not synced from config: %#v", fromStore)
	}
}

func TestMissingSettingsFileIsCreated(t *testing.T) {
	store := newTestStore(t)
	path := filepath.Join(t.TempDir(), "config.json")
	result, err := SyncSettingsFromFileDetailed(context.Background(), store, path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := result.Settings
	if !result.GeneratedAdminPasswordSet || result.GeneratedAdminPassword == "" {
		t.Fatal("expected initial admin password to be generated")
	}
	fileCfg, exists, err := LoadSettingsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("expected config file to be created")
	}
	if fileCfg.WebAddr != cfg.WebAddr || fileCfg.APIToken == "" || fileCfg.AdminPasswordHash == "" || fileCfg.AdminPassword != "" {
		t.Fatalf("unexpected config file content: %#v", fileCfg)
	}
	if !VerifyPassword(result.GeneratedAdminPassword, fileCfg) {
		t.Fatal("generated password should verify against saved hash")
	}
	if fileCfg.DomesticHealthURL == "" || fileCfg.ForeignHealthURL == "" || fileCfg.ServeReachability != "any" {
		t.Fatalf("expected reachability settings to be written: %#v", fileCfg)
	}
}
