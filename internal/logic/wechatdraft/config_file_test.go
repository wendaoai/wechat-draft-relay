package wechatdraft

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const configTestKey = "test-program-local-key-at-least-32-bytes"

func writeTestConfig(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ConfigFileName)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProgramLocalConfigDefaultsAndEnvironmentIsolation(t *testing.T) {
	t.Setenv("WECHAT_DRAFT_SERVICE_KEY", "ignored-system-key")
	t.Setenv("WECHAT_DRAFT_DATA_DIR", "ignored-system-directory")
	t.Setenv("WECHAT_DRAFT_LISTEN", "invalid")
	t.Setenv("GF_SERVER_ADDRESS", "invalid-system-address")
	t.Setenv("WECHAT_DRAFT_WORKERS", "999999")
	t.Setenv("LEGACY_CONFIG_SHOULD_STAY_UNSET", "unchanged")
	path := writeTestConfig(t, "\xef\xbb\xbf# comment\r\nexport WECHAT_DRAFT_SERVICE_KEY = '"+configTestKey+"' # private\r\nLEGACY_CONFIG_SHOULD_STAY_UNSET=modified\r\nWECHAT_DRAFT_DATA_DIR=\"data/draft files\"\r\n")
	c, err := LoadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.ServiceKey != configTestKey || c.DataDir != filepath.Join(filepath.Dir(path), "data", "draft files") || c.Workers != 2 || c.QueueSize != 4 || c.Listen != "127.0.0.1:8790" {
		t.Fatal("file configuration did not override poisoned environment or apply defaults")
	}
	if os.Getenv("LEGACY_CONFIG_SHOULD_STAY_UNSET") != "unchanged" || os.Getenv("WECHAT_DRAFT_SERVICE_KEY") != "ignored-system-key" {
		t.Fatal("file parsing modified process environment")
	}
}

func TestProgramLocalConfigResourceLimits(t *testing.T) {
	path := writeTestConfig(t, "WECHAT_DRAFT_SERVICE_KEY="+configTestKey+"\nWECHAT_DRAFT_WORKERS=32\nWECHAT_DRAFT_QUEUE_SIZE=64\nWECHAT_DRAFT_UPLOAD_CONCURRENCY=8\nWECHAT_DRAFT_QUERY_RATE_PER_SECOND=200\nWECHAT_DRAFT_IP_WRITE_LIMIT_PER_MINUTE=300\nWECHAT_DRAFT_IP_QUERY_LIMIT_PER_MINUTE=7000\nWECHAT_DRAFT_IP_CANCEL_LIMIT_PER_MINUTE=700\nWECHAT_DRAFT_CLEANUP_INTERVAL_SECONDS=1\nWECHAT_DRAFT_DISK_QUOTA_BYTES="+strconv.FormatInt(StateReserve+UploadReservation, 10)+"\n")
	c, err := LoadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Workers != 32 || c.QueueSize != 64 || c.UploadConcurrency != 8 || c.QueryRate != 200 || c.IPWriteLimit != 300 || c.IPQueryLimit != 7000 || c.IPCancelLimit != 700 || c.CleanupInterval.Seconds() != 1 || c.DiskQuota != StateReserve+UploadReservation {
		t.Fatal("configured limits were not applied")
	}
}

