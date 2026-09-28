package whatsapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// keyTrap is a foreign server that records whether it ever saw the WAHA key.
func keyTrap(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "" {
			hits.Add(1)
		}
		_, _ = w.Write([]byte("%PDF-stolen"))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func mediaClient(baseURL string) *Client {
	return NewClient(Config{
		Enabled: true, BaseURL: baseURL, APIKey: "secret-key", WebhookSecret: "s",
		RequestTimeout: 2 * time.Second, MaxMediaBytes: 1 << 20,
	})
}

func TestDownloadMediaOnlyFromWAHAHost(t *testing.T) {
	var wahaHits atomic.Int32
	wahaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") == "secret-key" {
			wahaHits.Add(1)
		}
		_, _ = w.Write([]byte("%PDF-ok"))
	}))
	t.Cleanup(wahaServer.Close)
	foreign, foreignHits := keyTrap(t)
	c := mediaClient(wahaServer.URL)

	data, err := c.DownloadMedia(context.Background(), wahaServer.URL+"/api/files/a.pdf")
	if err != nil || string(data) != "%PDF-ok" {
		t.Fatalf("WAHA host download = %q, %v", data, err)
	}
	if wahaHits.Load() != 1 {
		t.Fatalf("WAHA did not receive the key")
	}

	for _, raw := range []string{
		foreign.URL + "/api/files/a.pdf",
		"http://169.254.169.254/latest/meta-data",
		"file:///etc/passwd",
		"//" + foreign.Listener.Addr().String() + "/x",
	} {
		if _, err := c.DownloadMedia(context.Background(), raw); err == nil {
			t.Fatalf("DownloadMedia(%q) accepted a foreign URL", raw)
		}
	}
	if foreignHits.Load() != 0 {
		t.Fatalf("X-Api-Key sent to a foreign host %d times", foreignHits.Load())
	}
}

func TestDownloadMediaRefusesCrossHostRedirect(t *testing.T) {
	foreign, foreignHits := keyTrap(t)
	wahaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+"/steal", http.StatusFound)
	}))
	t.Cleanup(wahaServer.Close)
	c := mediaClient(wahaServer.URL)

	if _, err := c.DownloadMedia(context.Background(), wahaServer.URL+"/api/files/a.pdf"); err == nil {
		t.Fatal("redirect to a foreign host was followed")
	}
	if foreignHits.Load() != 0 {
		t.Fatalf("X-Api-Key leaked through a redirect")
	}
}
