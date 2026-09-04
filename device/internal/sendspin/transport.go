package sendspin

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// The WebSocket transport.
//
// Thin on purpose: the state machine above it does not know it is talking
// over gorilla, which is what lets the entire protocol be tested against an
// in-memory transport. Everything here is about the socket and nothing about
// Sendspin.

const (
	// wsHandshakeTimeout bounds the dial. A stale mDNS advertisement points
	// at a host that no longer answers, and an unbounded dial there parks
	// the retry loop instead of failing it.
	wsHandshakeTimeout = 10 * time.Second

	// wsWriteTimeout bounds a single frame write. Without it a device whose
	// peer has stopped reading blocks in Send forever while the audio
	// buffer drains — the same failure the controller's own speaker plane
	// hit, from the other end.
	wsWriteTimeout = 10 * time.Second

	// wsPongWait / wsPingPeriod keep a dead link from looking like a quiet
	// one. This fleet's links are measured at 4.6–7.1% packet loss and TCP
	// hides a dead peer for minutes; a silent Sendspin session is
	// indistinguishable from a paused group, so the ping is the only thing
	// that tells them apart.
	wsPongWait   = 60 * time.Second
	wsPingPeriod = 20 * time.Second
)

// WSTransport is a Sendspin Transport over a WebSocket.
type WSTransport struct {
	conn *websocket.Conn
	// stopPing closes the keepalive goroutine. Held rather than left to the
	// connection's own close because a goroutine writing pings to a closed
	// connection logs an error per tick forever.
	stopPing chan struct{}
}

// Dial connects to a Sendspin server.
func Dial(ctx context.Context, wsURL string) (*WSTransport, error) {
	d := websocket.Dialer{HandshakeTimeout: wsHandshakeTimeout}
	conn, resp, err := d.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("sendspin: dial %s: %w (HTTP %d)", wsURL, err, resp.StatusCode)
		}
		return nil, fmt.Errorf("sendspin: dial %s: %w", wsURL, err)
	}

	t := &WSTransport{conn: conn, stopPing: make(chan struct{})}

	// The read deadline is extended by every pong AND by every frame that
	// arrives: an active stream is proof of life, and a session carrying
	// audio must not be torn down because a pong was the thing that got
	// lost.
	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	go t.keepalive()
	return t, nil
}

func (t *WSTransport) keepalive() {
	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopPing:
			return
		case <-ticker.C:
			deadline := time.Now().Add(wsWriteTimeout)
			if err := t.conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				// The read side will surface this as a closed connection;
				// returning here just stops a dead ticker from logging.
				return
			}
		}
	}
}

// Send writes one frame. Called from a single goroutine, so gorilla's
// one-writer rule is satisfied without a mutex here — except for the
// keepalive, which uses WriteControl and is explicitly allowed to run
// concurrently with a writer.
func (t *WSTransport) Send(frame []byte) error {
	if err := t.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
		return err
	}
	return t.conn.WriteMessage(websocket.BinaryMessage, frame)
}

// Recv reads one frame.
//
// TEXT FRAMES ARE ACCEPTED, not just binary. The pre-encryption handshake is
// specified as cleartext JSON and an implementation is free to send it as a
// text frame; refusing one would fail the handshake before it started, with
// an error naming the frame type rather than the cause.
func (t *WSTransport) Recv() ([]byte, error) {
	for {
		msgType, data, err := t.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		// Any frame is proof of life, not just a pong.
		t.conn.SetReadDeadline(time.Now().Add(wsPongWait))
		switch msgType {
		case websocket.BinaryMessage, websocket.TextMessage:
			if len(data) == 0 {
				continue
			}
			return data, nil
		default:
			continue
		}
	}
}

// Close shuts the socket down. Idempotent: the connection teardown and a
// caller's own Close both reach here.
func (t *WSTransport) Close() error {
	select {
	case <-t.stopPing:
	default:
		close(t.stopPing)
	}
	return t.conn.Close()
}
