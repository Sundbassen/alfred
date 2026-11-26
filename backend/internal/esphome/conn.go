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

// Config says how to reach one device.
type Config struct {
	// Address is host:port, port usually 6053.
	Address string
	// Key is the device's API encryption key (base64, 32 bytes); empty for
	// plaintext devices.
	Key string
	// ClientInfo identifies the client to the device, e.g. "Alfred hub".
	ClientInfo string
	// KeepAlive is how often to ping the device (default 20 s).
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

	stop chan struct{}
	once sync.Once
}

// Dial connects to a device, runs the handshake (Noise if Config.Key is
// set), and exchanges hello and device info. Failure categories:
// fault.ErrServiceUnavailable (unreachable), fault.ErrUnauthorized (wrong
// or missing key), fault.ErrDecoding (protocol errors).
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
	ctx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()

	nc, err := (&net.Dialer{}).DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, errors.Prefix(errors.WithMessagef(err, "connect to %s", cfg.Address), fault.ErrServiceUnavailable)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(deadline)
	}
	c := &Conn{net: nc, r: bufio.NewReader(nc), codec: plaintext{}, stop: make(chan struct{})}
	if err := c.setup(cfg); err != nil {
		nc.Close()
		return nil, errors.WithMessage(err, cfg.Address)
	}
	_ = nc.SetDeadline(time.Time{})
	go c.keepAlive(cfg.KeepAlive)
	return c, nil
}

func (c *Conn) setup(cfg Config) error {
	if cfg.Key != "" {
		psk, err := base64.StdEncoding.DecodeString(cfg.Key)
		if err != nil || len(psk) != 32 {
			return errors.WithMessage(fault.ErrInvalidValue, "the encryption key must be 32 bytes, base64")
		}
		codec, name, _, err := clientHandshake(c.net, c.r, psk)
		if err != nil {
			return err
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
	if err := c.codec.write(c.net, id, data); err != nil {
		return errors.Prefix(errors.WithMessagef(err, "send %T", msg), fault.ErrServiceUnavailable)
	}
	return nil
}

// Recv returns the next message. It answers pings itself (and swallows ping
// responses), and returns io.EOF-like errors when the device disconnects.
// Messages of types this client doesn't know are skipped.
func (c *Conn) Recv() (proto.Message, error) {
	for {
		typ, payload, err := c.codec.read(c.r)
		if err != nil {
			return nil, errors.Prefix(errors.WithMessage(err, "receive"), fault.ErrServiceUnavailable)
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
			_ = c.Send(&esphomepb.DisconnectResponse{})
			c.Close()
			return nil, errors.WithMessage(fault.ErrServiceUnavailable, "the device disconnected")
		}
		return msg, nil
	}
}

// Close ends the connection (politely, if it is still up).
func (c *Conn) Close() error {
	var err error
	c.once.Do(func() {
		close(c.stop)
		_ = c.Send(&esphomepb.DisconnectRequest{})
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
// fn returns, until ctx ends. fn should return when the connection fails.
func Keep(ctx context.Context, cfg Config, logger *slog.Logger, fn func(ctx context.Context, c *Conn) error) {
	backoff := time.Second
	for ctx.Err() == nil {
		c, err := Dial(ctx, cfg)
		if err == nil {
			logger.Info("Connected to satellite", slog.String("address", cfg.Address), slog.String("name", c.Name))
			backoff = time.Second
			err = fn(ctx, c)
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

// Message ids come from the api.proto `id` option of each message.
var (
	registry = sync.OnceValues(buildRegistry)
)

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
	reg, err := registry()
	if err != nil {
		return 0, false
	}
	id, ok := reg.byName[msg.ProtoReflect().Descriptor().FullName()]
	return id, ok
}

func typeOf(id uint32) (protoreflect.MessageType, bool) {
	reg, err := registry()
	if err != nil {
		return nil, false
	}
	mt, ok := reg.byID[id]
	return mt, ok
}
