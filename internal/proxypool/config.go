package proxypool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type SettingsSyncResult struct {
	Settings                  Settings
	GeneratedAdminPassword    string
	GeneratedAdminPasswordSet bool
}

func defaultConfigPath() string {
	return "config.json"
}

func LoadSettingsFile(path string) (Settings, bool, error) {
	cfg := defaultSettings()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, false, nil
		}
		return cfg, false, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, true, err
	}
	return normalizeSettings(cfg), true, nil
}

func SaveSettingsFile(path string, cfg Settings) error {
	cfg = normalizeSettings(cfg)
	if cfg.AdminPasswordHash == "" && cfg.AdminPassword != "" {
		hash, err := HashPassword(cfg.AdminPassword)
		if err != nil {
			return err
		}
		cfg.AdminPasswordHash = hash
	}
	cfg.AdminPassword = ""
	if err := os.MkdirAll(configDir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func SyncSettingsFromFile(ctx context.Context, store *Store, path string) (Settings, error) {
	result, err := SyncSettingsFromFileDetailed(ctx, store, path)
	return result.Settings, err
}

func SyncSettingsFromFileDetailed(ctx context.Context, store *Store, path string) (SettingsSyncResult, error) {
	fileCfg, exists, err := LoadSettingsFile(path)
	if err != nil {
		return SettingsSyncResult{}, err
	}
	var generated string
	if exists {
		if err := store.UpdateSettings(ctx, fileCfg); err != nil {
			return SettingsSyncResult{}, err
		}
		cfg, err := store.Settings(ctx)
		if err != nil {
			return SettingsSyncResult{}, err
		}
		return SettingsSyncResult{Settings: cfg}, SaveSettingsFile(path, cfg)
	}
	cfg, err := store.Settings(ctx)
	if err != nil {
		return SettingsSyncResult{}, err
	}
	if cfg.AdminPassword == "" && cfg.AdminPasswordHash == "" {
		generated, err = RandomPassword()
		if err != nil {
			return SettingsSyncResult{}, fmt.Errorf("generate admin password: %w", err)
		}
		cfg.AdminPassword = generated
	}
	if err := store.UpdateSettings(ctx, cfg); err != nil {
		return SettingsSyncResult{}, err
	}
	saved, err := store.Settings(ctx)
	if err != nil {
		return SettingsSyncResult{}, err
	}
	return SettingsSyncResult{
		Settings:                  saved,
		GeneratedAdminPassword:    generated,
		GeneratedAdminPasswordSet: generated != "",
	}, SaveSettingsFile(path, saved)
}

func normalizeSettings(cfg Settings) Settings {
	def := defaultSettings()
	if cfg.WebAddr == "" {
		cfg.WebAddr = def.WebAddr
	}
	if cfg.HTTPProxyAddr == "" {
		cfg.HTTPProxyAddr = def.HTTPProxyAddr
	}
	if cfg.SOCKSProxyAddr == "" {
		cfg.SOCKSProxyAddr = def.SOCKSProxyAddr
	}
	if cfg.AdminUser == "" {
		cfg.AdminUser = def.AdminUser
	}
	if cfg.APIToken == "" {
		cfg.APIToken = def.APIToken
	}
	if cfg.HealthURL == "" {
		cfg.HealthURL = def.HealthURL
	}
	if cfg.DomesticHealthURL == "" {
		cfg.DomesticHealthURL = def.DomesticHealthURL
	}
	if cfg.ForeignHealthURL == "" {
		cfg.ForeignHealthURL = cfg.HealthURL
	}
	if cfg.AnonymityURL == "" {
		cfg.AnonymityURL = def.AnonymityURL
	}
	cfg.ServeAnonymity = normalizeAnonymityFilter(cfg.ServeAnonymity)
	cfg.ServeReachability = normalizeReachabilityFilter(cfg.ServeReachability)
	cfg.ServeStrategy = normalizeServeStrategy(cfg.ServeStrategy)
	if cfg.CollectorSources == "" {
		cfg.CollectorSources = def.CollectorSources
	}
	cfg.CollectorMode = normalizeCollectorMode(cfg.CollectorMode)
	if cfg.CheckConcurrency <= 0 {
		cfg.CheckConcurrency = def.CheckConcurrency
	}
	if cfg.CheckIntervalSeconds <= 0 {
		cfg.CheckIntervalSeconds = def.CheckIntervalSeconds
	}
	cfg.CheckScheduleScope = normalizeCheckScope(cfg.CheckScheduleScope)
	cfg.CheckScheduleSchemes = normalizeCheckSchemes(cfg.CheckScheduleSchemes)
	if cfg.FailThreshold <= 0 {
		cfg.FailThreshold = def.FailThreshold
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = def.TimeoutSeconds
	}
	if cfg.MaintenanceInterval <= 0 {
		cfg.MaintenanceInterval = def.MaintenanceInterval
	}
	if cfg.LogRetentionDays < 0 {
		cfg.LogRetentionDays = def.LogRetentionDays
	}
	if cfg.SourceRetentionDays < 0 {
		cfg.SourceRetentionDays = def.SourceRetentionDays
	}
	if cfg.BackupDir == "" {
		cfg.BackupDir = def.BackupDir
	}
	if cfg.ScheduleInterval <= 0 {
		cfg.ScheduleInterval = def.ScheduleInterval
	}
	if cfg.ScheduleMaxRunsDay <= 0 {
		cfg.ScheduleMaxRunsDay = def.ScheduleMaxRunsDay
	}
	if cfg.SchedulePauseMinutes < 0 {
		cfg.SchedulePauseMinutes = def.SchedulePauseMinutes
	}
	cfg.LogLevel = normalizeLogLevel(cfg.LogLevel)
	if cfg.LogFile == "" {
		cfg.LogFile = def.LogFile
	}
	return cfg
}

func configDir(path string) string {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return "."
	}
	return dir
}
