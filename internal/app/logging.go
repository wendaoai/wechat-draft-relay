package app

import (
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gogf/gf/v2/os/gfpool"
	"github.com/gogf/gf/v2/os/glog"
)

// StartLogging preserves stderr and uses GoFrame's bounded file rotation.
// Call once before serving, and restore only after requests have stopped.
func StartLogging(dataDir string) (restore func(), err error) {
	// Keep logs outside the draft store's strictly validated task namespace.
	directory := filepath.Clean(dataDir) + ".logs"
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(directory); statErr != nil || !info.IsDir() {
		return nil, errors.New("unsafe log directory")
	}
	filePath := filepath.Join(directory, "server.log")
	if info, statErr := os.Lstat(filePath); statErr != nil && !os.IsNotExist(statErr) || statErr == nil && !info.Mode().IsRegular() {
		return nil, errors.New("unsafe log file")
	}
	const flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	probe, err := os.OpenFile(filePath, flags, 0600)
	if err != nil {
		return nil, err
	}
	if err = probe.Close(); err != nil {
		return nil, err
	}
	logger := glog.New()
	c := glog.DefaultConfig()
	c.Path, c.File = directory, "server.log"
	c.HeaderPrint, c.StdoutPrint = false, false
	c.RotateSize, c.RotateBackupLimit = 10<<20, 3
	c.RotateCheckInterval = time.Minute
	if err = logger.SetConfig(c); err != nil {
		return nil, err
	}
	previous := log.Writer()
	log.SetOutput(io.MultiWriter(logger, previous))
	log.Print("server_logging_ready trace_version=2 log_file=<data_dir>.logs/server.log rotation_mb=10 backups=3")
	return func() {
		log.SetOutput(previous)
		// GoFrame pools its log file for one minute; release it at shutdown on Windows too.
		if file := gfpool.Get(filePath, flags, 0666, time.Minute); file != nil {
			_ = file.Close(true)
		}
	}, nil
}
