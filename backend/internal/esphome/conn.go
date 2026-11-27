// Package esphome is a client for ESPHome's native API: the protocol Alfred's
// hub uses to talk to its satellites (#54), both ESP32 speakers running
// ESPHome and laptops running linux-voice-assistant. The hub connects to the
// device (port 6053), like Home Assistant does.
//
// Messages are the protobuf types in esphomepb, generated from ESPHome's
// api.proto (vendored from aioesphomeapi, MIT, in backend/proto/esphome).
// Frames are plaintext or encrypted with Noise (NNpsk0) using the device's
// API encryption key.
package esphome

import (
	"bufio"
	"context"
	"encoding/base64"
	"log/slog"
	"net"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/sundbassen/alfred/internal/esphome/esphomepb"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// API version the client speaks (aioesphomeapi 45.3.1 sends 1.14).
const (
	apiMajor = 1
	apiMinor = 14
)

const (
	// writeTimeout bounds one write: a device that stops reading must not
	// block senders (and Close) forever.
	writeTimeout = 10 * time.Second
	// closeTimeout bounds the polite disconnect in Close.
	closeTimeout = time.Second
)

// Config says how to reach one device.
type Config struct {
	// Address is host:port, port usually 6053.
	Address string
	// Key is the device's API encryption key (base64, 32 bytes); empty for
	// plaintext devices.
	Key string
	// ClientInfo identifies the client to the device, e.g. "Alfred hub".
	ClientInfo string
	// KeepAlive is how often to ping the device (default 20 s). A device
	// that sends nothing for 3 × KeepAlive (not even a ping reply) counts
	// as gone: Recv fails and Keep reconnects.
	KeepAlive time.Duration
	// DialTimeout bounds connecting and the handshake (default 10 s).
	DialTimeout time.Duration
}

// Conn is one connection to a device. Recv must be called from a single
// goroutine; Send is safe from any.
type Conn struct {
	net   net.Conn
	r     *bufio.Reader
	codec codec

	wmu sync.Mutex

	// Name is the device's name (from its hello), Info its device info.
	Name string
	Info *esphomepb.DeviceInfoResponse

	// silentFor is how long Recv waits for any frame before it gives the
	// device up; 0 (during setup, and on the device side) waits forever.
	silentFor time.Duration

	stop chan struct{}
	once sync.Once
}

// Dial connects to a device, runs the handshake (Noise if Config.Key is
// set), and exchanges hello and device info. Failure categories:
// fault.ErrServiceUnavailable (unreachable), fault.ErrUnauthorized (wrong
// or missing key), fault.ErrInvalidValue (a malformed key, or a key for a
// plaintext device), fault.ErrDecoding (protocol errors).
func Dial(ctx context.Context, cfg Config) (*Conn, error) {
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = 20 * time.Second
	}
	if cfg.ClientInfo == "" {
		cfg.ClientInfo = "Alfred"
	}
	psk, err := decodeKey(cfg.Key)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()

	nc, err := (&net.Dialer{}).DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, errors.Prefix(errors.WithMessagef(err, "connect to %s", cfg.Address), fault.ErrServiceUnavailable)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(deadline)
	}
	// Cancelling ctx during the handshake ends it at once.
	stopCancel := context.AfterFunc(ctx, func() { _ = nc.SetDeadline(time.Unix(1, 0)) })
	c := &Conn{net: nc, r: bufio.NewReader(nc), codec: plaintext{}, stop: make(chan struct{})}
	err = c.setup(cfg, psk)
	if !stopCancel() && err == nil {
		err = errors.Prefix(errors.WithStack(ctx.Err()), fault.ErrServiceUnavailable)
	}
	if err != nil {
		nc.Close()
		return nil, errors.WithMessage(err, cfg.Address)
	}
	_ = nc.SetDeadline(time.Time{})
	c.silentFor = 3 * cfg.KeepAlive
	go c.keepAlive(cfg.KeepAlive)
	return c, nil
}

