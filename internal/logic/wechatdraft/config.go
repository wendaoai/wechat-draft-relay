package wechatdraft

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata"
)

const (
	MiB            int64 = 1 << 20
	MaxImagePixels       = 24_000_000
	// Transport/disk budget, independent of WeChat's content acceptance rules.
	MaxRequest        = 256 * MiB
	UploadReservation = MaxRequest + 4*MiB
	StateReserve      = 64 * MiB
)

type Config struct {
	ConfigPath string

	Listen, DataDir, ServiceKey                               string
	Workers, QueueSize, UploadConcurrency                     int
	QueryRate, IPWriteLimit, IPQueryLimit, IPCancelLimit      int
	DiskQuota                                                 int64
	CleanupInterval, QueueTimeout, UploadTimeout, TaskTimeout time.Duration
	Now                                                       func() time.Time
	// Fault is a test-only filesystem boundary hook. It cannot supply results.
	Fault func(operation, path string) error
}

func DefaultConfig() Config {
	return Config{Listen: "127.0.0.1:8790", Workers: 2, QueueSize: 4,
		UploadConcurrency: 4, QueryRate: 100, IPWriteLimit: 120,
		IPQueryLimit: 6000, IPCancelLimit: 600, DiskQuota: 5 << 30,
		CleanupInterval: 300 * time.Second, QueueTimeout: 60 * time.Second,
		UploadTimeout: 120 * time.Second, TaskTimeout: 300 * time.Second, Now: time.Now}
}

func LoadConfig() (Config, error) {
	return LoadConfigFile("")
}

func (c Config) Validate() error {
	if filepath.Clean(c.DataDir) == filepath.VolumeName(c.DataDir)+string(os.PathSeparator) {
		return errors.New("data directory must not be a filesystem root")
	}
	if !filepath.IsAbs(c.DataDir) || len(c.ServiceKey) < 32 || len(c.ServiceKey) > 4096 {
		return errors.New("absolute data directory and service key of at least 32 bytes required")
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return errors.New("invalid listen address")
	}
	if c.Workers < 1 || c.QueueSize < 1 || c.UploadConcurrency < 1 || c.DiskQuota < StateReserve+UploadReservation {
		return errors.New("invalid resource configuration")
	}
	if c.Now == nil || c.QueueTimeout <= 0 || c.UploadTimeout <= 0 || c.TaskTimeout <= 0 || c.CleanupInterval <= 0 {
		return errors.New("invalid lifecycle configuration")
	}
	return nil
}

var shanghai = func() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic("Asia/Shanghai timezone is unavailable")
	}
	return location
}()

func (c Config) now() time.Time { return c.Now().In(shanghai) }
