package wechatdraft_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
	controller "github.com/wendaoai/wechat-draft-relay/internal/controller/wechatdraft"
	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

// The helper exits at real I/O boundaries, without graceful shutdown. Its
// request still passes through the same GoFrame server and multipart decoder.
func TestCrashHelper(t *testing.T) {
	if os.Getenv("DRAFT_CRASH_HELPER") != "1" {
		t.Skip("subprocess-only helper")
	}
	c := logic.DefaultConfig()
	c.Listen = "127.0.0.1:0"
	c.DataDir = os.Getenv("DRAFT_CRASH_DIR")
	c.ServiceKey = testKey
	point := os.Getenv("DRAFT_CRASH_POINT")
	c.Fault = func(op, path string) error {
		stop := false
		switch point {
		case "image":
			stop = op == "imagewrite"
		case "snapshot-state":
			stop = op == "write" && strings.Contains(path, ".staging") && strings.HasSuffix(path, "status.json")
		case "before-commit":
			stop = op == "commit"
		case "after-commit":
			stop = op == "commitsync"
		case "getting-token":
			if op == "write" && strings.HasSuffix(path, "status.json") && !strings.Contains(path, ".staging") {
				stop = true
			}
		case "submitting":
			if op == "write" && strings.HasSuffix(path, "status.json") && !strings.Contains(path, ".staging") {
				b, _ := os.ReadFile(path)
				stop = bytes.Contains(b, []byte(`"phase":"preparing_content"`))
			}
		case "after-draft":
			if op == "write" && strings.HasSuffix(path, "status.json") && !strings.Contains(path, ".staging") {
				b, _ := os.ReadFile(path)
				stop = bytes.Contains(b, []byte(`"phase":"submitting_draft"`))
			}
		case "submitting-mark":
			if op == "dirsync" && strings.HasSuffix(path, "status.json") && !strings.Contains(path, ".staging") {
				b, _ := os.ReadFile(path)
				stop = bytes.Contains(b, []byte(`"phase":"submitting_draft"`))
			}
		}
		if stop {
			os.Exit(73)
		}
		return nil
	}
	m, err := logic.NewManager(context.Background(), c, logic.NewLocalWechat(os.Getenv("DRAFT_CRASH_WX")))
	if err != nil {
		t.Fatal(err)
	}
	s := ghttp.GetServer("crash-helper")
	controller.Configure(s, c)
	controller.Bind(s, m, testKey)
	if s.Start() != nil {
		t.Fatal("start")
	}
	var p v1.Payload
	if json.Unmarshal([]byte(os.Getenv("DRAFT_CRASH_PAYLOAD")), &p) != nil {
		t.Fatal("payload")
	}
	files := map[string][]byte{"file_cover": tinyImage(), "file_body": tinyImage()}
	b, ct := multipartBody(p, files)
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/v1/wechat-drafts", s.GetListenedPort()), bytes.NewReader(b))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("X-Draft-Token", os.Getenv("DRAFT_CRASH_TOKEN"))
	req.Header.Set("X-Wechat-AppId", p.AppID)
	req.Header.Set("X-Draft-UUID", p.UUID)
	res, err := http.DefaultClient.Do(req)
	if err == nil {
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	select {
	case <-time.After(5 * time.Second):
		t.Fatal("crash boundary was not reached")
	}
}

func TestHTTPProcessCrashRecoveryBoundaries(t *testing.T) {
	for _, point := range []string{"image", "snapshot-state", "before-commit", "after-commit", "getting-token", "submitting", "submitting-mark", "after-draft"} {
		t.Run(point, func(t *testing.T) {
			w := newWX(t)
			c := config(t)
			p, token, files := payload("wx0123456789abcdef", true)
			encoded, _ := json.Marshal(p)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashHelper$")
			cmd.Env = append(os.Environ(), "DRAFT_CRASH_HELPER=1", "DRAFT_CRASH_DIR="+c.DataDir, "DRAFT_CRASH_POINT="+point, "DRAFT_CRASH_WX="+w.server.URL, "DRAFT_CRASH_PAYLOAD="+string(encoded), "DRAFT_CRASH_TOKEN="+token)
			output, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 73 {
				t.Fatalf("crash helper %v %s", err, output)
			}
			for _, secret := range []string{testSecret, testKey, token} {
				if bytes.Contains(output, []byte(secret)) {
					t.Fatal("credential in crash log")
				}
			}
			h := startHarness(t, w, c)
			s, out := h.get(p, token)
			switch point {
			case "image", "snapshot-state", "before-commit":
				expect(t, s, out, 404, "TASK_NOT_FOUND")
				entries, _ := os.ReadDir(filepath.Join(c.DataDir, ".staging"))
				if len(entries) != 0 {
					t.Fatal("crashed staging persisted")
				}
			case "after-draft", "submitting-mark":
				expect(t, s, out, 200, "")
				if task(out).Status != "uncertain" || task(out).RetryAllowed {
					t.Fatal("submission was repeated")
				}
				_, _, _, drafts := w.counts()
				want := 1
				if point == "submitting-mark" {
					want = 0
				}
				if drafts != want {
					t.Fatal("remote result altered")
				}
			default:
				expect(t, s, out, 200, "")
				if task(out).Status != "failed" || task(out).Error.Code != "SERVER_RESTARTED" {
					t.Fatalf("recovery %+v", task(out))
				}
			}
			_, _, _, before := w.counts()
			s, out = h.post(p, token, files, false)
			if point == "image" || point == "snapshot-state" || point == "before-commit" {
				expect(t, s, out, 202, "")
				h.wait(p, token, "succeeded")
			} else {
				expect(t, s, out, 200, "")
				_, _, _, after := w.counts()
				if before != after {
					t.Fatal("restart replay executed")
				}
			}
		})
	}
}
