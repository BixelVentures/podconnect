// Minimal RFC 6455 websocket CLIENT — stdlib only (the manager is deliberately dependency-free).
//
// Scope is deliberately tiny: connect to go-librespot's ws://localhost:<port>/events, read TEXT
// frames (the JSON event stream), answer PINGs, and send client PINGs/CLOSE. It is NOT a general
// websocket library — it implements only what this one consumer needs. If anything goes wrong the
// caller (runGLEvents) reconnects and falls back to /status polling, so "return the error" is the
// universal failure mode here.
//
// RFC 6455 references are cited inline (§5.x = framing, §4.x = handshake).
package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// wsMagicGUID is the RFC 6455 §1.3 handshake GUID concatenated with the client key to derive the
// expected Sec-WebSocket-Accept.
const wsMagicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// RFC 6455 §5.2 opcodes.
const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xA
)

// wsConn is a connected websocket: the raw TCP conn plus a buffered reader over it. Reads happen on
// one goroutine (runGLEvents); writeControl may race with it, so writes take a mutex-free path by
// only ever being called from the same goroutine OR the keepalive — see runGLEvents, which sends
// PINGs from a separate goroutine. We guard writes with a small mutex to be safe.
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
}

// dialWebsocket performs the RFC 6455 §4.1 opening handshake over a plain TCP connection (ws://,
// not wss:// — this only ever targets localhost go-librespot) and returns a ready wsConn.
func dialWebsocket(rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("wsclient: only ws:// supported, got %q", u.Scheme)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "80")
	}
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}

	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return nil, err
	}

	// Random 16-byte client key, base64'd (RFC 6455 §4.1).
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	// Write the HTTP/1.1 Upgrade request. Host carries the original host:port.
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetWriteDeadline(time.Time{})

	// Read + validate the 101 response.
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	statusLine, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.Contains(statusLine, " 101") {
		conn.Close()
		return nil, fmt.Errorf("wsclient: handshake not 101: %q", strings.TrimSpace(statusLine))
	}
	// Parse headers until the blank line; grab Sec-WebSocket-Accept.
	var accept string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if i := strings.IndexByte(line, ':'); i >= 0 {
			name := strings.ToLower(strings.TrimSpace(line[:i]))
			if name == "sec-websocket-accept" {
				accept = strings.TrimSpace(line[i+1:])
			}
		}
	}
	_ = conn.SetReadDeadline(time.Time{})

	// Verify Sec-WebSocket-Accept = base64(sha1(key + GUID)) (RFC 6455 §4.2.2 step 5).
	sum := sha1.Sum([]byte(key + wsMagicGUID))
	want := base64.StdEncoding.EncodeToString(sum[:])
	if accept != want {
		conn.Close()
		return nil, fmt.Errorf("wsclient: bad Sec-WebSocket-Accept")
	}

	return &wsConn{conn: conn, br: br}, nil
}

// ReadMessage reads one application message (RFC 6455 §5.4 fragmentation aware). It transparently
// answers PINGs with PONGs, treats CLOSE as io.EOF, and reassembles TEXT/CONTINUATION fragments
// until FIN. Server→client frames are unmasked (§5.1); a masked server frame is a protocol error.
// Returns the message payload (TEXT or BINARY).
func (c *wsConn) ReadMessage() ([]byte, error) {
	var msg []byte
	var msgOpcode byte
	assembling := false
	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case wsOpPing:
			// Reply with PONG echoing the ping payload (§5.5.2/§5.5.3), then keep reading. A frame from
			// the peer proves liveness — extend the read deadline (see the PONG note).
			if err := c.writeControl(wsOpPong, payload); err != nil {
				return nil, err
			}
			_ = c.SetReadDeadline(time.Now().Add(glReadDeadline))
		case wsOpPong:
			// Keepalive pong (the reply to our 20 s Ping): the connection is alive, so extend the read
			// deadline. Without this, an idle (event-less) /events stream hit the 30 s deadline before the
			// next ping and forced a needless reconnect — the recurring "websocket connection errored:
			// StatusNoStatusRcvd" churn. A truly dead peer stops ponging, so the deadline still fires and
			// we reconnect + fall back to polling.
			_ = c.SetReadDeadline(time.Now().Add(glReadDeadline))
		case wsOpClose:
			// Best-effort echo a close, then signal EOF (§5.5.1).
			_ = c.writeControl(wsOpClose, nil)
			return nil, io.EOF
		case wsOpText, wsOpBinary:
			if assembling {
				return nil, fmt.Errorf("wsclient: new data frame mid-fragment")
			}
			if fin {
				return payload, nil // single-frame message (the common case)
			}
			msg = append(msg[:0], payload...)
			msgOpcode = opcode
			assembling = true
		case wsOpContinuation:
			if !assembling {
				return nil, fmt.Errorf("wsclient: continuation without start")
			}
			msg = append(msg, payload...)
			if fin {
				_ = msgOpcode // opcode of the assembled message (text/binary); payload returned as-is
				return msg, nil
			}
		default:
			return nil, fmt.Errorf("wsclient: unknown opcode 0x%x", opcode)
		}
	}
}

