package wechatdraft_test

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

func TestBoundedLocalSameKeyBurst(t *testing.T) {
	if os.Getenv("DRAFT_LOAD_TEST") != "1" {
		t.Skip("explicit bounded local load only")
	}
	w := newWX(t)
	w.delay = 5 * time.Millisecond
	c := config(t)
	c.IPWriteLimit = 5000
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", true)
	var mu sync.Mutex
	responses := map[int]int{}
	codes := map[string]int{}
	var latencies []float64
	jobs := make(chan struct{})
	var wg sync.WaitGroup
	started := time.Now()
	for client := 0; client < 64; client++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				begin := time.Now()
				s, out := h.post(p, token, files, false)
				if s != 202 && s != 200 && s != 409 && s != 429 {
					t.Errorf("unexpected same-key response %d %+v", s, out)
				}
				if s == 409 && (out.Error == nil || out.Error.Code != "CREATE_IN_PROGRESS") {
					t.Errorf("unexpected same-key conflict %+v", out.Error)
				}
				mu.Lock()
				responses[s]++
				latencies = append(latencies, float64(time.Since(begin).Microseconds())/1000)
				if out.Error != nil {
					codes[out.Error.Code]++
				}
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < 1000; i++ {
		jobs <- struct{}{}
	}
	close(jobs)
	wg.Wait()
	h.wait(p, token, "succeeded")
	_, cover, body, drafts := w.counts()
	if responses[202] != 1 || drafts != 1 || cover != 1 || body != 1 || h.manager.Stats()["diskReserved"] != 0 {
		t.Fatalf("duplicate work or leaked budget: responses=%v cover=%d body=%d drafts=%d", responses, cover, body, drafts)
	}
	report := map[string]any{"requests": 1000, "clients": 64, "responses": responses,
		"errorCodes": codes, "createdDrafts": drafts, "wallSeconds": time.Since(started).Seconds(),
		"latencyP95MS": percentile(latencies, .95), "latencyP99MS": percentile(latencies, .99),
		"note": "Same immutable task and token, actual HTTP/FS executor; loopback Wechat only."}
	b, _ := json.MarshalIndent(report, "", "  ")
	t.Log(string(b))
	if path := os.Getenv("DRAFT_DUPLICATE_REPORT"); path != "" {
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
