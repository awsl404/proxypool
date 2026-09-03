package proxypool

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	logDebug = iota
	logInfo
	logWarn
	logError
	logOff
)

type AppLogger struct {
	mu    sync.Mutex
	level int
	path  string
	file  *os.File
}

var appLogger = &AppLogger{level: logInfo}

func InitLogger(cfg Settings) error {
	log.SetFlags(0)
	return appLogger.Configure(cfg.LogLevel, cfg.LogFile)
}

func (l *AppLogger) Configure(level, path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = parseLogLevel(level)
	if path == "" {
		path = defaultSettings().LogFile
	}
	if l.file != nil && l.path != path {
		_ = l.file.Close()
		l.file = nil
	}
	l.path = path
	if l.level == logOff {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if l.file == nil {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		l.file = f
	}
	return nil
}

func (l *AppLogger) Log(level int, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if level < l.level || l.level == logOff {
		return
	}
	line := fmt.Sprintf("%s [%s] %s", time.Now().Format("2006-01-02 15:04:05"), logLevelName(level), fmt.Sprintf(format, args...))
	log.Print(line)
	if l.file != nil {
		_, _ = l.file.WriteString(line + "\n")
	}
}

func (l *AppLogger) Truncate(path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if path == "" {
		path = defaultSettings().LogFile
	}
	if l.file != nil && l.path == path {
		if err := l.file.Truncate(0); err != nil {
			return err
		}
		_, err := l.file.Seek(0, 0)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

func (l *AppLogger) TrimRetention(path string, days int) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if path == "" {
		path = defaultSettings().LogFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	removed := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		if len(line) >= 19 {
			if ts, err := time.ParseInLocation("2006-01-02 15:04:05", line[:19], time.Local); err == nil && ts.Before(cutoff) {
				removed++
				continue
			}
		}
		kept = append(kept, line)
	}
	if removed == 0 {
		return 0, nil
	}
	content := strings.Join(kept, "\n")
	if content != "" {
		content += "\n"
	}
	if l.file != nil && l.path == path {
		if err := l.file.Truncate(0); err != nil {
			return 0, err
		}
		if _, err := l.file.Seek(0, 0); err != nil {
			return 0, err
		}
		if _, err := l.file.WriteString(content); err != nil {
			return 0, err
		}
		return removed, nil
	}
	return removed, os.WriteFile(path, []byte(content), 0644)
}

func normalizeLogLevel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug", "info", "warn", "error", "off":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "info"
	}
}

func parseLogLevel(v string) int {
	switch normalizeLogLevel(v) {
	case "debug":
		return logDebug
	case "warn":
		return logWarn
	case "error":
		return logError
	case "off":
		return logOff
	default:
		return logInfo
	}
}

func logLevelName(level int) string {
	switch level {
	case logDebug:
		return "DEBUG"
	case logWarn:
		return "WARN"
	case logError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func appDebug(format string, args ...any) { appLogger.Log(logDebug, format, args...) }
func appInfo(format string, args ...any)  { appLogger.Log(logInfo, format, args...) }
func appWarn(format string, args ...any)  { appLogger.Log(logWarn, format, args...) }
func appError(format string, args ...any) { appLogger.Log(logError, format, args...) }
