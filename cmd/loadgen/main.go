// Command loadgen hammers /v1/match and reports latency percentiles, so the
// README's numbers are measured rather than claimed.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sync"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8080/v1/match", "match endpoint")
	token := flag.String("token", "dev-token", "bearer token")
	workers := flag.Int("c", 32, "concurrent workers")
	total := flag.Int("n", 20000, "total requests")
	flag.Parse()

	body := []byte(`{"milestoneId":"load","integrations":["Procore","QuickBooks"],"payoutCents":200000,"estimatedHours":40}`)
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *workers}, Timeout: 2 * time.Second}

	jobs := make(chan struct{}, *total) // a channel used as a work queue
	for range *total {
		jobs <- struct{}{}
	}
	close(jobs)

	var (
		mu        sync.Mutex
		latencies = make([]time.Duration, 0, *total)
		failures  int
		wg        sync.WaitGroup
	)
	start := time.Now()
	for range *workers {
		wg.Go(func() { // Go 1.25+: Add, run in a goroutine, Done — in one call
			for range jobs {
				req, _ := http.NewRequest("POST", *url, bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+*token)
				t := time.Now()
				resp, err := client.Do(req)
				d := time.Since(t)
				ok := err == nil && resp.StatusCode == http.StatusOK
				if resp != nil {
					resp.Body.Close()
				}
				mu.Lock()
				if ok {
					latencies = append(latencies, d)
				} else {
					failures++
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	if len(latencies) == 0 {
		log.Fatalf("every request failed (%d)", failures)
	}

	slices.Sort(latencies)
	p := func(q float64) time.Duration { return latencies[int(q*float64(len(latencies)-1))] }
	fmt.Printf("requests %d  failures %d  throughput %.0f req/s\n", len(latencies)+failures, failures, float64(len(latencies))/elapsed.Seconds())
	fmt.Printf("latency  p50 %v  p95 %v  p99 %v  max %v\n", p(.50), p(.95), p(.99), latencies[len(latencies)-1])
}
