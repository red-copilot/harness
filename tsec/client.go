package tsec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/red-copilot/harness"
)

const (
	defaultHealthcheck = "http://10.0.100.58"
	defaultRate        = 3.0
	defaultBurst       = 3
	defaultInstanceCap = 3
)

type Config struct {
	BaseURL        string
	Token          string
	CheckVPN       bool
	HealthcheckURL string
	HTTPClient     *http.Client
	RetryDelayMS   int
	RatePerSecond  float64
	Burst          int
	InstanceLimit  int
}

type Client struct {
	baseURL        string
	token          string
	http           *http.Client
	healthcheckURL string
	retryDelay     time.Duration
	limiter        *tokenBucket

	mu    sync.Mutex
	open  map[string]struct{}
	limit int
}

func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("tsec base URL is required")
	}
	if cfg.Token == "" {
		return nil, errors.New("tsec token is required")
	}
	if cfg.HealthcheckURL == "" {
		cfg.HealthcheckURL = defaultHealthcheck
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.RetryDelayMS <= 0 {
		cfg.RetryDelayMS = 1000
	}
	if cfg.RatePerSecond <= 0 {
		cfg.RatePerSecond = defaultRate
	}
	if cfg.Burst <= 0 {
		cfg.Burst = defaultBurst
	}
	if cfg.InstanceLimit <= 0 {
		cfg.InstanceLimit = defaultInstanceCap
	}
	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"), token: cfg.Token, http: cfg.HTTPClient,
		healthcheckURL: cfg.HealthcheckURL,
		retryDelay:     time.Duration(cfg.RetryDelayMS) * time.Millisecond,
		limiter:        newTokenBucket(cfg.RatePerSecond, cfg.Burst),
		open:           map[string]struct{}{}, limit: cfg.InstanceLimit,
	}, nil
}

func (c *Client) BaseURL() string { return c.baseURL }

type Challenge struct {
	UniqueCode       string   `json:"unique_code"`
	Description      string   `json:"description"`
	Difficulty       string   `json:"difficulty"`
	Level            int      `json:"level"`
	TotalScore       int      `json:"total_score"`
	FlagCount        int      `json:"flag_count"`
	CorrectFlagCount int      `json:"correct_flag_count"`
	IsCompleted      bool     `json:"is_completed"`
	ContainerStatus  string   `json:"container_status"`
	ContainerAddr    []string `json:"container_addr"`
}

type StartResult struct {
	UniqueCode    string   `json:"unique_code"`
	ContainerAddr []string `json:"container_addr"`
}
type HintResult struct {
	UniqueCode string `json:"unique_code"`
	Hint       string `json:"hint"`
}
type SubmitResult struct {
	Correct          bool `json:"correct"`
	Awarded          int  `json:"awarded"`
	CumulativeScore  int  `json:"cumulative_score"`
	CorrectFlagCount int  `json:"correct_flag_count"`
	TotalFlagCount   int  `json:"total_flag_count"`
	MatchedFlagIndex *int `json:"matched_flag_index"`
}
type CloseResult struct {
	UniqueCode string `json:"unique_code"`
	Closed     bool   `json:"closed"`
}
type VpnCheckResult struct {
	Status   string `json:"status"`
	ClientIP string `json:"client_ip"`
	Time     string `json:"time"`
	OK       bool   `json:"-"`
}

func (c *Client) ListChallenges(ctx context.Context) ([]Challenge, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/openapi/v1/challenges", nil, nil, &raw); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var challenges []Challenge
		if err := json.Unmarshal(trimmed, &challenges); err != nil {
			return nil, fmt.Errorf("decode challenge list: %w", err)
		}
		return challenges, nil
	}
	var wrapped struct {
		Challenges []Challenge `json:"challenges"`
	}
	if err := json.Unmarshal(trimmed, &wrapped); err != nil {
		return nil, fmt.Errorf("decode challenge list: %w", err)
	}
	return wrapped.Challenges, nil
}

func (c *Client) StartChallenge(ctx context.Context, code string) (StartResult, error) {
	var result StartResult
	c.mu.Lock()
	if _, ok := c.open[code]; ok {
		c.mu.Unlock()
		return result, fmt.Errorf("challenge already has a running instance: %s", code)
	}
	if len(c.open) >= c.limit {
		c.mu.Unlock()
		return result, fmt.Errorf("challenge instance limit reached (%d)", c.limit)
	}
	c.open[code] = struct{}{}
	c.mu.Unlock()
	query := url.Values{"unique_code": []string{code}}
	err := c.do(ctx, http.MethodPost, "/openapi/v1/challenges/start", query, nil, &result)
	if err != nil {
		c.release(code)
		return StartResult{}, err
	}
	return result, nil
}

func (c *Client) GetHint(ctx context.Context, code string) (HintResult, error) {
	var result HintResult
	err := c.do(ctx, http.MethodGet, "/openapi/v1/challenges/hint",
		url.Values{"unique_code": []string{code}}, nil, &result)
	return result, err
}

func (c *Client) SubmitFlag(ctx context.Context, code, flag string) (SubmitResult, error) {
	var result SubmitResult
	body := map[string]string{"unique_code": code, "flag": flag}
	err := c.do(ctx, http.MethodPost, "/openapi/v1/challenges/submit", nil, body, &result)
	return result, err
}