// decodeKey decodes an API encryption key; nil for "" (plaintext).
func decodeKey(key string) ([]byte, error) {
	if key == "" {
		return nil, nil
	}
	psk, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(psk) != 32 {
		return nil, errors.WithMessage(fault.ErrInvalidValue, "the encryption key must be 32 bytes, base64")
	}
	return psk, nil
}

func (c *Conn) setup(cfg Config, psk []byte) error {
	if psk != nil {
		codec, name, _, err := clientHandshake(c.net, c.r, psk)
		if err != nil {
			return ioError(err, "handshake")
		}
		c.codec, c.Name = codec, name
	}

	if err := c.Send(&esphomepb.HelloRequest{ClientInfo: cfg.ClientInfo, ApiVersionMajor: apiMajor, ApiVersionMinor: apiMinor}); err != nil {
		return err
	}
	hello, err := expect[*esphomepb.HelloResponse](c)
	if err != nil {
		return errors.WithMessage(err, "hello")
	}
	if hello.ApiVersionMajor != apiMajor {
		return errors.WithMessagef(fault.ErrDecoding, "device speaks API %d.%d, want %d.x",
			hello.ApiVersionMajor, hello.ApiVersionMinor, apiMajor)
	}
	if hello.Name != "" {
		c.Name = hello.Name
	}

	if err := c.Send(&esphomepb.DeviceInfoRequest{}); err != nil {
		return err
	}
	if c.Info, err = expect[*esphomepb.DeviceInfoResponse](c); err != nil {
		return errors.WithMessage(err, "device info")
	}
	return nil
}

// expect receives until a message of type T arrives (pings are answered
// along the way).
func expect[T proto.Message](c *Conn) (T, error) {
	var zero T
	for {
		msg, err := c.Recv()
		if err != nil {
			return zero, err
		}
		if m, ok := msg.(T); ok {
			return m, nil
		}
	}
}

// Send writes one message.
func (c *Conn) Send(msg proto.Message) error {
	return c.send(msg, writeTimeout)
}

func (c *Conn) send(msg proto.Message, timeout time.Duration) error {
	id, ok := idOf(msg)
	if !ok {
		return errors.WithMessagef(fault.ErrInvalidValue, "%T has no ESPHome message id", msg)
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		return errors.WithStack(err)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.net.SetWriteDeadline(time.Now().Add(timeout))
	if err := c.codec.write(c.net, id, data); err != nil {
		return ioError(err, "send "+string(msg.ProtoReflect().Descriptor().Name()))
	}
	return nil
}

// Recv returns the next message. It answers pings itself (and swallows ping
// responses), and fails with fault.ErrServiceUnavailable when the device
// disconnects or stays silent for 3 × KeepAlive. Messages of types this
// client doesn't know are skipped.
func (c *Conn) Recv() (proto.Message, error) {
	for {
		if c.silentFor > 0 {
			// Any frame counts as a sign of life, including ping replies.
			_ = c.net.SetReadDeadline(time.Now().Add(c.silentFor))
		}
		typ, payload, err := c.codec.read(c.r)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() && c.silentFor > 0 {
				return nil, errors.WithMessagef(fault.ErrServiceUnavailable, "the device sent nothing for %s", c.silentFor)
			}
			return nil, ioError(err, "receive")
		}
		mt, ok := typeOf(typ)
		if !ok {
			continue
		}
		msg := mt.New().Interface()
		if err := proto.Unmarshal(payload, msg); err != nil {
			return nil, errors.Prefix(errors.WithMessagef(err, "message %d", typ), fault.ErrDecoding)
		}
		switch msg.(type) {
		case *esphomepb.PingRequest:
			if err := c.Send(&esphomepb.PingResponse{}); err != nil {
				return nil, err
			}
			continue
		case *esphomepb.PingResponse:
			continue
		case *esphomepb.DisconnectRequest:
			// The device is leaving: confirm and close, without asking it
			// to disconnect in turn.
			_ = c.send(&esphomepb.DisconnectResponse{}, closeTimeout)
			c.once.Do(func() {
				close(c.stop)
				_ = c.net.Close()
			})
			return nil, errors.WithMessage(fault.ErrServiceUnavailable, "the device disconnected")
		}
		return msg, nil
	}
}