// readFrame reads exactly one RFC 6455 §5.2 frame and returns its FIN bit, opcode, and unmasked
// payload. Control frames (§5.5) must not be fragmented and carry ≤125 bytes; we enforce that.
func (c *wsConn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	rsv := h[0] & 0x70
	opcode = h[0] & 0x0f
	if rsv != 0 {
		return fin, opcode, nil, fmt.Errorf("wsclient: RSV bits set (no extensions negotiated)")
	}
	masked := h[1]&0x80 != 0
	length := uint64(h[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	// Control frames: FIN must be set and payload ≤125 (§5.5).
	if opcode >= 0x8 {
		if !fin {
			return fin, opcode, nil, fmt.Errorf("wsclient: fragmented control frame")
		}
		if length > 125 {
			return fin, opcode, nil, fmt.Errorf("wsclient: oversized control frame")
		}
	}
	// Sanity cap so a bogus length can't allocate gigabytes (events are small JSON).
	if length > 8<<20 {
		return fin, opcode, nil, fmt.Errorf("wsclient: frame too large (%d)", length)
	}
	var maskKey [4]byte
	if masked {
		// Server→client frames MUST NOT be masked (§5.1). go-librespot doesn't mask; reject it.
		return fin, opcode, nil, fmt.Errorf("wsclient: masked server frame")
	}
	if length > 0 {
		payload = make([]byte, length)
		if _, err = io.ReadFull(c.br, payload); err != nil {
			return
		}
		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i&3]
			}
		}
	}
	return fin, opcode, payload, nil
}

// writeControl sends a client→server frame (§5.3): client frames MUST be masked with a fresh random
// 4-byte key XOR'd over the payload. Used for PONG, PING, and CLOSE. A short write deadline keeps a
// wedged socket from blocking the keepalive forever.
func (c *wsConn) writeControl(opcode byte, payload []byte) error {
	if len(payload) > 125 {
		payload = payload[:125] // control payloads are ≤125 (§5.5)
	}
	var maskKey [4]byte
	if _, err := rand.Read(maskKey[:]); err != nil {
		return err
	}
	// Header: FIN=1 + opcode, then MASK=1 + 7-bit length (control payloads never exceed 125).
	buf := make([]byte, 0, 2+4+len(payload))
	buf = append(buf, 0x80|opcode)
	buf = append(buf, 0x80|byte(len(payload)))
	buf = append(buf, maskKey[:]...)
	for i, b := range payload {
		buf = append(buf, b^maskKey[i&3])
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.conn.Write(buf)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

// Ping sends a client PING (§5.5.2) — the keepalive go-librespot needs (no server heartbeat).
func (c *wsConn) Ping() error { return c.writeControl(wsOpPing, nil) }

// Close best-effort sends a CLOSE then tears down the TCP conn.
func (c *wsConn) Close() error {
	_ = c.writeControl(wsOpClose, nil)
	return c.conn.Close()
}

// SetReadDeadline exposes the underlying conn deadline so the reader can bound how long it blocks
// waiting for the next event (used to drive the client keepalive / reconnect).
func (c *wsConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }
