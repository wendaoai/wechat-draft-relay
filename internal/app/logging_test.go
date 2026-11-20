package app

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type unavailableConsole struct{}

func (unavailableConsole) Write([]byte) (int, error) { return 0, errors.New("closed console") }

func TestLoggingPersistsWithClosedConsoleAndRestart(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	previous := log.Writer()
	log.SetOutput(unavailableConsole{})
	t.Cleanup(func() { log.SetOutput(previous) })
	for _, event := range []string{"before_restart", "after_restart"} {
		restore, err := StartLogging(directory)
		if err != nil {
			t.Fatal(err)
		}
		log.Printf("logging_test event=%s", event)
		restore()
	}
	file := filepath.Join(directory+".logs", "server.log")
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), "event=before_restart") || !strings.Contains(string(data), "event=after_restart") {
		t.Fatalf("file logs lost across restart: %v %s", err, data)
	}
	if err := os.Rename(file, file+".closed"); err != nil {
		t.Fatalf("logger retained file after shutdown: %v", err)
	}
}

func TestLoggingRejectsUnwritableDestination(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(directory+".logs", []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := log.Writer()
	if _, err := StartLogging(directory); err == nil || log.Writer() != previous {
		t.Fatal("failed initialization changed the running logger")
	}
}
