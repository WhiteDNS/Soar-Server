// WhiteDNS integration. No socket or independent egress is opened by this API.
package udpserver

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"stormdns-go/internal/config"
	"stormdns-go/internal/logger"
	"stormdns-go/internal/security"
)

type EmbeddedConfig struct {
	Domains     []string
	Key         string
	StreamLimit int
	Allowed     func() bool
	DialContext func(context.Context, string, string) (net.Conn, error)
	ExchangeDNS func(context.Context, []byte) ([]byte, error)
}

type EmbeddedServer struct {
	server  *Server
	ctx     context.Context
	cancel  context.CancelFunc
	allowed func() bool
	mu      sync.RWMutex
	once    sync.Once
	done    chan struct{}
}

func NewEmbedded(parent context.Context, in EmbeddedConfig) (*EmbeddedServer, error) {
	if len(in.Domains) == 0 || in.Key == "" || in.Allowed == nil || in.DialContext == nil || in.ExchangeDNS == nil {
		return nil, errors.New("embedded engine requires domains, key and all policy hooks")
	}
	cfg := config.EmbeddedDefaults()
	cfg.Domain = append([]string(nil), in.Domains...)
	cfg.DataEncryptionMethod = 5
	// Per-account engines are lazy; bound queues and workers independently of CPU count.
	cfg.DeferredSessionWorkers = 1
	cfg.DeferredSessionQueueLimit = 128
	cfg.DNSCacheMaxRecords = 256
	if in.StreamLimit > 0 {
		cfg.MaxStreamsPerSession = min(in.StreamLimit, 65535)
	}
	codec, err := security.NewCodec(5, in.Key)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	s := New(cfg, logger.New("WhiteDNS", "ERROR"), codec)
	s.dialStreamUpstreamFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		if ctx.Err() != nil || !in.Allowed() {
			return nil, net.ErrClosed
		}
		dialCtx, stop := context.WithTimeout(ctx, timeout)
		defer stop()
		return in.DialContext(dialCtx, network, address)
	}
	s.resolveDNSQueryFn = func(query []byte) ([]byte, error) {
		if ctx.Err() != nil || !in.Allowed() {
			return nil, net.ErrClosed
		}
		dnsCtx, stop := context.WithTimeout(ctx, 6*time.Second)
		defer stop()
		return in.ExchangeDNS(dnsCtx, query)
	}
	e := &EmbeddedServer{server: s, ctx: ctx, cancel: cancel, allowed: in.Allowed, done: make(chan struct{})}
	s.deferredDNSSession.Start(ctx)
	s.deferredConnectSession.Start(ctx)
	go func() { defer close(e.done); s.sessionCleanupLoop(ctx) }()
	return e, nil
}

func (e *EmbeddedServer) HandleDNS(query []byte) []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.ctx.Err() != nil || !e.allowed() {
		return nil
	}
	return e.server.safeHandlePacket(query)
}

func (e *EmbeddedServer) Close() error {
	e.once.Do(func() {
		e.cancel()
		e.mu.Lock()
		defer e.mu.Unlock()
		for id := 1; id <= 255; id++ {
			if record, ok := e.server.sessions.Close(uint8(id), time.Now(), 0); ok {
				e.server.cleanupClosedSession(uint8(id), record)
			}
		}
		<-e.done
	})
	return nil
}
