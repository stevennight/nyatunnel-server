package edge

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stevennight/nyatunnel-common/tunnelproto"
)

// portListener serves one TCP tunnel port.
type portListener struct {
	e     *Edge
	ln    net.Listener
	route atomic.Pointer[route]

	mu    sync.Mutex
	conns map[net.Conn]struct{}
	done  chan struct{}
}

func (e *Edge) listenTCP(port int, r *route) (*portListener, error) {
	ln, err := e.opt.Listen("tcp", net.JoinHostPort(e.opt.BindAddr, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	l := &portListener{e: e, ln: ln, conns: map[net.Conn]struct{}{}, done: make(chan struct{})}
	l.route.Store(r)
	go l.serve()
	e.opt.Log.Info("edge: listening", "proto", "tcp", "port", port, "tunnel", r.TunnelID)
	return l, nil
}

func (l *portListener) serve() {
	for {
		c, err := l.ln.Accept()
		if err != nil {
			select {
			case <-l.done:
				return
			default:
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			l.e.opt.Log.Warn("edge: accept", "err", err)
			return
		}
		go l.handle(c)
	}
}

func (l *portListener) track(c net.Conn, add bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if add {
		l.conns[c] = struct{}{}
	} else {
		delete(l.conns, c)
	}
}

func (l *portListener) handle(c net.Conn) {
	defer c.Close()
	r := l.route.Load()
	if !r.live(l.e.opt.Now()) || !r.allowed(hostOf(c.RemoteAddr())) {
		return
	}
	m, ok := l.e.acquire(r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	raw, err := l.e.opt.Dialer.Dial(ctx, r.DeviceID, r.TunnelID, "tcp", c.RemoteAddr().String())
	cancel()
	if err != nil {
		m.active.Add(-1)
		l.e.opt.Log.Debug("edge: tcp dial", "tunnel", r.TunnelID, "err", err)
		return
	}
	stream := l.e.metered(raw, m)
	defer stream.Close()
	l.track(c, true)
	defer l.track(c, false)
	pipe(c, stream)
}

func (l *portListener) close() {
	close(l.done)
	l.ln.Close()
	l.mu.Lock()
	for c := range l.conns {
		c.Close()
	}
	l.mu.Unlock()
}

func hostOf(a net.Addr) string {
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return h
}

// pipe copies both ways until either side is done, then closes both.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		// Half-close when possible so protocols that shut down writing first still get their reply.
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			dst.Close()
		}
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	a.Close()
	b.Close()
}

// udpIdle closes a visitor's stream after this long without packets in either direction.
const udpIdle = 60 * time.Second

// udpListener serves one UDP tunnel port: one stream per visitor address.
type udpListener struct {
	e     *Edge
	pc    net.PacketConn
	route atomic.Pointer[route]

	mu    sync.Mutex
	peers map[string]*udpPeer
	done  chan struct{}
}

type udpPeer struct {
	stream net.Conn
	last   atomic.Int64
}

func (e *Edge) listenUDP(port int, r *route) (*udpListener, error) {
	pc, err := e.opt.ListenPacket("udp", net.JoinHostPort(e.opt.BindAddr, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	l := &udpListener{e: e, pc: pc, peers: map[string]*udpPeer{}, done: make(chan struct{})}
	l.route.Store(r)
	go l.serve()
	go l.reap()
	e.opt.Log.Info("edge: listening", "proto", "udp", "port", port, "tunnel", r.TunnelID)
	return l, nil
}

func (l *udpListener) serve() {
	buf := make([]byte, tunnelproto.MaxDatagram)
	for {
		n, addr, err := l.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		p := l.peer(addr)
		if p == nil {
			continue
		}
		p.last.Store(time.Now().UnixNano())
		if err := tunnelproto.WriteDatagram(p.stream, buf[:n]); err != nil {
			l.drop(addr.String(), p)
		}
	}
}

// peer returns the stream for addr, opening one on the first packet.
func (l *udpListener) peer(addr net.Addr) *udpPeer {
	key := addr.String()
	l.mu.Lock()
	p := l.peers[key]
	l.mu.Unlock()
	if p != nil {
		return p
	}
	r := l.route.Load()
	if !r.live(l.e.opt.Now()) || !r.allowed(hostOf(addr)) {
		return nil
	}
	m, ok := l.e.acquire(r)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	raw, err := l.e.opt.Dialer.Dial(ctx, r.DeviceID, r.TunnelID, "udp", key)
	cancel()
	if err != nil {
		m.active.Add(-1)
		return nil
	}
	p = &udpPeer{stream: l.e.metered(raw, m)}
	p.last.Store(time.Now().UnixNano())
	l.mu.Lock()
	l.peers[key] = p
	l.mu.Unlock()
	go l.replies(addr, p)
	return p
}

func (l *udpListener) replies(addr net.Addr, p *udpPeer) {
	buf := make([]byte, tunnelproto.MaxDatagram)
	for {
		d, err := tunnelproto.ReadDatagram(p.stream, buf)
		if err != nil {
			l.drop(addr.String(), p)
			return
		}
		p.last.Store(time.Now().UnixNano())
		if _, err := l.pc.WriteTo(d, addr); err != nil {
			l.drop(addr.String(), p)
			return
		}
	}
}

func (l *udpListener) drop(key string, p *udpPeer) {
	l.mu.Lock()
	if l.peers[key] == p {
		delete(l.peers, key)
	}
	l.mu.Unlock()
	p.stream.Close()
}

func (l *udpListener) reap() {
	t := time.NewTicker(udpIdle / 4)
	defer t.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-t.C:
			cutoff := time.Now().Add(-udpIdle).UnixNano()
			l.mu.Lock()
			var idle []string
			for k, p := range l.peers {
				if p.last.Load() < cutoff {
					idle = append(idle, k)
				}
			}
			l.mu.Unlock()
			for _, k := range idle {
				l.mu.Lock()
				p := l.peers[k]
				l.mu.Unlock()
				if p != nil {
					l.drop(k, p)
				}
			}
		}
	}
}

func (l *udpListener) close() {
	close(l.done)
	l.pc.Close()
	l.mu.Lock()
	peers := l.peers
	l.peers = map[string]*udpPeer{}
	l.mu.Unlock()
	for _, p := range peers {
		p.stream.Close()
	}
}
