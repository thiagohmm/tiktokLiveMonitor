package signer

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Signer struct {
	server  *http.Server
	client  *http.Client
	baseURL string
	msToken string
	mu      sync.RWMutex
}

func New() *Signer {
	s := &Signer{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/webcast/fetch/", s.handleFetch)
	mux.HandleFunc("/webcast/rate_limits", s.handleRateLimits)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	s.server = &http.Server{
		Handler: mux,
	}

	return s
}

func (s *Signer) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("signer listen: %w", err)
	}

	s.baseURL = fmt.Sprintf("http://127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.server.Shutdown(shutdownCtx)
	}()

	s.refreshMsToken()

	log.Printf("[Signer] Listening on %s", s.baseURL)
	return s.server.Serve(listener)
}

func (s *Signer) BaseURL() string {
	return s.baseURL
}

func (s *Signer) WaitReady(ctx context.Context) error {
	url := s.baseURL + "/health"
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *Signer) refreshMsToken() {
	req, err := http.NewRequest("GET", "https://www.tiktok.com/", nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/json,application/protobuf")

	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("[Signer] Failed to fetch TikTok for msToken: %v", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	bodyStr := string(body)

	re := regexp.MustCompile(`"msToken":"([^"]+)"`)
	matches := re.FindStringSubmatch(bodyStr)
	if len(matches) > 1 {
		s.mu.Lock()
		s.msToken = matches[1]
		s.mu.Unlock()
		log.Printf("[Signer] msToken obtained (len=%d)", len(matches[1]))
	}

	go func() {
		time.Sleep(15 * time.Minute)
		s.refreshMsToken()
	}()
}

func (s *Signer) handleFetch(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, `{"error":"missing url param"}`, http.StatusBadRequest)
		return
	}

	decodedURL, err := url.QueryUnescape(targetURL)
	if err != nil {
		decodedURL = targetURL
	}

	parsed, err := url.Parse(decodedURL)
	if err != nil {
		http.Error(w, `{"error":"invalid url"}`, http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	msToken := s.msToken
	s.mu.RUnlock()

	q := parsed.Query()
	if msToken != "" {
		q.Set("msToken", msToken)
	}
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", parsed.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/json,application/protobuf")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://www.tiktok.com/")
	req.Header.Set("Origin", "https://www.tiktok.com")
	req.Header.Set("Connection", "keep-alive")

	resp, err := s.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for k, v := range resp.Header {
		kl := strings.ToLower(k)
		if kl == "content-type" || kl == "content-length" || kl == "set-cookie" {
			w.Header()[k] = v
		}
	}

	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func (s *Signer) handleRateLimits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	now := time.Now()
	fmt.Fprintf(w, `{"code":0,"message":"ok","day":{"max":100000,"remaining":99999,"reset_at":"%s"},"hour":{"max":10000,"remaining":9999,"reset_at":"%s"},"minute":{"max":1000,"remaining":999,"reset_at":"%s"}}`,
		now.Add(24*time.Hour).Format(time.RFC3339),
		now.Add(1*time.Hour).Format(time.RFC3339),
		now.Add(1*time.Minute).Format(time.RFC3339),
	)
}
