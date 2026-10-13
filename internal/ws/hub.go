// Package ws is a small, dependency-free WebSocket broadcast hub (RFC 6455,
// server side). It supports many concurrent clients, ping/pong keep-alive and
// drops clients that cannot keep up instead of blocking the broadcaster.
package ws

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // mandated by RFC 6455 for the handshake
	"encoding/base64"
	"encoding/binary"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	handshakeGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	writeWait     = 10 * time.Second
	pingInterval  = 25 * time.Second
	pongWait      = 60 * time.Second
	sendBuffer    = 64
)

// Hub tracks connected clients and fans messages out to them.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[*client]struct{})}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast queues msg (a text frame) for every client. It never blocks: a
// client whose buffer is full is disconnected.
func (h *Hub) Broadcast(msg []byte) {
	h.mu.RLock()
	var slow []*client
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			slow = append(slow, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range slow {
		log.Printf("ws: dropping slow client %s", c.conn.RemoteAddr())
		c.close()
	}
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// ServeHTTP upgrades the request to a WebSocket and serves it until the
// client disconnects. Any origin is accepted (the API is meant to be called
// from a separately hosted frontend).
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !headerContains(r.Header, "Connection", "upgrade") || !headerContains(r.Header, "Upgrade", "websocket") {
		http.Error(w, "expected websocket upgrade", http.StatusBadRequest)
		return
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported websocket version", http.StatusUpgradeRequired)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket unsupported by server", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		log.Printf("ws: hijack failed: %v", err)
		return
	}

	sum := sha1.Sum([]byte(key + handshakeGUID)) //nolint:gosec
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	if _, err := rw.WriteString(resp); err != nil {
		_ = conn.Close()
		return
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetWriteDeadline(time.Time{})

	c := &client{hub: h, conn: conn, send: make(chan []byte, sendBuffer), done: make(chan struct{})}
	h.add(c)
	go c.writeLoop()
	c.readLoop(rw.Reader) // blocks until the connection ends
}

func headerContains(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

type client struct {
	hub  *Hub
	conn net.Conn
	send chan []byte

	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
}

// close tears the client down exactly once.
func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.hub.remove(c)
		_ = c.conn.Close()
	})
}

func (c *client) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	_, err := c.conn.Write(appendFrame(nil, opcode, payload))
	return err
}

func (c *client) writeLoop() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	defer c.close()
	for {
		select {
		case msg := <-c.send:
			if err := c.writeFrame(opText, msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.writeFrame(opPing, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *client) readLoop(r *bufio.Reader) {
	defer c.close()
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		f, err := readFrame(r)
		if err != nil {
			return
		}
		switch f.opcode {
		case opClose:
			code := make([]byte, 2)
			binary.BigEndian.PutUint16(code, 1000)
			_ = c.writeFrame(opClose, code)
			return
		case opPing:
			if err := c.writeFrame(opPong, f.payload); err != nil {
				return
			}
		default:
			// pong, text, binary and continuation frames carry nothing the
			// server needs; receiving them still proves the peer is alive.
		}
	}
}
