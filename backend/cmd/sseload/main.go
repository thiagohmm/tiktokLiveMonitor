// Command sseload is a load-test tool for the SSE fan-out: starts the full
// backend (with an in-memory repository, no PostgreSQL/TikTok needed) and
// runs a configurable event storm via monitor.Emit (the view layer
// broadcasts every monitor event to all SSE clients).
//
// It runs as a SEPARATE process from the test client on purpose: macOS caps
// each process at ~10240 open files, and 10k loopback connections would
// otherwise consume 20k FDs in a single test process.
//
// Usage: go run ./cmd/sseload -port 19858 -rate 2000 -duration 60s
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/view"
)

// fakeRepo satisfies model.Repository via embedding; only the methods
// touched by the constructor (settings) are implemented. Any other call
// would panic loudly (nil embedded interface).
type fakeRepo struct{ model.Repository }

func (fakeRepo) GetSetting(string) (string, error) { return "", nil }
func (fakeRepo) SetSetting(string, string) error   { return nil }

func main() {
	port := flag.Int("port", 19858, "porta do servidor")
	rate := flag.Int("rate", 2000, "eventos/segundo na tempestade")
	duration := flag.Duration("duration", 60*time.Second, "duração da tempestade")
	flag.Parse()

	mon, err := monitor.New()
	if err != nil {
		log.Fatalf("monitor: %v", err)
	}
	mon.SetCurrentLive("loadtest")
	ctrl := controller.NewAppController(mon, fakeRepo{})
	srv := view.New(view.Config{Host: "127.0.0.1", Port: *port}, ctrl)

	ctx := context.Background()
	go func() {
		if err := srv.Start(ctx); err != nil {
			log.Printf("server stopped: %v", err)
		}
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", *port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(base + "/api/readiness")
		if err == nil {
			// One-shot readiness probe: the close error is not actionable.
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			log.Fatal("servidor não subiu")
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("READY")

	// Tempestade: mon.Emit faz o view broadcastar para todos os clientes SSE.
	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(*rate))
		defer ticker.Stop()
		stop := time.NewTimer(*duration)
		n := 0
		for {
			select {
			case <-stop.C:
				log.Printf("storm done: %d eventos emitidos", n)
				fmt.Println("STORM_DONE")
				return
			case <-ticker.C:
				n++
				mon.Emit("load-test", monitor.EventData{"n": n})
			}
		}
	}()

	// Mantém o processo vivo: o cliente do teste encerra com kill.
	// (O view trata SIGINT/SIGTERM com graceful shutdown por conta própria.)
	select {}
}
