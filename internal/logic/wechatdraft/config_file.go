package wechatdraft

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ConfigFileName = ".env"

// The default belongs to the actual executable, independently of the working
// directory. An explicit path supports source development with go run.
func ConfigFilePath(path string) (string, error) {
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", errors.New("cannot locate executable")
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return "", errors.New("cannot resolve executable")
		}
		path = filepath.Join(filepath.Dir(exe), ConfigFileName)
	}
	return filepath.Abs(path)
}

// This reads only the file; it neither imports nor mutates process environment.
func LoadConfigFile(path string) (Config, error) {
	c := DefaultConfig()
	path, err := ConfigFilePath(path)
	if err != nil {
		return c, err
	}
	c.ConfigPath = path
	guard := Store{config: Config{DataDir: filepath.Dir(path)}}
	if guard.safe(path) != nil {
		return c, errors.New("unsafe configuration path")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return c, errors.New("configuration must be an existing regular .env file")
	}
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("cannot read .env; initialize configuration first")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(b) > 65536 {
		return c, errors.New("configuration file exceeds 64 KiB or cannot be read")
	}
	settings, err := parseConfigEnv(b)
	if err != nil {
		return c, err
	}
	c.ServiceKey = settings["WECHAT_DRAFT_SERVICE_KEY"]
	if value := settings["GF_SERVER_ADDRESS"]; value != "" {
		c.Listen = value
	}
	if value := settings["WECHAT_DRAFT_LISTEN"]; value != "" {
		if shared := settings["GF_SERVER_ADDRESS"]; shared != "" && shared != value {
			return c, errors.New("GF_SERVER_ADDRESS and WECHAT_DRAFT_LISTEN must agree; configure only one")
		}
		c.Listen = value
	}
	dataDir := settings["WECHAT_DRAFT_DATA_DIR"]
	if dataDir == "" {
		dataDir = "data/wechat-drafts"
	}
	if filepath.IsAbs(dataDir) || filepath.VolumeName(dataDir) != "" || strings.HasPrefix(dataDir, "/") || strings.HasPrefix(dataDir, "\\") || strings.Contains(dataDir, ":") {
		return c, errors.New("data directory must be relative to the configuration directory")
	}
	base := filepath.Dir(path)
	c.DataDir = filepath.Join(base, dataDir)
	rel, err := filepath.Rel(base, c.DataDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return c, errors.New("data directory must stay inside the program configuration directory")
	}
	limits := []struct {
		key    string
		target *int
		max    int
	}{
		{"WORKERS", &c.Workers, 128}, {"QUEUE_SIZE", &c.QueueSize, 1024},
		{"UPLOAD_CONCURRENCY", &c.UploadConcurrency, 64}, {"QUERY_RATE_PER_SECOND", &c.QueryRate, 10000},
		{"IP_WRITE_LIMIT_PER_MINUTE", &c.IPWriteLimit, 100000}, {"IP_QUERY_LIMIT_PER_MINUTE", &c.IPQueryLimit, 1000000},
		{"IP_CANCEL_LIMIT_PER_MINUTE", &c.IPCancelLimit, 100000},
	}
	for _, item := range limits {
		if value, exists := settings["WECHAT_DRAFT_"+item.key]; exists {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > item.max {
				return c, errors.New("configuration resource limit is out of range")
			}
			*item.target = n
		}
	}
	if value, exists := settings["WECHAT_DRAFT_CLEANUP_INTERVAL_SECONDS"]; exists {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 300 {
			return c, errors.New("cleanup interval must be between 1 and 300 seconds")
		}
		c.CleanupInterval = time.Duration(n) * time.Second
	}
	if value, exists := settings["WECHAT_DRAFT_DISK_QUOTA_BYTES"]; exists {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < StateReserve+UploadReservation || n > 1<<50 {
			return c, errors.New("disk quota is out of range")
		}
		c.DiskQuota = n
	}
	if strings.IndexFunc(c.ServiceKey, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return c, errors.New("service key must contain visible ASCII characters without whitespace")
	}
	return c, c.Validate()
}
