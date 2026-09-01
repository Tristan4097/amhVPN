package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"whitevpn-desktop/internal/mihomoconf"
	"whitevpn-desktop/internal/session"
)

type sourceStatus struct {
	URL     string `json:"url"`
	Fetched int    `json:"fetched"`
	Parsed  int    `json:"parsed"`
	Error   string `json:"error,omitempty"`
}

type status struct {
	CheckedAt       string         `json:"checked_at"`
	Sources         []sourceStatus `json:"sources"`
	Fetched         int            `json:"fetched"`
	Parsed          int            `json:"parsed"`
	Unique          int            `json:"unique"`
	Tested          int            `json:"tested"`
	TCPReachable    int            `json:"tcp_reachable"`
	HTTPFirstPass   int            `json:"http_success_first_pass"`
	HTTPSecondPass  int            `json:"http_success_second_pass"`
	Healthy         int            `json:"healthy"`
	Protocols       map[string]int `json:"protocols"`
	DurationSeconds int            `json:"duration_seconds"`
}

type candidate struct {
	link     string
	protocol string
	proxy    mihomoconf.Proxy
}

func main() {
	var (
		sourcesPath  = flag.String("sources", "", "newline-delimited public subscription URLs")
		outputPath   = flag.String("output", "", "healthy share-link output path")
		statusPath   = flag.String("status", "", "status JSON output path")
		corePath     = flag.String("core", defaultCorePath(), "Mihomo executable path")
		limit        = flag.Int("limit", 500, "maximum unique configs to test")
		concurrency  = flag.Int("concurrency", 24, "maximum concurrent Mihomo measurers")
		probeTimeout = flag.Duration("probe-timeout", 8*time.Second, "per-node HTTP probe timeout")
		verifyDelay  = flag.Duration("verify-delay", 3*time.Second, "delay before a second successful probe")
		minimum      = flag.Int("minimum-healthy", 5, "minimum double-confirmed nodes required to publish")
		dryRun       = flag.Bool("dry-run", false, "test only; do not write healthy.txt or status.json")
	)
	flag.Parse()
	if *sourcesPath == "" || *outputPath == "" || *statusPath == "" {
		fmt.Fprintln(os.Stderr, "sources, output, and status are required")
		os.Exit(2)
	}
	if *limit < 500 {
		fmt.Fprintln(os.Stderr, "limit must be at least 500")
		os.Exit(2)
	}
	if *concurrency < 1 || *minimum < 1 {
		fmt.Fprintln(os.Stderr, "concurrency and minimum-healthy must be positive")
		os.Exit(2)
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	urls, err := readSources(*sourcesPath)
	if err != nil {
		fatal(err)
	}
	report := status{CheckedAt: started.UTC().Format(time.RFC3339), Protocols: map[string]int{}}
	items := make([]candidate, 0, *limit)
	seen := map[string]struct{}{}
	for _, rawURL := range urls {
		entry := sourceStatus{URL: rawURL}
		body, err := fetch(ctx, rawURL)
		if err != nil {
			entry.Error = err.Error()
			report.Sources = append(report.Sources, entry)
			continue
		}
		entry.Fetched = configCount(body)
		report.Fetched += entry.Fetched
		proxies, links, _, err := mihomoconf.ParseSubscriptionWithReport(body)
		if err != nil {
			entry.Error = err.Error()
			report.Sources = append(report.Sources, entry)
			continue
		}
		entry.Parsed = len(proxies)
		report.Parsed += entry.Parsed
		report.Sources = append(report.Sources, entry)
		for index, proxy := range proxies {
			identity, err := connectionIdentity(proxy)
			if err != nil {
				continue
			}
			if _, exists := seen[identity]; exists {
				continue
			}
			seen[identity] = struct{}{}
			protocol, _ := proxy["type"].(string)
			items = append(items, candidate{link: links[index], protocol: strings.ToLower(protocol), proxy: proxy})
		}
	}
	report.Unique = len(items)
	if len(items) > *limit {
		items = items[:*limit]
	}
	if len(items) == 0 {
		report.DurationSeconds = int(time.Since(started).Round(time.Second).Seconds())
		writeStatus(*statusPath, report, *dryRun)
		fatal(fmt.Errorf("no parseable unique configs"))
	}

	// Reparse the exact deduplicated document. This makes Mihomo's generated
	// names align with the candidates that the workers select.
	links := make([]string, 0, len(items))
	for _, item := range items {
		links = append(links, item.link)
	}
	subscription := strings.Join(links, "\n")
	proxies, _, _, err := mihomoconf.ParseSubscriptionWithReport(subscription)
	if err != nil {
		fatal(err)
	}
	items = items[:0]
	for index, proxy := range proxies {
		protocol, _ := proxy["type"].(string)
		items = append(items, candidate{link: links[index], protocol: strings.ToLower(protocol), proxy: proxy})
	}
	report.Tested = len(items)
	for _, item := range items {
		report.Protocols[item.protocol]++
	}

	var tcpReachable atomic.Int64
	parallel(ctx, items, 64, func(item candidate) {
		if tcpOpen(ctx, item.proxy) {
			tcpReachable.Add(1)
		}
	})
	report.TCPReachable = int(tcpReachable.Load())

	workers := min(*concurrency, len(items))
	jobs := make(chan candidate)
	var firstPass, secondPass atomic.Int64
	var healthyMu sync.Mutex
	healthy := make([]string, 0)
	var workerWG sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			homeDir := tempHome()
			defer os.RemoveAll(homeDir)
			measurer, err := session.StartMeasurer(ctx, session.MeasureOptions{
				CorePath:               *corePath,
				HomeDir:                homeDir,
				Subscription:           subscription,
				PipeSecurityDescriptor: "D:P(A;;GA;;;WD)",
			})
			if err != nil {
				return
			}
			defer measurer.Close()
			for item := range jobs {
				probeCtx, probeCancel := context.WithTimeout(ctx, *probeTimeout)
				_, err := measurer.ProbeHTTP(probeCtx, item.proxy.Name())
				probeCancel()
				if err != nil {
					continue
				}
				firstPass.Add(1)
				select {
				case <-ctx.Done():
					continue
				case <-time.After(*verifyDelay):
				}
				verifyCtx, verifyCancel := context.WithTimeout(ctx, *probeTimeout)
				_, err = measurer.ProbeHTTP(verifyCtx, item.proxy.Name())
				verifyCancel()
				if err != nil {
					continue
				}
				secondPass.Add(1)
				healthyMu.Lock()
				healthy = append(healthy, item.link)
				healthyMu.Unlock()
			}
		}()
	}
	for _, item := range items {
		jobs <- item
	}
	close(jobs)
	workerWG.Wait()

	report.HTTPFirstPass = int(firstPass.Load())
	report.HTTPSecondPass = int(secondPass.Load())
	report.Healthy = len(healthy)
	report.DurationSeconds = int(time.Since(started).Round(time.Second).Seconds())
	writeStatus(*statusPath, report, *dryRun)
	if !*dryRun && len(healthy) >= *minimum {
		writeHealthy(*outputPath, healthy)
	}
	encoded, _ := json.Marshal(report)
	fmt.Println(string(encoded))
}

