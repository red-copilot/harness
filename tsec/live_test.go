package tsec_test

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/red-copilot/harness/tsec"
)

// TestLiveTSecLifecycle performs a real start/close against TSec. It is
// disabled by default because it allocates a benchmark instance and requires
// the benchmark VPN.
func TestLiveTSecLifecycle(t *testing.T) {
	if os.Getenv("HARNESS_TSEC_LIVE") != "1" {
		t.Skip("set HARNESS_TSEC_LIVE=1 to run the live TSec lifecycle test")
	}
	baseURL := os.Getenv("BENCHMARK_BASE_URL")
	token := os.Getenv("BENCHMARK_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("BENCHMARK_BASE_URL and BENCHMARK_TOKEN are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client, err := tsec.New(tsec.Config{BaseURL: baseURL, Token: token, CheckVPN: true})
	if err != nil {
		t.Fatal(err)
	}
	vpn, err := client.CheckVPN(ctx)
	if err != nil {
		t.Fatalf("VPN preflight: %v", err)
	}
	t.Logf("VPN ok, client_ip=%s", vpn.ClientIP)

	challenges, err := client.ListChallenges(ctx)
	if err != nil {
		t.Fatalf("ListChallenges: %v", err)
	}
	code := os.Getenv("HARNESS_TSEC_CHALLENGE")
	if code == "" {
		for _, challenge := range challenges {
			if !challenge.IsCompleted && challenge.ContainerStatus == "stopped" {
				code = challenge.UniqueCode
				break
			}
		}
	}
	if code == "" {
		t.Skip("no stopped, unfinished challenge available")
	}

	started, err := client.StartChallenge(ctx, code)
	if err != nil {
		t.Fatalf("StartChallenge(%s): %v", code, err)
	}
	closed := false
	defer func() {
		if closed {
			return
		}
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		if _, err := client.CloseChallenge(closeCtx, code); err != nil {
			t.Errorf("cleanup CloseChallenge(%s): %v", code, err)
		}
	}()
	t.Logf("started challenge=%s addresses=%v", code, started.ContainerAddr)

	addresses := waitForAddresses(t, ctx, client, code, 45*time.Second)
	reachable := 0
	for _, address := range addresses {
		conn, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			t.Logf("target address %s not reachable over TCP: %v", address, err)
			continue
		}
		_ = conn.Close()
		reachable++
	}
	if reachable == 0 {
		t.Fatalf("challenge %s started but no address was reachable over TCP: %v", code, addresses)
	}
	t.Logf("reachable target addresses=%d/%d", reachable, len(addresses))

	closedResult, err := client.CloseChallenge(ctx, code)
	if err != nil {
		t.Fatalf("CloseChallenge(%s): %v", code, err)
	}
	if !closedResult.Closed {
		t.Fatalf("CloseChallenge(%s) returned closed=false", code)
	}
	closed = true
	t.Logf("closed challenge=%s", code)
}

func waitForAddresses(t *testing.T, ctx context.Context, client *tsec.Client, code string, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		challenges, err := client.ListChallenges(ctx)
		if err != nil {
			t.Fatalf("ListChallenges while waiting for %s: %v", code, err)
		}
		for _, challenge := range challenges {
			if challenge.UniqueCode == code && len(challenge.ContainerAddr) > 0 {
				return challenge.ContainerAddr
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for target addresses for %s", code)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended while waiting for %s: %v", code, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}
