package wechatdraft

import (
	"bytes"
	"errors"
	"strings"
)

// A deliberately bounded dotenv subset: one assignment per line, optional
// export/quotes/comments, UTF-8 BOM and CRLF. No shell or variable expansion.
// GF_SERVER_ADDRESS is the shared listen setting. Other legacy non-draft keys
// are ignored, never installed in the environment.
func parseConfigEnv(data []byte) (map[string]string, error) {
	known := map[string]bool{
		"DATA_DIR": true, "SERVICE_KEY": true, "LISTEN": true,
		"WORKERS": true, "QUEUE_SIZE": true, "UPLOAD_CONCURRENCY": true,
		"QUERY_RATE_PER_SECOND": true, "IP_WRITE_LIMIT_PER_MINUTE": true,
		"IP_QUERY_LIMIT_PER_MINUTE": true, "IP_CANCEL_LIMIT_PER_MINUTE": true,
		"CLEANUP_INTERVAL_SECONDS": true, "DISK_QUOTA_BYTES": true,
	}
	settings := make(map[string]string)

	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[6:])
		}
		key, value, assignment := strings.Cut(line, "=")
		key = strings.TrimSpace(key)

		if key != "GF_SERVER_ADDRESS" && !strings.HasPrefix(key, "WECHAT_DRAFT_") {
			continue
		}
		if !assignment || (key != "GF_SERVER_ADDRESS" && !known[strings.TrimPrefix(key, "WECHAT_DRAFT_")]) {
			return nil, errors.New("unknown or malformed draft configuration field")
		}
		if _, exists := settings[key]; exists {
			return nil, errors.New("duplicate draft configuration field")
		}
		value, valid := configEnvValue(value)
		if !valid {
			return nil, errors.New("invalid draft configuration value")
		}
		settings[key] = value
	}
	return settings, nil
}

func configEnvValue(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	if value[0] == '\'' || value[0] == '"' {
		end := strings.IndexByte(value[1:], value[0])
		if end < 0 {
			return "", false
		}
		end++
		rest := strings.TrimSpace(value[end+1:])
		if rest != "" && !strings.HasPrefix(rest, "#") {
			return "", false
		}
		return value[1:end], true
	}
	for i := 1; i < len(value); i++ {
		if value[i] == '#' && (value[i-1] == ' ' || value[i-1] == '\t') {
			value = strings.TrimSpace(value[:i])
			break
		}
	}
	if strings.ContainsAny(value, "\x00\r\n\"'") {
		return "", false
	}
	return value, true
}
