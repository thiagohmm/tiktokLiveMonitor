package view

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	sseDefaultMaxClients = 10000
	sseEventBuffer       = 256
	ssePingInterval      = 30 * time.Second
	sseWriteTimeout      = 5 * time.Second
)

var ssePing = []byte(": ping\n\n")

func sseMaxClientsFromEnv() int {
	if v := os.Getenv("SSE_MAX_CLIENTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return sseDefaultMaxClients
}

type sseClient struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	ch       chan []byte
	done     chan struct{}
	error    chan struct{}
	doneOnce sync.Once
}

func newSSEClient(w http.ResponseWriter, flusher http.Flusher) *sseClient {
	return &sseClient{
		w:       w,
		flusher: flusher,
		ch:      make(chan []byte, sseEventBuffer),
		done:    make(chan struct{}),
		error:   make(chan struct{}),
	}
}

// finish sinaliza o fim do cliente: a goroutine de escrita encerra (done)
// e a handler retorna (error — sem depender de o ctx do request acabar, que
// não acontece se a conexão seguir aberta mesmo após ejeção). O canal ch é
// intencionalmente NUNCA fechado: fechar causaria panic em sends não-
// bloqueantes subsequentes (broadcast).
func (c *sseClient) finish() {
	c.doneOnce.Do(func() {
		close(c.done)
		close(c.error)
	})
}

func (c *sseClient) write(msg []byte) error {
	rc := http.NewResponseController(c.w)
	_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
	if _, err := c.w.Write(msg); err != nil {
		return err
	}
	c.flusher.Flush()
	return nil
}

// sseWriteLoop drena o canal de um cliente, impondo deadline por escrita: um
// peer morto (pacotes enviados, ninguém lendo) é detectado pelo timeout ou
// pelo ping e removido em vez de segurar a conexão para sempre.
func (s *HTTPServer) sseWriteLoop(c *sseClient, ctx context.Context) {
	ticker := time.NewTicker(ssePingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case msg := <-c.ch:
			if err := c.write(msg); err != nil {
				s.dropSSEClient(c, "erro de escrita")
				return
			}
		case <-ticker.C:
			if err := c.write(ssePing); err != nil {
				s.dropSSEClient(c, "keepalive falhou")
				return
			}
		}
	}
}

// removeSSEClient desconecta um cliente normalmente (cliente fechou a
// conexão). Sem log para evitar spam com muitos clientes.
func (s *HTTPServer) removeSSEClient(c *sseClient) {
	s.sseMu.Lock()
	delete(s.sseClients, c)
	s.sseMu.Unlock()
	c.finish()
}

// dropSSEClient ejeta um cliente com problema (escrita lenta/falhou) e
// registra em log.
func (s *HTTPServer) dropSSEClient(c *sseClient, reason string) {
	s.sseMu.Lock()
	remaining := 0
	if _, ok := s.sseClients[c]; ok {
		delete(s.sseClients, c)
		remaining = len(s.sseClients)
		log.Printf("[View] SSE: cliente ejetado (%s); restantes=%d", reason, remaining)
	}
	s.sseMu.Unlock()
	c.finish()
}

func (s *HTTPServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	client := newSSEClient(w, flusher)

	// Teto de clientes SSE simultâneos (proteção contra esgotamento de
	// conexões): acima do limite, novos clientes recebem 503 e podem
	// reconectar — clientes existentes continuam servidos.
	s.sseMu.Lock()
	if len(s.sseClients) >= s.maxSSEClients {
		s.sseMu.Unlock()
		log.Printf("[View] SSE: teto atingido (%d); recusando novo cliente", s.maxSSEClients)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := fmt.Fprint(w, "servidor com muitos clientes conectados; tente novamente em instantes\n"); err != nil {
			log.Printf("[View] SSE 503 body: %v", err)
		}
		return
	}
	s.sseClients[client] = struct{}{}
	s.sseMu.Unlock()

	// Conexões SSE duram horas: limpa apenas o deadline de LEITURA do
	// servidor para esta conexão (o de escrita é gerido por-write na
	// loop de escrita, com ping periódico para detectar clientes mortos).
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// Envio do estado inicial (antes do writer começar, para não intercalar).
	state := s.controller.GetState()
	initial := map[string]interface{}{
		"connected": state.Connected,
		"username":  state.Username,
	}
	if err := writeSSEPayload(w, flusher, "server-state", initial); err != nil {
		log.Printf("[View] SSE: falha ao enviar estado inicial: %v", err)
		s.dropSSEClient(client, "falha no estado inicial")
		return
	}

	// A handler PRECISA esperar a goroutine de escrita encerrar antes de
	// retornar: quando a handler sai, o net/http finaliza o ResponseWriter —
	// se a writer ainda estiver no ar (ex.: bloqueada num write com
	// deadline), ela usaria um writer já finalizado e causaria SIGSEGV
	// (nil pointer) derrubando o processo inteiro.
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		s.sseWriteLoop(client, r.Context())
	}()

	// Aguarda o cliente desconectar (ctx) ou ser ejetado / ter erro de
	// escrita (client.error); depois, a writer encerrar de fato.
	select {
	case <-r.Context().Done():
	case <-client.error:
	}
	s.removeSSEClient(client)
	<-stopped
}

func (s *HTTPServer) broadcastSSE(eventType string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := fmt.Appendf(nil, "event: %s\ndata: %s\n\n", eventType, payload)

	s.sseMu.Lock()
	for c := range s.sseClients {
		select {
		case c.ch <- msg:
		default:
			// Buffer cheio: cliente lento. Ejetar em vez de segurar o
			// broadcast para todos os demais.
			delete(s.sseClients, c)
			c.finish()
			log.Printf("[View] SSE: cliente lento ejetado (buffer cheio); restantes=%d", len(s.sseClients))
		}
	}
	s.sseMu.Unlock()
}

// writeSSEPayload escreve um único evento SSE e faz o flush (usado para o
// estado inicial da conexão).
func writeSSEPayload(w http.ResponseWriter, f http.Flusher, event string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	f.Flush()
	return nil
}
