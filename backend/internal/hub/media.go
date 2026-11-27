package hub

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"
)

// mediaTTL is how long a reply's audio stays available: long enough for the
// satellite to fetch and play it.
const mediaTTL = 10 * time.Minute

// media serves audio clips (WAV) over HTTP for satellites to fetch: ESPHome
// plays replies and announcements from a URL.
type media struct {
	mu    sync.Mutex
	clips map[string]clip
	ln    net.Listener
	srv   *http.Server
}

type clip struct {
	data    []byte
	expires time.Time // zero: never
}

func newMedia(addr string) (*media, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, errors.WithMessagef(err, "listen for media on %s", addr)
	}
	m := &media{clips: make(map[string]clip), ln: ln}
	m.srv = &http.Server{Handler: m, ReadHeaderTimeout: 10 * time.Second}
	return m, nil
}

func (m *media) serve() error {
	if err := m.srv.Serve(m.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.WithStack(err)
	}
	return nil
}

func (m *media) close() error { return errors.WithStack(m.srv.Close()) }

// put stores a clip and returns its id. keep stores it for good (the chime).
func (m *media) put(data []byte, keep bool) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, c := range m.clips {
		if !c.expires.IsZero() && now.After(c.expires) {
			delete(m.clips, k)
		}
	}
	c := clip{data: data}
	if !keep {
		c.expires = now.Add(mediaTTL)
	}
	m.clips[id] = c
	return id
}

// url is where a satellite that reaches the hub at host fetches a clip.
func (m *media) url(host, id string) string {
	port := m.ln.Addr().(*net.TCPAddr).Port
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/media/" + id + ".wav"
}

func (m *media) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutPrefix(r.URL.Path, "/media/")
	id, ok2 := strings.CutSuffix(id, ".wav")
	if r.Method != http.MethodGet && r.Method != http.MethodHead || !ok || !ok2 {
		http.NotFound(w, r)
		return
	}
	m.mu.Lock()
	c, found := m.clips[id]
	m.mu.Unlock()
	if !found || (!c.expires.IsZero() && time.Now().After(c.expires)) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Content-Length", strconv.Itoa(len(c.data)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(c.data)
	}
}
