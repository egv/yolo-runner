package opencode

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultWatchdogTimeout = 10 * time.Minute
const defaultWatchdogInterval = 5 * time.Second
const defaultWatchdogLogTail = 20

var defaultHomeDir = os.UserHomeDir

const (
	stallPermission = "permission"
	stallQuestion   = "question"
	stallNoOutput   = "no_output"
)

type Process interface {
	Wait() error
	Kill() error
}

type WatchdogConfig struct {
	LogPath        string
	OpenCodeLogDir string
	Timeout        time.Duration
	Interval       time.Duration
	TailLines      int
	Now            func() time.Time
	Tick           <-chan time.Time
}

type Watchdog struct {
	config WatchdogConfig
}

type StallError struct {
	Category      string
	OpenCodeLog   string
	SessionID     string
	LogPath       string
	LastOutputAge time.Duration
	Tail          []string
}

func (err *StallError) Error() string {
	parts := []string{
		"opencode stall",
		"category=" + err.Category,
	}
	if err.LogPath != "" {
		parts = append(parts, "runner_log="+err.LogPath)
	}
	if err.OpenCodeLog != "" {
		parts = append(parts, "opencode_log="+err.OpenCodeLog)
	}
	if err.SessionID != "" {
		parts = append(parts, "session="+err.SessionID)
	}
	if err.LastOutputAge > 0 {
		parts = append(parts, fmt.Sprintf("last_output_age=%s", err.LastOutputAge))
	}
	if len(err.Tail) > 0 {
		parts = append(parts, "opencode_tail="+strings.Join(err.Tail, " | "))
	}
	return strings.Join(parts, " ")
}

func NewWatchdog(config WatchdogConfig) *Watchdog {
	return &Watchdog{config: config}
}

func (watchdog *Watchdog) Monitor(process Process) error {
	if process == nil {
		return errors.New("watchdog requires process")
	}
	config := watchdog.config
	if config.Timeout <= 0 {
		config.Timeout = defaultWatchdogTimeout
	}
	if config.Interval <= 0 {
		config.Interval = defaultWatchdogInterval
	}
	if config.TailLines <= 0 {
		config.TailLines = defaultWatchdogLogTail
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.OpenCodeLogDir == "" {
		home, err := defaultHomeDir()
		if err == nil {
			config.OpenCodeLogDir = filepath.Join(home, ".local", "share", "opencode", "log")
		}
	}

	lastOutput, err := fileModTime(config.LogPath)
	if err != nil {
		lastOutput = config.Now()
	}
	lastSize := fileSize(config.LogPath)
	startTime := config.Now()
	if config.LogPath == "" {
		lastOutput = startTime
	}

	done := make(chan error, 1)
	go func() {
		done <- process.Wait()
	}()

	select {
	case err := <-done:
		return err
	default:
	}

	tick := config.Tick
	var ticker *time.Ticker
	if tick == nil {
		ticker = time.NewTicker(config.Interval)
		defer ticker.Stop()
		tick = ticker.C
	}

	for {
		select {
		case err := <-done:
			return err
		case <-tick:
			currentTime := config.Now()
			currentSize := fileSize(config.LogPath)
			if currentSize > lastSize {
				lastSize = currentSize
				if modTime, modErr := fileModTime(config.LogPath); modErr == nil {
					lastOutput = modTime
				} else {
					lastOutput = currentTime
				}
			}
			if currentTime.Sub(lastOutput) > config.Timeout {
				select {
				case err := <-done:
					return err
				default:
				}
				runtime.Gosched()
				select {
				case err := <-done:
					return err
				default:
				}
				stall := classifyStall(config, currentTime, lastOutput)
				_ = process.Kill()
				return stall
			}
		}
	}
}

func classifyStall(config WatchdogConfig, now time.Time, lastOutput time.Time) *StallError {
	latestLog := latestLogPath(config.OpenCodeLogDir)
	lines := tailLines(latestLog, config.TailLines)
	category := stallNoOutput
	for _, line := range lines {
		if strings.Contains(line, "service=permission") || strings.Contains(line, "permission=doom_loop") {
			category = stallPermission
			break
		}
		if strings.Contains(line, "service=question") || strings.Contains(line, "permission=question") {
			category = stallQuestion
			break
		}
	}
	stall := &StallError{
		Category:      category,
		OpenCodeLog:   latestLog,
		SessionID:     extractSessionID(lines),
		LogPath:       config.LogPath,
		LastOutputAge: now.Sub(lastOutput),
		Tail:          lines,
	}
	return stall
}

func latestLogPath(dir string) string {
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var latest string
	var latestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime()
		if latest == "" || mod.After(latestTime) {
			latest = filepath.Join(dir, entry.Name())
			latestTime = mod
		}
	}
	return latest
}

func tailLines(path string, limit int) []string {
	if path == "" || limit <= 0 {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	lines := []string{}
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > limit {
			lines = lines[1:]
		}
	}
	return lines
}

func extractSessionID(lines []string) string {
	for _, line := range lines {
		if strings.Contains(line, "sessionID=") {
			parts := strings.Split(line, "sessionID=")
			if len(parts) > 1 {
				rest := parts[1]
				for i, r := range rest {
					if r == ' ' || r == '\t' || r == ',' {
						return rest[:i]
					}
				}
				return rest
			}
		}
		if strings.Contains(line, "session id=") {
			parts := strings.Split(line, "session id=")
			if len(parts) > 1 {
				rest := parts[1]
				for i, r := range rest {
					if r == ' ' || r == '\t' || r == ',' {
						return rest[:i]
					}
				}
				return rest
			}
		}
	}
	return ""
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func fileModTime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