// ioError wraps a read or write error. Errors that already have a fault
// category (a protocol error, a missing key) keep just that one; others are
// I/O failures, fault.ErrServiceUnavailable.
func ioError(err error, what string) error {
	for _, category := range []error{fault.ErrDecoding, fault.ErrUnauthorized, fault.ErrInvalidValue, fault.ErrServiceUnavailable} {
		if errors.Is(err, category) {
			return errors.WithMessage(err, what)
		}
	}
	return errors.Prefix(errors.WithMessage(err, what), fault.ErrServiceUnavailable)
}

// Close ends the connection, politely if it is still up: it asks the device
// to disconnect, waiting at most a second for the write.
func (c *Conn) Close() error {
	var err error
	c.once.Do(func() {
		close(c.stop)
		_ = c.send(&esphomepb.DisconnectRequest{}, closeTimeout)
		err = c.net.Close()
	})
	return errors.WithStack(err)
}

func (c *Conn) keepAlive(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			if err := c.Send(&esphomepb.PingRequest{}); err != nil {
				c.net.Close() // Recv returns the error
				return
			}
		}
	}
}

// Keep connects to a device and runs fn with each connection, reconnecting
// with backoff (1 s, doubling to 30 s) when the device is unreachable or
// fn returns, until ctx ends. When ctx ends, Keep closes the connection, so
// an fn blocked in Recv returns. fn should return when the connection
// fails. A configuration error (a malformed key, or a key for a plaintext
// device) isn't retried: Keep logs it and returns.
func Keep(ctx context.Context, cfg Config, logger *slog.Logger, fn func(ctx context.Context, c *Conn) error) {
	backoff := time.Second
	for ctx.Err() == nil {
		c, err := Dial(ctx, cfg)
		if errors.Is(err, fault.ErrInvalidValue) {
			logger.Error("Satellite left out: its configuration is wrong", slog.String("address", cfg.Address), slog.String("error", err.Error()))
			return
		}
		if err == nil {
			logger.Info("Connected to satellite", slog.String("address", cfg.Address), slog.String("name", c.Name))
			backoff = time.Second
			stop := context.AfterFunc(ctx, func() { c.Close() })
			err = fn(ctx, c)
			stop()
			c.Close()
		}
		if ctx.Err() != nil {
			return
		}
		logger.Warn("Satellite connection lost, retrying", slog.String("address", cfg.Address),
			slog.Duration("in", backoff), slog.String("error", errString(err)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func errString(err error) string {
	if err == nil {
		return "closed"
	}
	return err.Error()
}

// registry maps message ids, from the api.proto `id` option of each
// message, to message types and back. api.proto has no package, so its
// messages register globally under bare names (HelloRequest); another
// unpackaged proto with the same names in the binary would clash.
var registry = sync.OnceValue(func() *messageRegistry {
	reg, err := buildRegistry()
	if err != nil {
		panic("esphome: " + err.Error()) // the generated code is broken: a programming error
	}
	return reg
})

type messageRegistry struct {
	byID   map[uint32]protoreflect.MessageType
	byName map[protoreflect.FullName]uint32
}

func buildRegistry() (*messageRegistry, error) {
	reg := &messageRegistry{byID: map[uint32]protoreflect.MessageType{}, byName: map[protoreflect.FullName]uint32{}}
	msgs := esphomepb.File_api_proto.Messages()
	for i := range msgs.Len() {
		md := msgs.Get(i)
		if !proto.HasExtension(md.Options(), esphomepb.E_Id) {
			continue
		}
		id := proto.GetExtension(md.Options(), esphomepb.E_Id).(uint32)
		mt, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName())
		if err != nil {
			return nil, errors.WithStack(err)
		}
		reg.byID[id] = mt
		reg.byName[md.FullName()] = id
	}
	return reg, nil
}

func idOf(msg proto.Message) (uint32, bool) {
	id, ok := registry().byName[msg.ProtoReflect().Descriptor().FullName()]
	return id, ok
}

func typeOf(id uint32) (protoreflect.MessageType, bool) {
	mt, ok := registry().byID[id]
	return mt, ok
}