func (c *Client) CloseChallenge(ctx context.Context, code string) (CloseResult, error) {
	var result CloseResult
	err := c.do(ctx, http.MethodPost, "/openapi/v1/challenges/close",
		url.Values{"unique_code": []string{code}}, nil, &result)
	c.release(code)
	return result, err
}

func (c *Client) CheckVPN(ctx context.Context) (VpnCheckResult, error) {
	var result VpnCheckResult
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.healthcheckURL, nil)
	if err != nil {
		return result, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return result, &Error{Code: "vpn_check_failed", Message: "VPN检测未通过,请检查靶场VPN网络配置", Detail: json.RawMessage(`{"reason":"network_error"}`)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, &Error{Code: "vpn_check_failed", Message: "VPN检测未通过,请检查靶场VPN网络配置", StatusCode: resp.StatusCode, Detail: json.RawMessage(`{"reason":"bad_status"}`)}
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, &Error{Code: "vpn_check_failed", Message: "VPN检测未通过,请检查靶场VPN网络配置", Detail: json.RawMessage(`{"reason":"bad_body"}`)}
	}
	if result.Status != "ok" {
		return result, &Error{Code: "vpn_check_failed", Message: "VPN检测未通过,请检查靶场VPN网络配置", Detail: json.RawMessage(`{"reason":"status_not_ok"}`)}
	}
	result.OK = true
	return result, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := c.limiter.wait(ctx); err != nil {
		return err
	}
	err := c.doOnce(ctx, method, path, query, body, out)
	var apiErr *Error
	if err != nil && errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
		select {
		case <-time.After(c.retryDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := c.limiter.wait(ctx); err != nil {
			return err
		}
		return c.doOnce(ctx, method, path, query, body, out)
	}
	return err
}

func (c *Client) doOnce(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("BENCHMARK_TOKEN", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseError(resp.StatusCode, data)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], data...)
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *Client) release(code string) {
	c.mu.Lock()
	delete(c.open, code)
	c.mu.Unlock()
}

type Error struct {
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	Detail     json.RawMessage `json:"detail"`
	StatusCode int             `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s [code=%s] [http=%d]", e.Message, e.Code, e.StatusCode)
	}
	return fmt.Sprintf("%s [code=%s]", e.Message, e.Code)
}

func IsCode(err error, code string) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Code == code
}

func parseError(status int, data []byte) error {
	apiErr := &Error{StatusCode: status, Code: "app_error"}
	if status == http.StatusUnprocessableEntity {
		apiErr.Code = "validation_error"
		apiErr.Message = "request validation failed"
		apiErr.Detail = append(json.RawMessage(nil), data...)
		return apiErr
	}
	var payload struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(data, &payload) == nil {
		if payload.Code != "" {
			apiErr.Code = payload.Code
		}
		if payload.Message != "" {
			apiErr.Message = payload.Message
		}
		apiErr.Detail = payload.Detail
	}
	if apiErr.Message == "" {
		apiErr.Message = "unexpected benchmark error"
	}
	return apiErr
}

type tokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newTokenBucket(rate float64, burst int) *tokenBucket {
	return &tokenBucket{rate: rate, burst: float64(burst), tokens: float64(burst), last: time.Now()}
}

func (b *tokenBucket) wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens = min(b.burst, b.tokens+elapsed*b.rate)
			b.last = now
		}
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Backend implements harness.Backend for one TSec challenge.
type Backend struct {
	Client   *Client
	Code     string
	UseHint  bool
	Network  string
	CheckVPN bool
}

func NewBackend(client *Client, code string) *Backend {
	return &Backend{Client: client, Code: code, Network: "host", CheckVPN: true}
}

func (b *Backend) Provision(ctx context.Context, req harness.ProvisionRequest) (harness.Target, error) {
	if b.CheckVPN {
		if _, err := b.Client.CheckVPN(ctx); err != nil {
			return harness.Target{}, err
		}
	}
	started, err := b.Client.StartChallenge(ctx, b.Code)
	if err != nil {
		return harness.Target{}, err
	}
	metadata := map[string]string{
		"challenge_code":     b.Code,
		"benchmark_base_url": b.Client.BaseURL(),
		"network":            b.Network,
	}
	if b.UseHint {
		hint, err := b.Client.GetHint(ctx, b.Code)
		if err == nil && hint.Hint != "" {
			metadata["hint"] = hint.Hint
			metadata["hint_used"] = "true"
		}
	}
	return harness.Target{ID: b.Code, Addresses: started.ContainerAddr, Metadata: metadata}, nil
}

func (b *Backend) Observe(ctx context.Context, target harness.Target) (harness.Completion, error) {
	challenges, err := b.Client.ListChallenges(ctx)
	if err != nil {
		return harness.Completion{}, err
	}
	for _, challenge := range challenges {
		if challenge.UniqueCode != b.Code {
			continue
		}
		if challenge.IsCompleted {
			return harness.Completion{
				Done: true, Success: true, Score: float64(challenge.TotalScore),
				Message: "challenge completed",
				Metadata: map[string]string{
					"flag_count":         strconv.Itoa(challenge.FlagCount),
					"correct_flag_count": strconv.Itoa(challenge.CorrectFlagCount),
				},
			}, nil
		}
		return harness.Completion{}, nil
	}
	return harness.Completion{}, &Error{Code: "challenge_not_found", Message: "challenge not found: " + b.Code}
}

func (b *Backend) Close(ctx context.Context, target harness.Target) error {
	_, err := b.Client.CloseChallenge(ctx, b.Code)
	return err
}

var _ harness.Backend = (*Backend)(nil)
