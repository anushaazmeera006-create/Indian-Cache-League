// ICL load tester — fires concurrent HTTP requests at the score endpoint
// and reports latency percentiles. Run:
//
//	go run ./load-testing/hammer -url http://localhost/api/matches/104/score -c 1000 -n 50000
//
// It snapshots /api/stats before and after so you can see exactly how many
// database queries the cache+singleflight stack absorbed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type statsSnapshot struct {
	DBQueries      int64 `json:"db_queries"`
	Coalesced      int64 `json:"coalesced_requests"`
	DBSaved        int64 `json:"db_queries_saved"`
	RequestsTotal  int64 `json:"requests_total"`
}

func fetchStats(base string) statsSnapshot {
	var s statsSnapshot
	resp, err := http.Get(base + "/api/stats")
	if err != nil {
		return s
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// minimal parse via encoding/json
	var full map[string]interface{}
	if jsonUnmarshal(body, &full) == nil {
		get := func(k string) int64 {
			if v, ok := full[k].(float64); ok {
				return int64(v)
			}
			return 0
		}
		s.DBQueries = get("db_queries")
		s.Coalesced = get("coalesced_requests")
		s.DBSaved = get("db_queries_saved")
		s.RequestsTotal = get("requests_total")
	}
	return s
}

func jsonUnmarshal(b []byte, v interface{}) error {
	return json.Unmarshal(b, v)
}

func main() {
	url := flag.String("url", "http://localhost/api/matches/104/score", "target URL")
	concurrency := flag.Int("c", 100, "concurrent connections")
	total := flag.Int("n", 10000, "total requests")
	statsBase := flag.String("stats", "http://localhost", "base URL for /api/stats")
	flag.Parse()

	before := fetchStats(*statsBase)

	var latencies = make([]time.Duration, *total)
	var okCount, failCount atomic.Int64
	var idx atomic.Int64

	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		MaxIdleConns:        *concurrency,
		MaxIdleConnsPerHost: *concurrency,
		MaxConnsPerHost:     *concurrency,
	}}

	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(idx.Add(1) - 1)
				if i >= *total {
					return
				}
				t0 := time.Now()
				resp, err := client.Get(*url)
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode == 200 {
						okCount.Add(1)
					} else {
						failCount.Add(1)
					}
				} else {
					failCount.Add(1)
				}
				latencies[i] = time.Since(t0)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(p float64) time.Duration {
		return latencies[int(float64(len(latencies))*p/100)]
	}
	var sum time.Duration
	for _, d := range latencies {
		sum += d
	}

	after := fetchStats(*statsBase)

	fmt.Println("================ ICL LOAD TEST RESULT ================")
	fmt.Printf("URL:          %s\n", *url)
	fmt.Printf("Concurrency:  %d\n", *concurrency)
	fmt.Printf("Requests:     %d (ok=%d fail=%d)\n", *total, okCount.Load(), failCount.Load())
	fmt.Printf("Duration:     %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Throughput:   %.0f req/s\n", float64(*total)/elapsed.Seconds())
	fmt.Printf("Avg latency:  %v\n", (sum / time.Duration(*total)).Round(time.Microsecond))
	fmt.Printf("P50:          %v\n", pct(50))
	fmt.Printf("P95:          %v\n", pct(95))
	fmt.Printf("P99:          %v\n", pct(99))
	fmt.Println("------------------------------------------------------")
	fmt.Printf("DB queries during test:      %d\n", after.DBQueries-before.DBQueries)
	fmt.Printf("Coalesced requests:          %d\n", after.Coalesced-before.Coalesced)
	fmt.Printf("DB queries saved by SF:      %d\n", after.DBSaved-before.DBSaved)
	fmt.Println("======================================================")
}
