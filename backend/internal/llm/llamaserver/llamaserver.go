// Package llamaserver runs llama.cpp's llama-server as a child process and
// talks to its OpenAI-compatible API. It is model-independent: the model
// file and sampling choices come from the caller (e.g. internal/llm/
// ministral).
package llamaserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// Config configures the llama-server process.
type Config struct {
	Binary      string // path to llama-server
	ModelFile   string // GGUF model
	Threads     int    // CPU threads (default 4)
	ContextSize int    // tokens, shared by the slots (default 4096)
	// Parallel is the number of slots (default 1). Each keeps its own
	// prompt cache, so requests with different system prompts (intents,
	// answers) don't evict each other's; llama-server routes a request to
	// the slot with the most similar prompt.
	Parallel int
	// StartupTimeout bounds model loading (default 2 minutes).
	StartupTimeout time.Duration
	Logger         *slog.Logger
}

// Server is a running llama-server. It loads the model in the background:
// until it is ready, requests return fault.ErrServiceUnavailable.
type Server struct {
	*Client
	cmd    *exec.Cmd
	exited chan struct{}
	logger *slog.Logger
}

// Start launches llama-server on a free loopback port and returns at once;
// the model loads in the background (see Ready, WaitReady). A missing
// binary or model file is fault.ErrNotFound.
func Start(cfg Config) (*Server, error) {
	for _, f := range []struct{ what, path string }{{"llama-server", cfg.Binary}, {"model", cfg.ModelFile}} {
		if _, err := os.Stat(f.path); err != nil {
			return nil, errors.Prefix(errors.WithMessagef(err, "%s %s", f.what, f.path), fault.ErrNotFound)
		}
	}
	if cfg.Threads == 0 {
		cfg.Threads = 4
	}
	if cfg.ContextSize == 0 {
		cfg.ContextSize = 4096
	}
	if cfg.Parallel == 0 {
		cfg.Parallel = 1
	}
	if cfg.StartupTimeout == 0 {
		cfg.StartupTimeout = 2 * time.Minute
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	port, err := freePort()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(cfg.Binary,
		"--model", cfg.ModelFile,
		"--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"--ctx-size", strconv.Itoa(cfg.ContextSize),
		"--threads", strconv.Itoa(cfg.Threads),
		"--parallel", strconv.Itoa(cfg.Parallel), // slots, each with its own prompt cache
		"--jinja", // use the model's own chat template
		"--no-ui",
	)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.WithStack(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, errors.Prefix(errors.WithMessage(err, "start llama-server"), fault.ErrServiceUnavailable)
	}

	s := &Server{
		Client: NewClient("http://127.0.0.1:" + strconv.Itoa(port)),
		cmd:    cmd,
		exited: make(chan struct{}),
		logger: logger,
	}
	s.Client.ready.Store(false)
	go s.forwardLogs(output)
	go func() {
		err := cmd.Wait()
		s.Client.ready.Store(false)
		close(s.exited)
		logger.Debug("llama-server exited", slog.Any("error", err))
	}()
	go s.waitHealthy(cfg.StartupTimeout)
	logger.Info("Starting llama-server", slog.String("model", cfg.ModelFile), slog.Int("port", port))
	return s, nil
}

// WaitReady blocks until the model is loaded, the server exits, or ctx ends.
func (s *Server) WaitReady(ctx context.Context) error {
	for !s.Ready() {
		select {
		case <-s.exited:
			return errors.WithMessage(fault.ErrServiceUnavailable, "llama-server exited")
		case <-ctx.Done():
			return errors.WithStack(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

// Close stops the server: an interrupt, then a kill after 5 s.
func (s *Server) Close() error {
	select {
	case <-s.exited:
		return nil
	default:
	}
	_ = s.cmd.Process.Signal(os.Interrupt)
	select {
	case <-s.exited:
	case <-time.After(5 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.exited
	}
	return nil
}

func (s *Server) waitHealthy(timeout time.Duration) {
	start := time.Now()
	deadline := start.Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-s.exited:
			return
		case <-time.After(250 * time.Millisecond):
		}
		resp, err := s.http.Get(s.baseURL + "/health")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			s.Client.ready.Store(true)
			s.logger.Info("llama-server ready", slog.Duration("load_time", time.Since(start)))
			return
		}
	}
	s.logger.Warn("llama-server did not become ready", slog.Duration("timeout", timeout))
}

// forwardLogs passes llama-server's (verbose) output to the debug log.
func (s *Server) forwardLogs(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		s.logger.Debug("llama-server", slog.String("line", scanner.Text()))
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, errors.WithMessage(err, "find a free port")
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Client talks to a llama-server's OpenAI-compatible API.
type Client struct {
	baseURL string
	http    *http.Client
	ready   atomic.Bool
}

// NewClient returns a client for a llama-server at baseURL (considered
// ready; Start manages readiness for the servers it launches).
func NewClient(baseURL string) *Client {
	c := &Client{baseURL: baseURL, http: &http.Client{}}
	c.ready.Store(true)
	return c
}

// Ready reports whether the model is loaded.
func (c *Client) Ready() bool { return c.ready.Load() }

// Message is one chat message.
type Message struct {
	Role    string `json:"role"` // "system", "user" or "assistant"
	Content string `json:"content"`
}

// Options are sampling settings for one request.
type Options struct {
	Temperature float64
	MaxTokens   int
	// LogProbs returns each generated token's log-probability.
	LogProbs bool
}

// Completion is a chat completion's answer.
type Completion struct {
	Content []byte
	// Tokens are the generated tokens, with Options.LogProbs. Their text
	// can include formatting llama-server strips from Content (a ```json
	// fence around the JSON).
	Tokens []Token
}

// Token is one generated token and its log-probability.
type Token struct {
	Text    string
	Logprob float64
}

type chatRequest struct {
	Messages       []Message      `json:"messages"`
	ResponseFormat responseFormat `json:"response_format"`
	Temperature    float64        `json:"temperature"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	CachePrompt    bool           `json:"cache_prompt"`
	LogProbs       bool           `json:"logprobs,omitempty"`
	Stream         bool           `json:"stream"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string `json:"name"`
	Strict bool   `json:"strict"`
	Schema any    `json:"schema"`
}

// ChatJSON sends a chat completion whose answer is constrained to schema (a
// JSON-marshalable JSON Schema; llama-server compiles it to a grammar, so
// the output can only match it) and returns the answer's content. The
// shared prompt prefix is cached across requests.
func (c *Client) ChatJSON(ctx context.Context, messages []Message, schema any, opts Options) (Completion, error) {
	if !c.Ready() {
		return Completion{}, errors.WithMessage(fault.ErrServiceUnavailable, "llama-server is still loading the model")
	}
	body, err := json.Marshal(chatRequest{
		Messages: messages,
		ResponseFormat: responseFormat{
			Type:       "json_schema",
			JSONSchema: jsonSchema{Name: "answer", Strict: true, Schema: schema},
		},
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
		CachePrompt: true,
		LogProbs:    opts.LogProbs,
	})
	if err != nil {
		return Completion{}, errors.WithStack(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Completion{}, errors.WithStack(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Completion{}, errors.WithStack(ctx.Err())
		}
		return Completion{}, errors.Prefix(errors.WithMessage(err, "llama-server"), fault.ErrServiceUnavailable)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Completion{}, errors.Prefix(errors.WithMessage(err, "read llama-server response"), fault.ErrServiceUnavailable)
	}
	if category := fault.ErrorFromHTTPCode(resp.StatusCode); category != nil {
		return Completion{}, errors.WithMessagef(category, "llama-server: %d %s", resp.StatusCode, bytes.TrimSpace(data))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			LogProbs *struct {
				Content []struct {
					Token   string  `json:"token"`
					Logprob float64 `json:"logprob"`
				} `json:"content"`
			} `json:"logprobs"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
		return Completion{}, errors.WithMessagef(fault.ErrDecoding, "llama-server response %s", bytes.TrimSpace(data))
	}
	choice := out.Choices[0]
	completion := Completion{Content: []byte(choice.Message.Content)}
	if choice.LogProbs != nil {
		for _, t := range choice.LogProbs.Content {
			completion.Tokens = append(completion.Tokens, Token{Text: t.Token, Logprob: t.Logprob})
		}
	}
	return completion, nil
}
