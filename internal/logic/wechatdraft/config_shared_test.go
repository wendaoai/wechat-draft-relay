package wechatdraft

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedServerAddressAndLegacyAccountConfiguration(t *testing.T) {
	t.Setenv("GF_SERVER_ADDRESS", "invalid-system-address")
	base := "WECHAT_DRAFT_SERVICE_KEY=" + configTestKey + "\n"
	cases := []struct {
		name, values, want string
		invalid            bool
	}{
		{"shared", "GF_SERVER_ADDRESS=:8201\n", ":8201", false},
		{"quoted", "export GF_SERVER_ADDRESS='127.0.0.1:8201' # comment\n", "127.0.0.1:8201", false},
		{"compatibility", "WECHAT_DRAFT_LISTEN=127.0.0.1:8202\n", "127.0.0.1:8202", false},
		{"same aliases", "GF_SERVER_ADDRESS=:8201\nWECHAT_DRAFT_LISTEN=:8201\n", ":8201", false},
		{"conflict", "GF_SERVER_ADDRESS=:8201\nWECHAT_DRAFT_LISTEN=:8790\n", "", true},
		{"duplicate", "GF_SERVER_ADDRESS=:8201\nGF_SERVER_ADDRESS=:8201\n", "", true},
		{"missing equals", "GF_SERVER_ADDRESS\n", "", true},
		{"invalid address", "GF_SERVER_ADDRESS=invalid\n", "", true},
		{"malformed quotes", "GF_SERVER_ADDRESS=':8201\n", "", true},
		{"absent", "", "127.0.0.1:8790", false},
		{"legacy account options", "GF_SERVER_ADDRESS=:8201\nGF_SECURITY_JWTSECRET=ignored-secret\nGF_SECURITY_COOKIESECURE=false\nGF_SECURITY_EXPOSERESETTOKEN=true\nGF_APP_MODE=saas\nGF_APP_SINGLETENANT=true\nGF_APP_ALLOWREGISTRATION=true\n", ":8201", false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			c, err := LoadConfigFile(writeTestConfig(t, base+item.values))
			if item.invalid {
				if err == nil {
					t.Fatal("invalid shared address configuration accepted")
				}
				if strings.Contains(err.Error(), configTestKey) {
					t.Fatal("configuration error exposed service key")
				}
				return
			}
			if err != nil || c.Listen != item.want || c.ServiceKey != configTestKey {
				t.Fatal("shared configuration was ignored or legacy options changed authentication")
			}
		})
	}
}

func TestConfigExamplesMatchInitialization(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(data), "\r\n", "\n") != defaultEnvText("") {
		t.Fatal(".env.example differs from initialization; update both")
	}
	settings, err := parseConfigEnv([]byte(defaultEnvText(configTestKey)))
	if err != nil || settings["GF_SERVER_ADDRESS"] != ":8201" || len(settings) != 12 {
		t.Fatal("initialization template does not contain exactly the active documented fields")
	}
}
