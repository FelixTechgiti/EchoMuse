package sendspin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsEcho stands up a real WebSocket server for the transport tests. The
// transport is thin and its whole job is the socket, so testing it against an
// in-memory fake would test nothing that matters.
func wsEcho(t *testing.T, handle func(*websocket.Conn)) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		handle(c)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/sendspin"
}

func TestAFrameGoesOutAndComesBack(t *testing.T) {
	url := wsEcho(t, func(c *websocket.Conn) {
		for {
			mt, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if c.WriteMessage(mt, data) != nil {
				return
			}
		}
	})
	tr, err := Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	want := EncodeAudioChunk(AudioChunk{Timestamp: 7, Data: []byte{1, 2, 3}})
	if err := tr.Send(want); err != nil {
		t.Fatal(err)
	}
	got, err := tr.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestATextFrameIsAcceptedNotRefused(t *testing.T) {
	// The pre-encryption handshake is specified as cleartext JSON, and an
	// implementation is free to send it as a text frame. Refusing one fails
	// the handshake before it starts, with an error naming the frame type
	// rather than the cause.
	url := wsEcho(t, func(c *websocket.Conn) {
		c.WriteMessage(websocket.TextMessage, []byte(`{"type":"server/hello"}`))
		time.Sleep(200 * time.Millisecond)
	})
	tr, err := Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	got, err := tr.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "server/hello") {
		t.Fatalf("got %q", got)
	}
}

func TestAnEmptyFrameIsSkippedRatherThanDispatched(t *testing.T) {
	// A zero-length frame has no type byte. Handing it up produces an
	// index-out-of-range at the first dispatch, on a connection that was
	// otherwise fine.
	url := wsEcho(t, func(c *websocket.Conn) {
		c.WriteMessage(websocket.BinaryMessage, nil)
		c.WriteMessage(websocket.BinaryMessage, []byte{TypeJSON, '{', '}'})
		time.Sleep(200 * time.Millisecond)
	})
	tr, err := Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	got, err := tr.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("an empty frame was handed up")
	}
	if got[0] != TypeJSON {
		t.Fatalf("first frame = % x", got)
	}
}

func TestADialToNowhereFailsRatherThanHanging(t *testing.T) {
	// A stale mDNS advertisement points at a host that no longer answers,
	// and an unbounded dial there parks the retry loop instead of failing
	// it.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := Dial(ctx, "ws://127.0.0.1:1/sendspin")
	if err == nil {
		t.Fatal("dialled a closed port successfully")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the dial took %s", time.Since(start))
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	// The connection teardown and a caller's own Close both reach here, and
	// a double close of the keepalive channel is a panic rather than an
	// error.
	url := wsEcho(t, func(c *websocket.Conn) { time.Sleep(200 * time.Millisecond) })
	tr, err := Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	tr.Close()
	tr.Close()
}

func TestAClosedPeerSurfacesAsAReadError(t *testing.T) {
	url := wsEcho(t, func(c *websocket.Conn) {})
	tr, err := Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if _, err := tr.Recv(); err == nil {
		t.Fatal("reading from a closed peer succeeded")
	}
}

func TestTheKeepaliveIsShorterThanTheReadDeadline(t *testing.T) {
	// Otherwise the deadline fires before the ping that would have reset
	// it, and every idle session is torn down on a timer — which presents
	// as a group that drops a speaker whenever the music pauses.
	if wsPingPeriod >= wsPongWait {
		t.Fatalf("ping period %s is not shorter than the pong wait %s",
			wsPingPeriod, wsPongWait)
	}
	// And with room for a lost ping: this fleet measures 4.6–7.1% loss.
	if wsPingPeriod*2 >= wsPongWait {
		t.Fatalf("ping period %s leaves no room for a lost ping inside %s",
			wsPingPeriod, wsPongWait)
	}
}
