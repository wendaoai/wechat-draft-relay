package wechatdraft_test

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

type loadTask struct {
	payload v1.Payload
	token   string
}
type loadReport struct {
	Profile                                                                           string `json:"profile"`
	OS, Arch                                                                          string
	CPUs, Workers, Queue, Clients, Requests, Accepted, Rejected, Completed, Cancelled int
	RejectCodes                                                                       map[string]int
	WallSeconds, RequestsPerSecond, CompletedPerSecond, CPUSeconds                    float64
	LatencyP95MS, LatencyP99MS, QueryP95MS, CancelP95MS                               float64
	QueueP95MS, QueueP99MS                                                            float64
	PeakHeapBytes, PeakRuntimeBytes, PeakDiskBytes, PeakReservedBytes                 int64
	PeakGoroutines, PeakOutboundConnections                                           int
	UploadBytes                                                                       int
	Notes                                                                             string
}

func TestBoundedLocalLoad(t *testing.T) {
	if os.Getenv("DRAFT_LOAD_TEST") != "1" {
		t.Skip("explicit bounded local load only")
	}
	var reports []loadReport
	for _, profile := range []struct {
		name                     string
		workers, queue, requests int
	}{{"default", 2, 4, 2000}, {"shared", 32, 64, 10000}} {
		t.Run(profile.name, func(t *testing.T) {
			w := newWX(t)
			w.delay = 5 * time.Millisecond
			c := config(t)
			c.Workers = profile.workers
			c.QueueSize = profile.queue
			c.IPWriteLimit = 50000
			c.IPQueryLimit = 50000
			c.QueryRate = 10000
			h := startHarness(t, w, c)
			report := loadReport{Profile: profile.name, OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Workers: c.Workers, Queue: c.QueueSize, Clients: 64, Requests: profile.requests, RejectCodes: map[string]int{}, UploadBytes: len(tinyImage()), Notes: "Client, GoFrame server and simulated Wechat share one process; CPU/memory are combined. Tiny PNG; 5ms simulated delay per API call. Results do not represent real Wechat capacity."}
			var mu sync.Mutex
			var latencies, queryLatencies, cancelLatencies []float64
			var accepted []loadTask
			started := time.Now()
			cpuStart := processCPUSeconds()
			stopMonitor := make(chan struct{})
			monitorDone := make(chan struct{})
			go func() {
				defer close(monitorDone)
				ticker := time.NewTicker(20 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-stopMonitor:
						return
					case <-ticker.C:
						var mem runtime.MemStats
						runtime.ReadMemStats(&mem)
						stats := h.manager.Stats()
						mu.Lock()
						report.PeakHeapBytes = max(report.PeakHeapBytes, int64(mem.HeapAlloc))
						report.PeakRuntimeBytes = max(report.PeakRuntimeBytes, int64(mem.Sys))
						report.PeakGoroutines = max(report.PeakGoroutines, runtime.NumGoroutine())
						report.PeakDiskBytes = max(report.PeakDiskBytes, stats["diskUsed"])
						report.PeakReservedBytes = max(report.PeakReservedBytes, stats["diskReserved"])
						mu.Unlock()
					}
				}
			}()
			indices := make(chan int)
			var wg sync.WaitGroup
			for client := 0; client < report.Clients; client++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for index := range indices {
						p, token, files := payload(fmt.Sprintf("wx%016x", index+1), false)
						begin := time.Now()
						status, out := h.post(p, token, files, false)
						latency := float64(time.Since(begin).Microseconds()) / 1000
						mu.Lock()
						latencies = append(latencies, latency)
						if status == 202 {
							report.Accepted++
							accepted = append(accepted, loadTask{p, token})
						} else if status == 429 {
							report.Rejected++
							report.RejectCodes[out.Error.Code]++
						} else {
							t.Errorf("unexpected load response %d %+v", status, out)
						}
						mu.Unlock()
					}
				}()
			}
			for i := 0; i < profile.requests; i++ {
				indices <- i
			}
			close(indices)
			wg.Wait()
			// Poll accepted tasks once (not at client-abusive intervals), and measure
			// query/cancel HTTP independently while remaining tasks still execute.
			for i, item := range accepted {
				begin := time.Now()
				status, out := h.get(item.payload, item.token)
				if status != 200 {
					t.Fatalf("accepted task missing %d %+v", status, out)
				}
				queryLatencies = append(queryLatencies, float64(time.Since(begin).Microseconds())/1000)
				if i < 20 {
					begin = time.Now()
					status, _, _ = h.request("POST", "/api/v1/wechat-drafts/"+item.payload.AppID+"/"+item.payload.UUID+"/cancel", item.token, item.payload.AppID, item.payload.UUID, "application/json", strings.NewReader(fmt.Sprintf(`{"expectedAttempt":%d}`, task(out).Attempt)))
					cancelLatencies = append(cancelLatencies, float64(time.Since(begin).Microseconds())/1000)
					if status != 200 && status != 409 {
						t.Fatal("cancel inaccessible", status)
					}
				}
			}
			deadline := time.Now().Add(30 * time.Second)
			for h.manager.Stats()["active"] > 0 {
				if time.Now().After(deadline) {
					t.Fatal("executor failed to drain")
				}
				time.Sleep(25 * time.Millisecond)
			}
			var queues []float64
			for _, item := range accepted {
				status, out := h.get(item.payload, item.token)
				if status != 200 {
					t.Fatal("lost accepted task")
				}
				st := task(out)
				if st.Status == "succeeded" {
					report.Completed++
					if st.Result == nil || st.Result.MediaID == "" {
						t.Fatal("fabricated success")
					}
				} else if st.Status == "cancelled" {
					report.Cancelled++
				} else {
					t.Fatalf("accepted task failed %+v", st)
				}
				// Admission-to-terminal includes execution; actual queue samples are
				// collected from persisted running transitions by the mock below.
			}
			close(stopMonitor)
			<-monitorDone
			_, _, _, drafts := w.counts()
			if drafts != report.Completed {
				t.Fatalf("remote/local mismatch %d %d", drafts, report.Completed)
			}
			if report.Accepted+report.Rejected != report.Requests {
				t.Fatal("dropped requests")
			}
			if h.manager.Stats()["diskReserved"] != 0 {
				t.Fatal("load leaked reservations")
			}
			queues = h.manager.QueueWaitSamples()
			report.WallSeconds = time.Since(started).Seconds()
			report.CPUSeconds = processCPUSeconds() - cpuStart
			report.PeakOutboundConnections = int(w.maxConnections.Load())
			report.RequestsPerSecond = float64(report.Requests) / report.WallSeconds
			report.CompletedPerSecond = float64(report.Completed) / report.WallSeconds
			report.LatencyP95MS = percentile(latencies, .95)
			report.LatencyP99MS = percentile(latencies, .99)
			report.QueryP95MS = percentile(queryLatencies, .95)
			report.CancelP95MS = percentile(cancelLatencies, .95)
			report.QueueP95MS = percentile(queues, .95)
			report.QueueP99MS = percentile(queues, .99)
			b, _ := json.Marshal(report)
			t.Log(string(b))
			reports = append(reports, report)
		})
	}
	if path := os.Getenv("DRAFT_LOAD_REPORT"); path != "" {
		b, _ := json.MarshalIndent(reports, "", "  ")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	return values[min(len(values)-1, int(float64(len(values))*p))]
}
