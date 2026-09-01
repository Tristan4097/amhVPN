package session

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"whitevpn-desktop/internal/mihomoconf"
)

const amhVPNSubscriptionURL = "https://amhvpn.amirhasrati.workers.dev/sub"

// TestLiveAmhVPNSubscription tests the public built-in subscription end to end.
// It is deliberately opt-in because it opens one real proxy request per node.
func TestLiveAmhVPNSubscription(t *testing.T) {
	if os.Getenv("AMHVPN_LIVE") != "1" {
		t.Skip("set AMHVPN_LIVE=1 to test every public amhVPN node")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, amhVPNSubscriptionURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("subscription returned HTTP %d", response.StatusCode)
	}

	proxies, _, _, err := mihomoconf.ParseSubscriptionWithReport(string(body))
	if err != nil {
		t.Fatal(err)
	}
	measurer, err := StartMeasurer(ctx, MeasureOptions{
		CorePath:               enginePath(t),
		HomeDir:                t.TempDir(),
		Subscription:           string(body),
		PipeSecurityDescriptor: "D:P(A;;GA;;;WD)",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer measurer.Close()

	var tcpReachable, proxyTrafficSuccessful atomic.Int64
	// TCP and proxy traffic are measured separately. A bounded parallel pass
	// covers every node without turning a 100-node list into minutes of serial
	// waiting on the first few dead entries.
	forEachBounded(ctx, measurer.Names(), 50, func(name string) {
		proxy := proxyByName(proxies, name)
		if proxyTCPReachable(ctx, proxy) {
			tcpReachable.Add(1)
		}
	})
	forEachBounded(ctx, measurer.Names(), 50, func(name string) {
		probeCtx, probeCancel := context.WithTimeout(ctx, 8*time.Second)
		defer probeCancel()
		// asyncTestDelay is a Mihomo request through this exact proxy to the
		// health URL; success proves proxy traffic completed, not merely TCP.
		if _, err := measurer.Delay(probeCtx, name, mihomoconf.DelayTestURL, 5*time.Second); err == nil {
			proxyTrafficSuccessful.Add(1)
		}
	})

	total := subscriptionConfigCount(string(body))
	parsed := len(proxies)
	t.Logf("amhVPN live: total=%d parsed=%d tcp-reachable=%d proxy-traffic-successful=%d failed=%d", total, parsed, tcpReachable.Load(), proxyTrafficSuccessful.Load(), total-int(proxyTrafficSuccessful.Load()))
}

func subscriptionConfigCount(body string) int {
	total := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "://") {
			continue
		}
		scheme := strings.ToLower(line[:strings.Index(line, "://")])
		switch scheme {
		case "vless", "vmess", "trojan", "ss", "hysteria2", "hy2", "wireguard", "wg":
			total++
		}
	}
	return total
}

func proxyByName(proxies []mihomoconf.Proxy, name string) mihomoconf.Proxy {
	for _, proxy := range proxies {
		if proxy.Name() == name {
			return proxy
		}
	}
	return nil
}

func proxyTCPReachable(ctx context.Context, proxy mihomoconf.Proxy) bool {
	if proxy == nil {
		return false
	}
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
	if host == "" || port <= 0 || port > 65535 {
		return false
	}
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}