func readSources(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	urls := make([]string, 0)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, exists := seen[line]; !exists {
			seen[line] = struct{}{}
			urls = append(urls, line)
		}
	}
	return urls, nil
}

func fetch(ctx context.Context, rawURL string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 32<<20))
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
				return string(body), nil
			}
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return "", lastErr
}

func connectionIdentity(proxy mihomoconf.Proxy) (string, error) {
	copy := clone(proxy).(map[string]any)
	delete(copy, "name")
	removeInjectedUserAgents(copy)
	encoded, err := json.Marshal(copy)
	return string(encoded), err
}

func clone(value any) any {
	switch typed := value.(type) {
	case mihomoconf.Proxy:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = clone(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = clone(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = clone(item)
		}
		return out
	default:
		return typed
	}
}

func removeInjectedUserAgents(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if strings.EqualFold(key, "user-agent") {
				delete(typed, key)
				continue
			}
			removeInjectedUserAgents(item)
		}
	case []any:
		for _, item := range typed {
			removeInjectedUserAgents(item)
		}
	}
}

func configCount(body string) int {
	count := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "://") {
			continue
		}
		scheme := strings.ToLower(line[:strings.Index(line, "://")])
		switch scheme {
		case "vless", "vmess", "trojan", "ss", "hysteria2", "hy2", "wireguard", "wg":
			count++
		}
	}
	return count
}

func tcpOpen(ctx context.Context, proxy mihomoconf.Proxy) bool {
	host, _ := proxy["server"].(string)
	port := 0
	switch value := proxy["port"].(type) {
	case int:
		port = value
	case int64:
		port = int(value)
	case float64:
		port = int(value)
	}
	if host == "" || port < 1 || port > 65535 {
		return false
	}
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func parallel(ctx context.Context, items []candidate, workers int, work func(candidate)) {
	jobs := make(chan candidate)
	var wg sync.WaitGroup
	for index := 0; index < min(workers, len(items)); index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				work(item)
			}
		}()
	}
	for _, item := range items {
		jobs <- item
	}
	close(jobs)
	wg.Wait()
}

func writeHealthy(path string, links []string) {
	sort.Strings(links)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(links, "\n")+"\n"), 0o600); err != nil {
		fatal(err)
	}
}

func writeStatus(path string, report status, dryRun bool) {
	if dryRun {
		return
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		fatal(err)
	}
}

func defaultCorePath() string {
	name := "mihomo-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path, err := filepath.Abs(filepath.Join("cores", name))
	if err != nil {
		return filepath.Join("cores", name)
	}
	return path
}

func tempHome() string {
	path, err := os.MkdirTemp("", "amhvpn-harvester-")
	if err != nil {
		panic(err)
	}
	return path
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "sub-harvester:", err); os.Exit(1) }
