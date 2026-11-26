// Package esphometest is a fake ESPHome device for tests: it listens on a
// local port, answers hello and device info like a real device, and hands
// every other message to the test.
package esphometest

import (
	"net"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/sundbassen/alfred/internal/esphome"
	"github.com/sundbassen/alfred/internal/esphome/esphomepb"
)

// Device is a fake ESPHome device.
type Device struct {
	Name string
	Key  string // empty: plaintext

	ln       net.Listener
	mu       sync.Mutex
	received chan proto.Message
	conns    chan *esphome.Conn
	current  *esphome.Conn
}

// Start listens on a free local port. The device is closed when the test
// ends.
func Start(t testing.TB, name, key string) *Device {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &Device{Name: name, Key: key, ln: ln, received: make(chan proto.Message, 256), conns: make(chan *esphome.Conn, 4)}
	go d.serve()
	t.Cleanup(func() { ln.Close() })
	return d
}

// Addr is the address to dial.
func (d *Device) Addr() string { return d.ln.Addr().String() }

// Received delivers every message the client sent besides hello, device
// info and pings.
func (d *Device) Received() <-chan proto.Message { return d.received }

// Connected delivers each connection once the client finished hello and
// device info.
func (d *Device) Connected() <-chan *esphome.Conn { return d.conns }

// Send sends a message to the connected client, as the device.
func (d *Device) Send(msg proto.Message) error {
	d.mu.Lock()
	c := d.current
	d.mu.Unlock()
	if c == nil {
		return net.ErrClosed
	}
	return c.Send(msg)
}

// Drop closes the current connection, as if the device rebooted.
func (d *Device) Drop() {
	d.mu.Lock()
	c := d.current
	d.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

func (d *Device) serve() {
	for {
		nc, err := d.ln.Accept()
		if err != nil {
			return
		}
		go d.handle(nc)
	}
}

func (d *Device) handle(nc net.Conn) {
	c, err := esphome.Accept(nc, esphome.AcceptConfig{Key: d.Key, Name: d.Name, MAC: "02:00:00:00:00:01"})
	if err != nil {
		nc.Close()
		return
	}
	defer c.Close()
	for {
		msg, err := c.Recv()
		if err != nil {
			return
		}
		switch m := msg.(type) {
		case *esphomepb.HelloRequest:
			_ = c.Send(&esphomepb.HelloResponse{ApiVersionMajor: 1, ApiVersionMinor: m.ApiVersionMinor, ServerInfo: "fake", Name: d.Name})
		case *esphomepb.DeviceInfoRequest:
			_ = c.Send(&esphomepb.DeviceInfoResponse{Name: d.Name, FriendlyName: d.Name, MacAddress: "02:00:00:00:00:01",
				EsphomeVersion: "fake", VoiceAssistantFeatureFlags: 0b111})
			d.mu.Lock()
			d.current = c
			d.mu.Unlock()
			d.conns <- c
		default:
			d.received <- msg
		}
	}
}
