package proxypool

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func Run() {
	db := flag.String("db", dbPath(), "SQLite database path")
	configPath := flag.String("config", defaultConfigPath(), "JSON config file path")
	flag.Parse()

	store, err := OpenStore(*db)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	syncResult, err := SyncSettingsFromFileDetailed(context.Background(), store, *configPath)
	if err != nil {
		log.Fatalf("load config/settings: %v", err)
	}
	cfg := syncResult.Settings
	if err := InitLogger(cfg); err != nil {
		log.Fatalf("init logger: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool := NewProxyPool(store)
	StartHealthLoop(ctx, store)
	StartMaintenanceLoop(ctx, store)
	StartCollectorScheduler(ctx, store, pool)
	StartWeb(ctx, cfg.WebAddr, store, pool, *configPath)
	StartHTTPProxy(ctx, cfg.HTTPProxyAddr, store, pool)
	StartSOCKS5Proxy(ctx, cfg.SOCKSProxyAddr, store, pool)

	appInfo("admin user: %s", cfg.AdminUser)
	if syncResult.GeneratedAdminPasswordSet {
		credentialPath := initialCredentialPath(*configPath)
		if err := writeInitialCredentialFile(credentialPath, cfg.AdminUser, syncResult.GeneratedAdminPassword); err != nil {
			appWarn("failed to write initial admin password file: %v", err)
		}
		printInitialCredentials(cfg.AdminUser, syncResult.GeneratedAdminPassword, credentialPath, *configPath)
	}
	appInfo("api token configured")
	<-ctx.Done()
	appInfo("shutting down")
}

func printInitialCredentials(user, password, credentialPath, configPath string) {
	log.Printf("")
	log.Printf("============================================================")
	log.Printf("Initial admin credentials")
	log.Printf("Username: %s", user)
	log.Printf("Password: %s", password)
	log.Printf("Saved once to: %s", credentialPath)
	log.Printf("Only the password hash is stored in: %s", configPath)
	log.Printf("Delete the one-time password file after saving the password.")
	log.Printf("============================================================")
	log.Printf("")
	appWarn("generated initial admin password for user %s; one-time file=%s", user, credentialPath)
}

func initialCredentialPath(configPath string) string {
	dir := configDir(configPath)
	return filepath.Join(dir, "initial-admin-password.txt")
}

func writeInitialCredentialFile(path, user, password string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	content := "Initial admin credentials\n" +
		"Username: " + user + "\n" +
		"Password: " + password + "\n\n" +
		"Only the password hash is stored in config.json. Delete this file after saving the password.\n"
	return os.WriteFile(path, []byte(content), 0600)
}
