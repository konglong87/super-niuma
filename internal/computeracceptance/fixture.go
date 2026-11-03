package computeracceptance

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	loopbackAddress = "127.0.0.1:0"
	tokenBytes      = 32
	serverTimeout   = 5 * time.Second
)

// Fixture owns one loopback listener and a bounded event ring. Use Close even
// when acceptance fails. No files, credentials, or desktop sessions are used.
type Fixture struct {
	url       string
	host      string
	token     string
	server    *http.Server
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
	mu        sync.RWMutex
	ring      [MaxEvents]Event
	next      int
	count     int
	latest    Snapshot
}

// StartFixture binds an ephemeral IPv4 loopback port and starts serving the
// embedded page. URL carries a random token in its fragment (never a query).
func StartFixture() (*Fixture, error) {
	var token [tokenBytes]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", loopbackAddress)
	if err != nil {
		return nil, err
	}
	f := &Fixture{host: listener.Addr().String(), token: hex.EncodeToString(token[:]), done: make(chan struct{})}
	f.url = "http://" + f.host + "/#" + f.token
	f.server = &http.Server{
		Handler: http.HandlerFunc(f.serveHTTP), ReadHeaderTimeout: serverTimeout,
		ReadTimeout: serverTimeout, WriteTimeout: serverTimeout, IdleTimeout: serverTimeout,
		MaxHeaderBytes: 8 << 10, ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() {
		defer close(f.done)
		_ = f.server.Serve(listener) // Close owns shutdown; no persistent logging.
	}()
	return f, nil
}

// URL is the page URL for the main agent to open in its chosen browser/webview.
// Treat its fragment as a short-lived capability; do not persist or log it.
func (f *Fixture) URL() string { return f.url }

// Close stops the listener and active HTTP connections and waits for Serve.
// It is safe to call concurrently or more than once.
func (f *Fixture) Close() error {
	f.closeOnce.Do(func() {
		f.closeErr = f.server.Close()
		<-f.done
	})
	return f.closeErr
}

// Events returns retained events in ascending server sequence order.
func (f *Fixture) Events() []Event {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.eventsLocked()
}

// Snapshot returns state from the latest accepted report and retained events.
func (f *Fixture) Snapshot() Snapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s := f.latest
	s.Events = f.eventsLocked()
	s.Geometry = cloneGeometry(s.Geometry)
	return s
}

func (f *Fixture) eventsLocked() []Event {
	events := make([]Event, f.count)
	start := (f.next - f.count + MaxEvents) % MaxEvents
	for i := range events {
		events[i] = f.ring[(start+i)%MaxEvents]
	}
	return events
}

func (f *Fixture) record(report Report) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	for _, event := range report.Events {
		f.latest.TotalEvents++
		f.ring[f.next] = Event{DOMEvent: event, Sequence: f.latest.TotalEvents, ReceivedAt: now}
		f.next = (f.next + 1) % MaxEvents
		if f.count < MaxEvents {
			f.count++
		} else {
			f.latest.Dropped++
		}
	}
	f.latest.Geometry = cloneGeometry(report.Geometry)
	f.latest.State = *report.State
	f.latest.ClientDropped = report.ClientDropped
	f.latest.Reports++
	f.latest.UpdatedAt = now
}

func cloneGeometry(geometry *Geometry) *Geometry {
	if geometry == nil {
		return nil
	}
	copy := *geometry
	copy.Targets = append([]TargetGeometry(nil), geometry.Targets...)
	if geometry.VisualViewport != nil {
		viewport := *geometry.VisualViewport
		copy.VisualViewport = &viewport
	}
	if geometry.Calibration != nil {
		calibration := *geometry.Calibration
		copy.Calibration = &calibration
	}
	return &copy
}
