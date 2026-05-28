package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

// TCPTransport is the local compatibility transport. It opens one short-lived
// TCP connection per envelope and uses the configured EnvelopeCodec for bytes.
type TCPTransport struct {
	self  NodeConfig
	peers map[uint64]NodeConfig
	codec EnvelopeCodec
	ln    net.Listener
}

func newTCPTransport(node *Node, codec EnvelopeCodec) *TCPTransport {
	return &TCPTransport{self: node.self, peers: node.peers, codec: codec}
}

// Start listens on the node's consensus address and dispatches decoded
// envelopes to handler.
func (t *TCPTransport) Start(ctx context.Context, handler EnvelopeHandler) error {
	ln, err := net.Listen("tcp", t.self.ConsensusAddr)
	if err != nil {
		return err
	}
	t.ln = ln
	log.Printf("node %d tcp transport listening on %s", t.self.ID, t.self.ConsensusAddr)
	go func() {
		<-ctx.Done()
		_ = t.Close()
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("accept: %v", err)
				continue
			}
			go t.handleConn(conn, handler)
		}
	}()
	return nil
}

func (t *TCPTransport) handleConn(conn net.Conn, handler EnvelopeHandler) {
	defer conn.Close()
	data, err := io.ReadAll(conn)
	if err != nil {
		log.Printf("read envelope: %v", err)
		return
	}
	env, err := t.codec.Decode(data)
	if err != nil {
		log.Printf("decode envelope: %v", err)
		return
	}
	handler(env, len(data))
}

// Send dials the target node's consensus address and writes one encoded
// envelope. Dial retries make local multi-process startup less timing-sensitive.
func (t *TCPTransport) Send(ctx context.Context, to uint64, env WireEnvelope) (int, error) {
	peer, ok := t.peers[to]
	if !ok {
		return 0, fmt.Errorf("unknown peer %d", to)
	}
	data, err := t.codec.Encode(env)
	if err != nil {
		return 0, err
	}
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		conn, err := net.DialTimeout("tcp", peer.ConsensusAddr, 500*time.Millisecond)
		if err == nil {
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			_, err = conn.Write(data)
			_ = conn.Close()
			if err == nil {
				return len(data), nil
			}
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return 0, lastErr
}

// Close stops the listener if it has been started.
func (t *TCPTransport) Close() error {
	if t.ln != nil {
		return t.ln.Close()
	}
	return nil
}
