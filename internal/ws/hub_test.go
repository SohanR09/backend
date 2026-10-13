package ws

import (
	"bufio"
	"crypto/sha1" //nolint:gosec
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "dGhlIHNhbXBsZSBub25jZQ=="

// dial performs a raw client handshake against the test server.
func dial(t *testing.T, srv *httptest.Server) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	req := "GET /ws/alerts HTTP/1.1\r\nHost: test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + testKey + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	sum := sha1.Sum([]byte(testKey + handshakeGUID)) //nolint:gosec
	if got, want := resp.Header.Get("Sec-WebSocket-Accept"), base64.StdEncoding.EncodeToString(sum[:]); got != want {
		t.Fatalf("accept = %q, want %q", got, want)
	}
	return conn, br
}

// readServerText reads one unmasked text frame sent by the server.
func readServerText(t *testing.T, conn net.Conn, br *bufio.Reader) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		t.Fatalf("read header: %v", err)
	}
	if hdr[0]&0x0F != opText {
		t.Fatalf("opcode = %d, want text", hdr[0]&0x0F)
	}
	n := int(hdr[1] & 0x7F)
	if n == 126 {
		var ext [2]byte
		_, _ = io.ReadFull(br, ext[:])
		n = int(binary.BigEndian.Uint16(ext[:]))
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return string(buf)
}

func waitForClients(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.ClientCount() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("client count = %d, want %d", h.ClientCount(), want)
}

func TestBroadcastReachesAllClients(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub)
	defer srv.Close()

	c1, b1 := dial(t, srv)
	defer c1.Close()
	c2, b2 := dial(t, srv)
	defer c2.Close()
	waitForClients(t, hub, 2)

	hub.Broadcast([]byte(`{"hello":"world"}`))

	for i, p := range []struct {
		c net.Conn
		b *bufio.Reader
	}{{c1, b1}, {c2, b2}} {
		if got := readServerText(t, p.c, p.b); got != `{"hello":"world"}` {
			t.Fatalf("client %d got %q", i, got)
		}
	}
}

func TestLargeMessageUsesExtendedLength(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub)
	defer srv.Close()
	c, b := dial(t, srv)
	defer c.Close()
	waitForClients(t, hub, 1)

	msg := strings.Repeat("x", 1000)
	hub.Broadcast([]byte(msg))
	if got := readServerText(t, c, b); got != msg {
		t.Fatalf("got %d bytes, want %d", len(got), len(msg))
	}
}

func TestClientRemovedOnDisconnect(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub)
	defer srv.Close()
	c, _ := dial(t, srv)
	waitForClients(t, hub, 1)
	_ = c.Close()
	waitForClients(t, hub, 0)
}

func TestRejectsPlainHTTP(t *testing.T) {
	hub := NewHub()
	rec := httptest.NewRecorder()
	hub.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws/alerts", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}