func TestProgramLocalConfigRejectsInvalidAndRedactsErrors(t *testing.T) {
	base := "WECHAT_DRAFT_SERVICE_KEY=" + configTestKey + "\n"
	cases := map[string]string{
		"missing key":           "WECHAT_DRAFT_LISTEN=127.0.0.1:8790\n",
		"short key":             "WECHAT_DRAFT_SERVICE_KEY=short\n",
		"blank key":             "WECHAT_DRAFT_SERVICE_KEY=\n",
		"whitespace key":        "WECHAT_DRAFT_SERVICE_KEY='" + configTestKey + " '\n",
		"unknown field":         base + "WECHAT_DRAFT_SECRET_TYPO=" + configTestKey + "\n",
		"missing equals":        base + "WECHAT_DRAFT_WORKERS\n",
		"duplicate":             base + "WECHAT_DRAFT_SERVICE_KEY=" + configTestKey + "\n",
		"unclosed quote":        "WECHAT_DRAFT_SERVICE_KEY='" + configTestKey + "\n",
		"trailing quote text":   "WECHAT_DRAFT_SERVICE_KEY='" + configTestKey + "' invalid\n",
		"bad number":            base + "WECHAT_DRAFT_WORKERS=two\n",
		"zero":                  base + "WECHAT_DRAFT_QUEUE_SIZE=0\n",
		"negative":              base + "WECHAT_DRAFT_UPLOAD_CONCURRENCY=-1\n",
		"upper limit":           base + "WECHAT_DRAFT_WORKERS=129\n",
		"empty number":          base + "WECHAT_DRAFT_QUERY_RATE_PER_SECOND=\n",
		"cleanup upper":         base + "WECHAT_DRAFT_CLEANUP_INTERVAL_SECONDS=301\n",
		"disk below reserve":    base + "WECHAT_DRAFT_DISK_QUOTA_BYTES=" + strconv.FormatInt(StateReserve+UploadReservation-1, 10) + "\n",
		"disk overflow":         base + "WECHAT_DRAFT_DISK_QUOTA_BYTES=9999999999999999999999\n",
		"listen":                base + "WECHAT_DRAFT_LISTEN=invalid\n",
		"escape":                base + "WECHAT_DRAFT_DATA_DIR=../outside\n",
		"self":                  base + "WECHAT_DRAFT_DATA_DIR=.\n",
		"unix absolute":         base + "WECHAT_DRAFT_DATA_DIR=/outside\n",
		"windows absolute":      base + "WECHAT_DRAFT_DATA_DIR=D:\\outside\n",
		"windows root relative": base + "WECHAT_DRAFT_DATA_DIR=\\outside\n",
		"large file":            base + "#" + strings.Repeat("x", 65536),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfigFile(writeTestConfig(t, body))
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if strings.Contains(err.Error(), configTestKey) || strings.Contains(err.Error(), body) {
				t.Fatal("configuration error exposed private value")
			}
		})
	}
	if _, err := LoadConfigFile(filepath.Join(t.TempDir(), ".env")); err == nil {
		t.Fatal("missing configuration accepted")
	}
	if _, err := LoadConfigFile(t.TempDir()); err == nil {
		t.Fatal("directory accepted as configuration file")
	}
}

func TestInitConfigPrivateRandomAndNeverOverwrite(t *testing.T) {
	var previous string
	for i := 0; i < 2; i++ {
		path := filepath.Join(t.TempDir(), ConfigFileName)
		if err := InitConfig(path); err != nil {
			t.Fatal(err)
		}
		c, err := LoadConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if c.Listen != ":8201" {
			t.Fatal("initialized shared address was not applied")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(c.ServiceKey)
		if err != nil || len(decoded) != 32 || len(c.ServiceKey) != 43 || c.ServiceKey == previous {
			t.Fatal("initialization did not create an independent random 32-byte key")
		}
		previous = c.ServiceKey
		before, _ := os.ReadFile(path)
		if err := InitConfig(path); err == nil {
			t.Fatal("initialization overwrote existing configuration")
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("existing configuration changed")
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("configuration permissions must be 0600")
			}
		}
	}
}

func TestConfigEnvValuesDoNotExpandOrExecute(t *testing.T) {
	values, err := parseConfigEnv([]byte("WECHAT_DRAFT_SERVICE_KEY='${PRIVATE_SECRET}$(do-not-execute)'\nWECHAT_DRAFT_DATA_DIR=data/#literal\n"))
	if err != nil || values["WECHAT_DRAFT_SERVICE_KEY"] != "${PRIVATE_SECRET}$(do-not-execute)" || values["WECHAT_DRAFT_DATA_DIR"] != "data/#literal" {
		t.Fatal("configuration unexpectedly expanded or altered a literal value")
	}
}
