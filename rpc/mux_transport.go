package rpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/imajinyun/gofly/core"
)

const (
	experimentalMuxFrameVersion byte = 1

	experimentalMuxFrameOpen       byte = 1
	experimentalMuxFrameData       byte = 2
	experimentalMuxFrameClose      byte = 3
	experimentalMuxFrameCancel     byte = 4
	experimentalMuxFrameWindow     byte = 5
	experimentalMuxFrameFin        byte = 6
	experimentalMuxFramePing       byte = 7
	experimentalMuxFramePong       byte = 8
	experimentalMuxFrameGoAway     byte = 9
	experimentalMuxFrameWindowConn byte = 10
	experimentalMuxFrameDataFrag   byte = 11
	experimentalMuxFrameDataEnd    byte = 12
)

var (
	ErrExperimentalMuxTransportClosed = errors.New("rpc experimental mux transport is closed")
	ErrExperimentalMuxStreamClosed    = errors.New("rpc experimental mux stream is closed")
)

const (
	experimentalMuxReasonDraining             = "draining"
	experimentalMuxReasonPeerDraining         = "peer_draining"
	experimentalMuxReasonMaxConcurrentStreams = "max_concurrent_streams"
)

// ExperimentalMuxTransport is an isolated spike for stream multiplexing over a
// single net.Conn. It is not wired into the HTTP upgrade stream path.
type ExperimentalMuxTransport struct {
	conn              net.Conn
	role              string
	maxFrame          int64
	maxMessage        int64
	receiveQueueSize  int
	connectionWindow  int
	maxStreams        int
	keepaliveInterval time.Duration
	keepaliveIdle     time.Duration
	payload           PayloadCodec
	frame             FrameCodec

	nextID uint64

	writeMu sync.Mutex

	mu                sync.Mutex
	streams           map[uint64]*ExperimentalMuxStream
	closed            bool
	draining          bool
	remoteDraining    bool
	drainReason       string
	remoteDrainReason string

	accept     chan *ExperimentalMuxStream
	connCredit chan struct{}
	done       chan struct{}
	once       sync.Once

	framesIn                  atomic.Int64
	framesOut                 atomic.Int64
	dataFramesIn              atomic.Int64
	dataFramesOut             atomic.Int64
	fragmentFramesIn          atomic.Int64
	fragmentFramesOut         atomic.Int64
	openFramesIn              atomic.Int64
	openFramesOut             atomic.Int64
	closeFramesIn             atomic.Int64
	closeFramesOut            atomic.Int64
	cancelFramesIn            atomic.Int64
	cancelFramesOut           atomic.Int64
	windowFramesIn            atomic.Int64
	windowFramesOut           atomic.Int64
	connectionWindowFramesIn  atomic.Int64
	connectionWindowFramesOut atomic.Int64
	finFramesIn               atomic.Int64
	finFramesOut              atomic.Int64
	pingFramesIn              atomic.Int64
	pingFramesOut             atomic.Int64
	pongFramesIn              atomic.Int64
	pongFramesOut             atomic.Int64
	goAwayFramesIn            atomic.Int64
	goAwayFramesOut           atomic.Int64
	bytesIn                   atomic.Int64
	bytesOut                  atomic.Int64
	openedStreams             atomic.Int64
	acceptedStreams           atomic.Int64
	closedStreams             atomic.Int64
	canceledStreams           atomic.Int64
	halfClosedStreams         atomic.Int64
	backpressureEvents        atomic.Int64
	creditWaits               atomic.Int64
	connectionCreditWaits     atomic.Int64
	idleTimeouts              atomic.Int64
	localRejects              atomic.Int64
	remoteRejects             atomic.Int64
	drainRejects              atomic.Int64
	lastStreamID              atomic.Uint64
	lastFrameReadAt           atomic.Int64
	lastFrameWrittenAt        atomic.Int64
	lastPingAt                atomic.Int64
	lastPongAt                atomic.Int64
	lastCloseCode             atomic.Value
	lastCloseReason           atomic.Value
}

// ExperimentalMuxTransportOption customizes ExperimentalMuxTransport.
type ExperimentalMuxTransportOption func(*ExperimentalMuxTransport)

// ExperimentalMuxTransportSnapshot reports observable mux transport state.
type ExperimentalMuxTransportSnapshot struct {
	Role                      string        `json:"role,omitempty"`
	ActiveStreams             int           `json:"activeStreams"`
	OpenedStreams             int64         `json:"openedStreams,omitempty"`
	AcceptedStreams           int64         `json:"acceptedStreams,omitempty"`
	ClosedStreams             int64         `json:"closedStreams,omitempty"`
	CanceledStreams           int64         `json:"canceledStreams,omitempty"`
	FramesIn                  int64         `json:"framesIn,omitempty"`
	FramesOut                 int64         `json:"framesOut,omitempty"`
	DataFramesIn              int64         `json:"dataFramesIn,omitempty"`
	DataFramesOut             int64         `json:"dataFramesOut,omitempty"`
	OpenFramesIn              int64         `json:"openFramesIn,omitempty"`
	OpenFramesOut             int64         `json:"openFramesOut,omitempty"`
	CloseFramesIn             int64         `json:"closeFramesIn,omitempty"`
	CloseFramesOut            int64         `json:"closeFramesOut,omitempty"`
	CancelFramesIn            int64         `json:"cancelFramesIn,omitempty"`
	CancelFramesOut           int64         `json:"cancelFramesOut,omitempty"`
	WindowFramesIn            int64         `json:"windowFramesIn,omitempty"`
	WindowFramesOut           int64         `json:"windowFramesOut,omitempty"`
	ConnectionWindowFramesIn  int64         `json:"connectionWindowFramesIn,omitempty"`
	ConnectionWindowFramesOut int64         `json:"connectionWindowFramesOut,omitempty"`
	FinFramesIn               int64         `json:"finFramesIn,omitempty"`
	FinFramesOut              int64         `json:"finFramesOut,omitempty"`
	PingFramesIn              int64         `json:"pingFramesIn,omitempty"`
	PingFramesOut             int64         `json:"pingFramesOut,omitempty"`
	PongFramesIn              int64         `json:"pongFramesIn,omitempty"`
	PongFramesOut             int64         `json:"pongFramesOut,omitempty"`
	GoAwayFramesIn            int64         `json:"goAwayFramesIn,omitempty"`
	GoAwayFramesOut           int64         `json:"goAwayFramesOut,omitempty"`
	BytesIn                   int64         `json:"bytesIn,omitempty"`
	BytesOut                  int64         `json:"bytesOut,omitempty"`
	FragmentFramesIn          int64         `json:"fragmentFramesIn,omitempty"`
	FragmentFramesOut         int64         `json:"fragmentFramesOut,omitempty"`
	HalfClosedStreams         int64         `json:"halfClosedStreams,omitempty"`
	BackpressureEvents        int64         `json:"backpressureEvents,omitempty"`
	CreditWaits               int64         `json:"creditWaits,omitempty"`
	ConnectionCreditWaits     int64         `json:"connectionCreditWaits,omitempty"`
	IdleTimeouts              int64         `json:"idleTimeouts,omitempty"`
	LocalRejects              int64         `json:"localRejects,omitempty"`
	RemoteRejects             int64         `json:"remoteRejects,omitempty"`
	DrainRejects              int64         `json:"drainRejects,omitempty"`
	ReceiveQueueSize          int           `json:"receiveQueueSize,omitempty"`
	ConnectionWindow          int           `json:"connectionWindow,omitempty"`
	MaxMessageBytes           int64         `json:"maxMessageBytes,omitempty"`
	MaxStreams                int           `json:"maxStreams,omitempty"`
	KeepaliveInterval         time.Duration `json:"keepaliveInterval,omitempty"`
	KeepaliveIdle             time.Duration `json:"keepaliveIdle,omitempty"`
	LastFrameReadAt           time.Time     `json:"lastFrameReadAt,omitempty"`
	LastFrameWrittenAt        time.Time     `json:"lastFrameWrittenAt,omitempty"`
	LastPingAt                time.Time     `json:"lastPingAt,omitempty"`
	LastPongAt                time.Time     `json:"lastPongAt,omitempty"`
	Liveness                  string        `json:"liveness,omitempty"`
	Draining                  bool          `json:"draining,omitempty"`
	RemoteDraining            bool          `json:"remoteDraining,omitempty"`
	DrainReason               string        `json:"drainReason,omitempty"`
	RemoteDrainReason         string        `json:"remoteDrainReason,omitempty"`
	LastStreamID              uint64        `json:"lastStreamID,omitempty"`
	LastCloseCode             Code          `json:"lastCloseCode,omitempty"`
	LastCloseReason           string        `json:"lastCloseReason,omitempty"`
	Closed                    bool          `json:"closed"`
}

// ExperimentalMuxStream is a logical stream carried by ExperimentalMuxTransport.
type ExperimentalMuxStream struct {
	id uint64
	t  *ExperimentalMuxTransport

	recv   chan experimentalMuxStreamEvent
	credit chan struct{}
	done   chan struct{}
	once   sync.Once

	mu          sync.Mutex
	localClosed bool
	remoteDone  bool
	localDone   bool
	aborted     bool
	fragments   []byte
}

type experimentalMuxStreamEvent struct {
	msg Message
	err error
}

type experimentalMuxFrame struct {
	typ      byte
	streamID uint64
	code     Code
	reason   string
	window   uint32
	payload  []byte
}

// NewExperimentalMuxTransport creates an experimental mux transport over conn.
func NewExperimentalMuxTransport(conn net.Conn, opts ...ExperimentalMuxTransportOption) *ExperimentalMuxTransport {
	t := &ExperimentalMuxTransport{
		conn:             conn,
		role:             "client",
		maxFrame:         DefaultMaxFrameBytes,
		receiveQueueSize: 16,
		payload:          NoopPayloadCodec{},
		frame:            JSONFrameCodec{},
		nextID:           1,
		streams:          make(map[uint64]*ExperimentalMuxStream),
		accept:           make(chan *ExperimentalMuxStream, 64),
		done:             make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	if t.receiveQueueSize <= 0 {
		t.receiveQueueSize = 1
	}
	if t.connectionWindow <= 0 {
		t.connectionWindow = t.receiveQueueSize
	}
	if t.maxFrame <= 0 {
		t.maxFrame = DefaultMaxFrameBytes
	}
	if t.maxMessage <= 0 {
		t.maxMessage = t.maxFrame * 16
	}
	if t.payload == nil {
		t.payload = NoopPayloadCodec{}
	}
	if t.frame == nil {
		t.frame = JSONFrameCodec{}
	}
	if t.nextID == 0 {
		t.nextID = 2
	}
	t.connCredit = make(chan struct{}, t.connectionWindow)
	t.addConnectionCredit(uint32(t.connectionWindow))
	t.lastCloseCode.Store(CodeOK)
	t.lastCloseReason.Store("")
	now := time.Now()
	t.lastFrameReadAt.Store(now.UnixNano())
	t.lastFrameWrittenAt.Store(now.UnixNano())
	go t.readLoop()
	if t.keepaliveInterval > 0 {
		go t.keepaliveLoop()
	}
	return t
}

// WithExperimentalMuxServerRole makes locally opened stream IDs even.
func WithExperimentalMuxServerRole() ExperimentalMuxTransportOption {
	return func(t *ExperimentalMuxTransport) {
		t.role = "server"
		t.nextID = 2
	}
}

// WithExperimentalMuxReceiveQueueSize sets the per-stream inbound queue size.
func WithExperimentalMuxReceiveQueueSize(size int) ExperimentalMuxTransportOption {
	return func(t *ExperimentalMuxTransport) {
		if size > 0 {
			t.receiveQueueSize = size
		}
	}
}

// WithExperimentalMuxConnectionWindow limits unconsumed data frames across the connection.
func WithExperimentalMuxConnectionWindow(size int) ExperimentalMuxTransportOption {
	return func(t *ExperimentalMuxTransport) {
		if size > 0 {
			t.connectionWindow = size
		}
	}
}

// WithExperimentalMuxMaxConcurrentStreams limits active logical streams per connection.
func WithExperimentalMuxMaxConcurrentStreams(max int) ExperimentalMuxTransportOption {
	return func(t *ExperimentalMuxTransport) {
		if max > 0 {
			t.maxStreams = max
		}
	}
}

// WithExperimentalMuxMaxFrameBytes sets the maximum encoded mux frame size.
func WithExperimentalMuxMaxFrameBytes(max int64) ExperimentalMuxTransportOption {
	return func(t *ExperimentalMuxTransport) {
		if max > 0 {
			t.maxFrame = max
		}
	}
}

// WithExperimentalMuxMaxMessageBytes caps reassembled logical messages.
func WithExperimentalMuxMaxMessageBytes(max int64) ExperimentalMuxTransportOption {
	return func(t *ExperimentalMuxTransport) {
		if max > 0 {
			t.maxMessage = max
		}
	}
}

// WithExperimentalMuxPayloadCodec sets the payload codec for data frames.
func WithExperimentalMuxPayloadCodec(codec PayloadCodec) ExperimentalMuxTransportOption {
	return func(t *ExperimentalMuxTransport) {
		if codec != nil {
			t.payload = codec
		}
	}
}

// WithExperimentalMuxFrameCodec sets the message codec for data frames.
func WithExperimentalMuxFrameCodec(codec FrameCodec) ExperimentalMuxTransportOption {
	return func(t *ExperimentalMuxTransport) {
		if codec != nil {
			t.frame = codec
		}
	}
}

// WithExperimentalMuxKeepalive enables connection-level ping/pong liveness.
func WithExperimentalMuxKeepalive(interval, idle time.Duration) ExperimentalMuxTransportOption {
	return func(t *ExperimentalMuxTransport) {
		if interval > 0 {
			t.keepaliveInterval = interval
		}
		if idle > 0 {
			t.keepaliveIdle = idle
		}
	}
}

// OpenStream opens a local logical stream and announces it to the peer.
func (t *ExperimentalMuxTransport) OpenStream(ctx context.Context) (*ExperimentalMuxStream, error) {
	if t == nil {
		return nil, ErrExperimentalMuxTransportClosed
	}
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	streamID := atomic.AddUint64(&t.nextID, 2) - 2
	stream, err := t.registerStream(streamID, true)
	if err != nil {
		reason := reasonFromError(err)
		t.localRejects.Add(1)
		if isExperimentalMuxDrainReason(reason) {
			t.drainRejects.Add(1)
		}
		t.rememberClose(streamID, CodeUnavailable, reason)
		return nil, err
	}
	t.openedStreams.Add(1)
	t.lastStreamID.Store(streamID)
	if err := t.writeFrame(ctx, experimentalMuxFrame{typ: experimentalMuxFrameOpen, streamID: streamID}); err != nil {
		t.removeStream(streamID, false)
		return nil, err
	}
	t.openFramesOut.Add(1)
	return stream, nil
}

// Drain sends a GOAWAY-like control frame and rejects new streams while allowing
// active streams to finish.
func (t *ExperimentalMuxTransport) Drain(ctx context.Context, reason string) error {
	if t == nil {
		return ErrExperimentalMuxTransportClosed
	}
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if reason == "" {
		reason = experimentalMuxReasonDraining
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrExperimentalMuxTransportClosed
	}
	alreadyDraining := t.draining
	t.draining = true
	t.drainReason = reason
	t.mu.Unlock()
	if alreadyDraining {
		return nil
	}
	if err := t.writeFrame(ctx, experimentalMuxFrame{typ: experimentalMuxFrameGoAway, code: CodeUnavailable, reason: reason}); err != nil {
		return err
	}
	t.goAwayFramesOut.Add(1)
	t.rememberClose(0, CodeUnavailable, reason)
	return nil
}

// AcceptStream waits for a peer-opened logical stream.
func (t *ExperimentalMuxTransport) AcceptStream(ctx context.Context) (*ExperimentalMuxStream, error) {
	if t == nil {
		return nil, ErrExperimentalMuxTransportClosed
	}
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case stream, ok := <-t.accept:
		if !ok {
			return nil, ErrExperimentalMuxTransportClosed
		}
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, ErrExperimentalMuxTransportClosed
	}
}

// Close closes the underlying transport and all active logical streams.
func (t *ExperimentalMuxTransport) Close() error {
	if t == nil {
		return nil
	}
	var err error
	t.once.Do(func() {
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return
		}
		t.closed = true
		streams := make([]*ExperimentalMuxStream, 0, len(t.streams))
		for id, stream := range t.streams {
			delete(t.streams, id)
			streams = append(streams, stream)
		}
		t.mu.Unlock()
		for _, stream := range streams {
			stream.fail(ErrExperimentalMuxTransportClosed)
		}
		close(t.done)
		if t.conn != nil {
			err = t.conn.Close()
		}
	})
	return err
}

// Snapshot returns a point-in-time mux transport state.
func (t *ExperimentalMuxTransport) Snapshot() ExperimentalMuxTransportSnapshot {
	if t == nil {
		return ExperimentalMuxTransportSnapshot{Closed: true}
	}
	t.mu.Lock()
	active := len(t.streams)
	closed := t.closed
	draining := t.draining
	remoteDraining := t.remoteDraining
	drainReason := t.drainReason
	remoteDrainReason := t.remoteDrainReason
	t.mu.Unlock()
	code, _ := t.lastCloseCode.Load().(Code)
	reason, _ := t.lastCloseReason.Load().(string)
	lastFrameReadAt := unixNanoToTime(t.lastFrameReadAt.Load())
	lastFrameWrittenAt := unixNanoToTime(t.lastFrameWrittenAt.Load())
	lastPingAt := unixNanoToTime(t.lastPingAt.Load())
	lastPongAt := unixNanoToTime(t.lastPongAt.Load())
	liveness := "alive"
	if closed {
		liveness = "closed"
	} else if draining {
		liveness = "draining"
	} else if t.keepaliveIdle > 0 && !lastFrameReadAt.IsZero() && time.Since(lastFrameReadAt) > t.keepaliveIdle {
		liveness = "idle"
	}
	return ExperimentalMuxTransportSnapshot{
		Role:                      t.role,
		ActiveStreams:             active,
		OpenedStreams:             t.openedStreams.Load(),
		AcceptedStreams:           t.acceptedStreams.Load(),
		ClosedStreams:             t.closedStreams.Load(),
		CanceledStreams:           t.canceledStreams.Load(),
		FramesIn:                  t.framesIn.Load(),
		FramesOut:                 t.framesOut.Load(),
		DataFramesIn:              t.dataFramesIn.Load(),
		DataFramesOut:             t.dataFramesOut.Load(),
		FragmentFramesIn:          t.fragmentFramesIn.Load(),
		FragmentFramesOut:         t.fragmentFramesOut.Load(),
		OpenFramesIn:              t.openFramesIn.Load(),
		OpenFramesOut:             t.openFramesOut.Load(),
		CloseFramesIn:             t.closeFramesIn.Load(),
		CloseFramesOut:            t.closeFramesOut.Load(),
		CancelFramesIn:            t.cancelFramesIn.Load(),
		CancelFramesOut:           t.cancelFramesOut.Load(),
		WindowFramesIn:            t.windowFramesIn.Load(),
		WindowFramesOut:           t.windowFramesOut.Load(),
		ConnectionWindowFramesIn:  t.connectionWindowFramesIn.Load(),
		ConnectionWindowFramesOut: t.connectionWindowFramesOut.Load(),
		FinFramesIn:               t.finFramesIn.Load(),
		FinFramesOut:              t.finFramesOut.Load(),
		PingFramesIn:              t.pingFramesIn.Load(),
		PingFramesOut:             t.pingFramesOut.Load(),
		PongFramesIn:              t.pongFramesIn.Load(),
		PongFramesOut:             t.pongFramesOut.Load(),
		GoAwayFramesIn:            t.goAwayFramesIn.Load(),
		GoAwayFramesOut:           t.goAwayFramesOut.Load(),
		BytesIn:                   t.bytesIn.Load(),
		BytesOut:                  t.bytesOut.Load(),
		HalfClosedStreams:         t.halfClosedStreams.Load(),
		BackpressureEvents:        t.backpressureEvents.Load(),
		CreditWaits:               t.creditWaits.Load(),
		ConnectionCreditWaits:     t.connectionCreditWaits.Load(),
		IdleTimeouts:              t.idleTimeouts.Load(),
		LocalRejects:              t.localRejects.Load(),
		RemoteRejects:             t.remoteRejects.Load(),
		DrainRejects:              t.drainRejects.Load(),
		ReceiveQueueSize:          t.receiveQueueSize,
		ConnectionWindow:          t.connectionWindow,
		MaxMessageBytes:           t.maxMessage,
		MaxStreams:                t.maxStreams,
		KeepaliveInterval:         t.keepaliveInterval,
		KeepaliveIdle:             t.keepaliveIdle,
		LastFrameReadAt:           lastFrameReadAt,
		LastFrameWrittenAt:        lastFrameWrittenAt,
		LastPingAt:                lastPingAt,
		LastPongAt:                lastPongAt,
		Liveness:                  liveness,
		Draining:                  draining,
		RemoteDraining:            remoteDraining,
		DrainReason:               drainReason,
		RemoteDrainReason:         remoteDrainReason,
		LastStreamID:              t.lastStreamID.Load(),
		LastCloseCode:             code,
		LastCloseReason:           reason,
		Closed:                    closed,
	}
}

// ID returns the stream ID assigned by the mux transport.
func (s *ExperimentalMuxStream) ID() uint64 {
	if s == nil {
		return 0
	}
	return s.id
}

// Send sends a data message on the logical stream.
func (s *ExperimentalMuxStream) Send(ctx context.Context, msg Message) error {
	if s == nil || s.t == nil {
		return ErrExperimentalMuxStreamClosed
	}
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	localClosed := s.localClosed
	localDone := s.localDone
	aborted := s.aborted
	s.mu.Unlock()
	if localClosed || localDone || aborted {
		return ErrExperimentalMuxStreamClosed
	}
	if err := s.acquireCredit(ctx); err != nil {
		return err
	}
	if err := s.t.acquireConnectionCredit(ctx); err != nil {
		s.releaseCredit()
		return err
	}
	encoded, err := s.t.payload.Encode(msg.Payload)
	if err != nil {
		s.t.releaseConnectionCredit()
		s.releaseCredit()
		return err
	}
	msg.Payload = encoded
	if msg.Codec == "" {
		msg.Codec = s.t.payload.Name()
	}
	data, err := s.t.frame.Marshal(msg)
	if err != nil {
		s.t.releaseConnectionCredit()
		s.releaseCredit()
		return err
	}
	if err := s.t.writeDataFrames(ctx, s.id, data); err != nil {
		s.t.releaseConnectionCredit()
		s.releaseCredit()
		return err
	}
	return nil
}

// Receive receives the next data message or terminal event for the stream.
func (s *ExperimentalMuxStream) Receive(ctx context.Context) (Message, error) {
	if s == nil {
		return Message{}, ErrExperimentalMuxStreamClosed
	}
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	select {
	case event, ok := <-s.recv:
		if !ok {
			return Message{}, ErrExperimentalMuxStreamClosed
		}
		if event.err != nil {
			if errors.Is(event.err, io.EOF) {
				s.markRemoteDone()
				s.t.removeStreamIfDone(s.id)
			} else {
				s.closeDone()
			}
		} else {
			s.t.sendWindowUpdateAsync(s.id, 1)
			s.t.sendConnectionWindowUpdateAsync(1)
		}
		return event.msg, event.err
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case <-s.done:
		select {
		case event, ok := <-s.recv:
			if ok {
				return event.msg, event.err
			}
		default:
		}
		return Message{}, ErrExperimentalMuxStreamClosed
	}
}

// Close sends a normal close frame for the logical stream.
func (s *ExperimentalMuxStream) Close(ctx context.Context, reason string) error {
	return s.CloseWithCode(ctx, CodeOK, reason)
}

// CloseSend half-closes the local sending direction while keeping receive open.
func (s *ExperimentalMuxStream) CloseSend(ctx context.Context, reason string) error {
	if s == nil || s.t == nil {
		return ErrExperimentalMuxStreamClosed
	}
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.localClosed || s.localDone {
		s.mu.Unlock()
		return nil
	}
	s.localDone = true
	s.mu.Unlock()
	if reason == "" {
		reason = "half_close"
	}
	if err := s.t.writeFrame(ctx, experimentalMuxFrame{typ: experimentalMuxFrameFin, streamID: s.id, code: CodeOK, reason: reason}); err != nil {
		return err
	}
	s.t.finFramesOut.Add(1)
	s.t.halfClosedStreams.Add(1)
	s.t.rememberClose(s.id, CodeOK, reason)
	s.t.removeStreamIfDone(s.id)
	return nil
}

// Cancel sends a cancel frame for the logical stream.
func (s *ExperimentalMuxStream) Cancel(ctx context.Context, reason string) error {
	return s.CloseWithCode(ctx, CodeCanceled, reason)
}

// CloseWithCode sends a terminal frame for the logical stream.
func (s *ExperimentalMuxStream) CloseWithCode(ctx context.Context, code Code, reason string) error {
	if s == nil || s.t == nil {
		return ErrExperimentalMuxStreamClosed
	}
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.localClosed {
		s.mu.Unlock()
		return nil
	}
	s.localClosed = true
	s.mu.Unlock()
	typ := experimentalMuxFrameClose
	if code == CodeCanceled {
		typ = experimentalMuxFrameCancel
	}
	if reason == "" {
		reason = string(code)
	}
	if err := s.t.writeFrame(ctx, experimentalMuxFrame{typ: typ, streamID: s.id, code: code, reason: reason}); err != nil {
		return err
	}
	if typ == experimentalMuxFrameCancel {
		s.t.cancelFramesOut.Add(1)
		s.t.canceledStreams.Add(1)
	} else {
		s.t.closeFramesOut.Add(1)
		s.t.closedStreams.Add(1)
	}
	s.t.rememberClose(s.id, code, reason)
	s.t.removeStream(s.id, true)
	s.closeDone()
	return nil
}

func (t *ExperimentalMuxTransport) readLoop() {
	var terminal error
	defer func() {
		if terminal == nil {
			terminal = ErrExperimentalMuxTransportClosed
		}
		t.failAll(terminal)
	}()
	for {
		frame, bytesRead, err := t.readFrame()
		if err != nil {
			terminal = err
			return
		}
		t.framesIn.Add(1)
		t.bytesIn.Add(bytesRead)
		if err := t.dispatchFrame(frame); err != nil {
			terminal = err
			return
		}
	}
}

func (t *ExperimentalMuxTransport) dispatchFrame(frame experimentalMuxFrame) error {
	if frame.streamID != 0 {
		t.lastStreamID.Store(frame.streamID)
	}
	switch frame.typ {
	case experimentalMuxFrameOpen:
		t.openFramesIn.Add(1)
		_ = t.getOrAcceptStream(frame.streamID, true)
	case experimentalMuxFrameData:
		return t.dispatchDataPayload(frame.streamID, frame.payload)
	case experimentalMuxFrameDataFrag:
		t.fragmentFramesIn.Add(1)
		stream := t.getOrAcceptStream(frame.streamID, true)
		if stream == nil {
			return nil
		}
		return stream.appendFragment(frame.payload)
	case experimentalMuxFrameDataEnd:
		t.fragmentFramesIn.Add(1)
		stream := t.getOrAcceptStream(frame.streamID, true)
		if stream == nil {
			return nil
		}
		payload, err := stream.finishFragments(frame.payload)
		if err != nil {
			return err
		}
		return t.dispatchDataPayload(frame.streamID, payload)
	case experimentalMuxFrameClose:
		t.closeFramesIn.Add(1)
		t.closedStreams.Add(1)
		t.rememberClose(frame.streamID, frame.code, frame.reason)
		if stream := t.lookupStream(frame.streamID); stream != nil {
			stream.markAborted()
			stream.deliverTerminal(io.EOF)
		}
		t.removeStream(frame.streamID, false)
	case experimentalMuxFrameCancel:
		t.cancelFramesIn.Add(1)
		t.canceledStreams.Add(1)
		t.rememberClose(frame.streamID, frame.code, frame.reason)
		if stream := t.lookupStream(frame.streamID); stream != nil {
			stream.markAborted()
			stream.deliverTerminal(NewError(frame.code, frame.reason))
		}
		t.removeStream(frame.streamID, false)
	case experimentalMuxFrameWindow:
		t.windowFramesIn.Add(1)
		if stream := t.lookupStream(frame.streamID); stream != nil {
			stream.addCredit(frame.window)
		}
	case experimentalMuxFrameWindowConn:
		t.connectionWindowFramesIn.Add(1)
		t.addConnectionCredit(frame.window)
	case experimentalMuxFrameFin:
		t.finFramesIn.Add(1)
		t.halfClosedStreams.Add(1)
		t.rememberClose(frame.streamID, frame.code, frame.reason)
		if stream := t.lookupStream(frame.streamID); stream != nil {
			stream.deliverTerminal(io.EOF)
		}
	case experimentalMuxFramePing:
		t.pingFramesIn.Add(1)
		now := time.Now().UnixNano()
		t.lastPingAt.Store(now)
		t.sendPongAsync()
	case experimentalMuxFramePong:
		t.pongFramesIn.Add(1)
		t.lastPongAt.Store(time.Now().UnixNano())
	case experimentalMuxFrameGoAway:
		t.goAwayFramesIn.Add(1)
		reason := frame.reason
		if reason == "" {
			reason = experimentalMuxReasonPeerDraining
		}
		t.markRemoteDraining(reason)
	default:
		return fmt.Errorf("rpc experimental mux unsupported frame type %d", frame.typ)
	}
	return nil
}

func (t *ExperimentalMuxTransport) registerStream(streamID uint64, enforceLimit bool) (*ExperimentalMuxStream, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if stream := t.streams[streamID]; stream != nil {
		return stream, nil
	}
	if enforceLimit && t.draining {
		return nil, NewError(CodeUnavailable, experimentalMuxReasonDraining)
	}
	if enforceLimit && t.remoteDraining {
		return nil, NewError(CodeUnavailable, experimentalMuxReasonPeerDraining)
	}
	if enforceLimit && t.maxStreams > 0 && len(t.streams) >= t.maxStreams {
		return nil, NewError(CodeUnavailable, experimentalMuxReasonMaxConcurrentStreams)
	}
	stream := &ExperimentalMuxStream{
		id:     streamID,
		t:      t,
		recv:   make(chan experimentalMuxStreamEvent, t.receiveQueueSize),
		credit: make(chan struct{}, t.receiveQueueSize),
		done:   make(chan struct{}),
	}
	stream.addCredit(uint32(t.receiveQueueSize))
	t.streams[streamID] = stream
	return stream, nil
}

func (t *ExperimentalMuxTransport) getOrAcceptStream(streamID uint64, rejectWhenFull bool) *ExperimentalMuxStream {
	t.mu.Lock()
	if stream := t.streams[streamID]; stream != nil {
		t.mu.Unlock()
		return stream
	}
	if rejectWhenFull && t.draining {
		t.mu.Unlock()
		t.remoteRejects.Add(1)
		t.drainRejects.Add(1)
		t.rememberClose(streamID, CodeUnavailable, experimentalMuxReasonDraining)
		t.sendCancelAsync(streamID, CodeUnavailable, experimentalMuxReasonDraining)
		return nil
	}
	if rejectWhenFull && t.maxStreams > 0 && len(t.streams) >= t.maxStreams {
		t.mu.Unlock()
		t.remoteRejects.Add(1)
		t.rememberClose(streamID, CodeUnavailable, experimentalMuxReasonMaxConcurrentStreams)
		t.sendCancelAsync(streamID, CodeUnavailable, experimentalMuxReasonMaxConcurrentStreams)
		return nil
	}
	stream := &ExperimentalMuxStream{
		id:     streamID,
		t:      t,
		recv:   make(chan experimentalMuxStreamEvent, t.receiveQueueSize),
		credit: make(chan struct{}, t.receiveQueueSize),
		done:   make(chan struct{}),
	}
	stream.addCredit(uint32(t.receiveQueueSize))
	t.streams[streamID] = stream
	closed := t.closed
	t.mu.Unlock()
	if !closed {
		t.acceptedStreams.Add(1)
		select {
		case t.accept <- stream:
		case <-t.done:
			stream.fail(ErrExperimentalMuxTransportClosed)
		}
	}
	return stream
}

func (t *ExperimentalMuxTransport) lookupStream(streamID uint64) *ExperimentalMuxStream {
	t.mu.Lock()
	stream := t.streams[streamID]
	t.mu.Unlock()
	return stream
}

func (t *ExperimentalMuxTransport) removeStream(streamID uint64, closeStream bool) {
	t.mu.Lock()
	stream := t.streams[streamID]
	delete(t.streams, streamID)
	t.mu.Unlock()
	if closeStream && stream != nil {
		stream.closeDone()
	}
}

func (t *ExperimentalMuxTransport) removeStreamIfDone(streamID uint64) {
	t.mu.Lock()
	stream := t.streams[streamID]
	if stream == nil {
		t.mu.Unlock()
		return
	}
	done := stream.bothDirectionsDone()
	if done {
		delete(t.streams, streamID)
	}
	t.mu.Unlock()
	if done {
		stream.closeDone()
	}
}

func (t *ExperimentalMuxTransport) failAll(err error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	streams := make([]*ExperimentalMuxStream, 0, len(t.streams))
	for id, stream := range t.streams {
		delete(t.streams, id)
		streams = append(streams, stream)
	}
	t.mu.Unlock()
	for _, stream := range streams {
		stream.fail(err)
	}
	t.once.Do(func() {
		close(t.done)
		if t.conn != nil {
			_ = t.conn.Close()
		}
	})
}

func (t *ExperimentalMuxTransport) rememberClose(streamID uint64, code Code, reason string) {
	if streamID != 0 {
		t.lastStreamID.Store(streamID)
	}
	t.lastCloseCode.Store(code)
	t.lastCloseReason.Store(reason)
}

func (t *ExperimentalMuxTransport) markRemoteDraining(reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.remoteDraining = true
	t.remoteDrainReason = reason
	t.mu.Unlock()
	t.rememberClose(0, CodeUnavailable, reason)
}

func (t *ExperimentalMuxTransport) sendPing(ctx context.Context) error {
	if err := t.writeFrame(ctx, experimentalMuxFrame{typ: experimentalMuxFramePing}); err != nil {
		return err
	}
	t.pingFramesOut.Add(1)
	t.lastPingAt.Store(time.Now().UnixNano())
	return nil
}

func (t *ExperimentalMuxTransport) sendPong(ctx context.Context) error {
	if err := t.writeFrame(ctx, experimentalMuxFrame{typ: experimentalMuxFramePong}); err != nil {
		return err
	}
	t.pongFramesOut.Add(1)
	t.lastPongAt.Store(time.Now().UnixNano())
	return nil
}

func (t *ExperimentalMuxTransport) sendPongAsync() {
	if t == nil {
		return
	}
	go func() {
		_ = t.sendPong(context.Background())
	}()
}

func (t *ExperimentalMuxTransport) sendCancelAsync(streamID uint64, code Code, reason string) {
	if t == nil || streamID == 0 {
		return
	}
	go func() {
		if err := t.writeFrame(context.Background(), experimentalMuxFrame{
			typ:      experimentalMuxFrameCancel,
			streamID: streamID,
			code:     code,
			reason:   reason,
		}); err != nil {
			return
		}
		t.cancelFramesOut.Add(1)
		t.canceledStreams.Add(1)
	}()
}

func (t *ExperimentalMuxTransport) keepaliveLoop() {
	interval := t.keepaliveInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if t.keepaliveIdle > 0 && time.Since(unixNanoToTime(t.lastFrameReadAt.Load())) > t.keepaliveIdle {
				t.idleTimeouts.Add(1)
				t.rememberClose(0, CodeDeadlineExceeded, "keepalive_idle")
				t.failAll(NewError(CodeDeadlineExceeded, "rpc experimental mux keepalive idle timeout"))
				return
			}
			if err := t.sendPing(context.Background()); err != nil {
				t.failAll(err)
				return
			}
		case <-t.done:
			return
		}
	}
}

func (t *ExperimentalMuxTransport) sendWindowUpdate(ctx context.Context, streamID uint64, delta uint32) error {
	if delta == 0 {
		return nil
	}
	if err := t.writeFrame(ctx, experimentalMuxFrame{typ: experimentalMuxFrameWindow, streamID: streamID, window: delta}); err != nil {
		return err
	}
	t.windowFramesOut.Add(1)
	return nil
}

func (t *ExperimentalMuxTransport) sendWindowUpdateAsync(streamID uint64, delta uint32) {
	if t == nil || delta == 0 {
		return
	}
	go func() {
		_ = t.sendWindowUpdate(context.Background(), streamID, delta)
	}()
}

func (t *ExperimentalMuxTransport) dispatchDataPayload(streamID uint64, payload []byte) error {
	stream := t.getOrAcceptStream(streamID, true)
	if stream == nil {
		return nil
	}
	t.dataFramesIn.Add(1)
	msg, err := t.frame.Unmarshal(payload)
	if err != nil {
		return err
	}
	if msg.Codec == t.payload.Name() {
		decoded, err := t.payload.Decode(msg.Payload)
		if err != nil {
			return err
		}
		msg.Payload = decoded
	}
	stream.deliver(experimentalMuxStreamEvent{msg: msg})
	return nil
}

func (t *ExperimentalMuxTransport) sendConnectionWindowUpdate(ctx context.Context, delta uint32) error {
	if delta == 0 {
		return nil
	}
	if err := t.writeFrame(ctx, experimentalMuxFrame{typ: experimentalMuxFrameWindowConn, window: delta}); err != nil {
		return err
	}
	t.connectionWindowFramesOut.Add(1)
	return nil
}

func (t *ExperimentalMuxTransport) sendConnectionWindowUpdateAsync(delta uint32) {
	if t == nil || delta == 0 {
		return
	}
	go func() {
		_ = t.sendConnectionWindowUpdate(context.Background(), delta)
	}()
}

func (s *ExperimentalMuxStream) deliver(event experimentalMuxStreamEvent) {
	if s == nil {
		return
	}
	if cap(s.recv) > 0 && len(s.recv) == cap(s.recv) {
		s.t.backpressureEvents.Add(1)
	}
	select {
	case s.recv <- event:
	case <-s.done:
	case <-s.t.done:
	}
}

func (s *ExperimentalMuxStream) deliverTerminal(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.remoteDone {
		s.mu.Unlock()
		return
	}
	s.remoteDone = true
	s.mu.Unlock()
	s.deliver(experimentalMuxStreamEvent{err: err})
}

func (s *ExperimentalMuxStream) markRemoteDone() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.remoteDone = true
	s.mu.Unlock()
}

func (s *ExperimentalMuxStream) markAborted() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.aborted = true
	s.mu.Unlock()
}

func (s *ExperimentalMuxStream) bothDirectionsDone() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	done := s.localDone && s.remoteDone
	s.mu.Unlock()
	return done
}

func (t *ExperimentalMuxTransport) acquireConnectionCredit(ctx context.Context) error {
	if t == nil {
		return ErrExperimentalMuxTransportClosed
	}
	if len(t.connCredit) == 0 {
		t.connectionCreditWaits.Add(1)
	}
	select {
	case <-t.connCredit:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return ErrExperimentalMuxTransportClosed
	}
}

func (t *ExperimentalMuxTransport) releaseConnectionCredit() {
	if t == nil {
		return
	}
	select {
	case t.connCredit <- struct{}{}:
	default:
	}
}

func (t *ExperimentalMuxTransport) addConnectionCredit(delta uint32) {
	if t == nil || delta == 0 {
		return
	}
	for range delta {
		t.releaseConnectionCredit()
	}
}

func (s *ExperimentalMuxStream) appendFragment(payload []byte) error {
	if s == nil || s.t == nil {
		return ErrExperimentalMuxStreamClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if int64(len(s.fragments)+len(payload)) > s.t.maxMessage {
		return ErrFrameTooLarge
	}
	s.fragments = append(s.fragments, payload...)
	return nil
}

func (s *ExperimentalMuxStream) finishFragments(payload []byte) ([]byte, error) {
	if s == nil || s.t == nil {
		return nil, ErrExperimentalMuxStreamClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if int64(len(s.fragments)+len(payload)) > s.t.maxMessage {
		s.fragments = nil
		return nil, ErrFrameTooLarge
	}
	out := make([]byte, 0, len(s.fragments)+len(payload))
	out = append(out, s.fragments...)
	out = append(out, payload...)
	s.fragments = nil
	return out, nil
}

func (s *ExperimentalMuxStream) acquireCredit(ctx context.Context) error {
	if s == nil {
		return ErrExperimentalMuxStreamClosed
	}
	if len(s.credit) == 0 {
		s.t.creditWaits.Add(1)
	}
	select {
	case <-s.credit:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrExperimentalMuxStreamClosed
	case <-s.t.done:
		return ErrExperimentalMuxTransportClosed
	}
}

func (s *ExperimentalMuxStream) releaseCredit() {
	if s == nil {
		return
	}
	select {
	case s.credit <- struct{}{}:
	default:
	}
}

func (s *ExperimentalMuxStream) addCredit(delta uint32) {
	if s == nil || delta == 0 {
		return
	}
	for range delta {
		s.releaseCredit()
	}
}

func (s *ExperimentalMuxStream) fail(err error) {
	if s == nil {
		return
	}
	s.deliverTerminal(err)
	s.closeDone()
}

func (s *ExperimentalMuxStream) closeDone() {
	s.once.Do(func() {
		close(s.done)
	})
}

func (t *ExperimentalMuxTransport) writeFrame(ctx context.Context, frame experimentalMuxFrame) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := encodeExperimentalMuxFrame(frame)
	if err != nil {
		return err
	}
	if int64(len(data)) > t.maxFrame || len(data) > math.MaxUint32 {
		return ErrFrameTooLarge
	}
	var header [4]byte
	// #nosec G115 -- len(data) is checked against math.MaxUint32 immediately above.
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = t.conn.SetWriteDeadline(deadline)
	} else {
		_ = t.conn.SetWriteDeadline(time.Time{})
	}
	if _, err := t.conn.Write(header[:]); err != nil {
		return fmt.Errorf("write rpc experimental mux frame header: %w", err)
	}
	if _, err := t.conn.Write(data); err != nil {
		return fmt.Errorf("write rpc experimental mux frame body: %w", err)
	}
	t.lastFrameWrittenAt.Store(time.Now().UnixNano())
	t.framesOut.Add(1)
	t.bytesOut.Add(int64(4 + len(data)))
	return nil
}

func (t *ExperimentalMuxTransport) writeDataFrames(ctx context.Context, streamID uint64, payload []byte) error {
	if int64(len(payload)) > t.maxMessage {
		return ErrFrameTooLarge
	}
	if !t.shouldFragment(payload) {
		if err := t.writeFrame(ctx, experimentalMuxFrame{typ: experimentalMuxFrameData, streamID: streamID, payload: payload}); err != nil {
			return err
		}
		t.dataFramesOut.Add(1)
		return nil
	}
	chunkSize, err := t.fragmentPayloadSize(streamID)
	if err != nil {
		return err
	}
	for offset := 0; offset < len(payload); {
		end := offset + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		typ := experimentalMuxFrameDataFrag
		if end == len(payload) {
			typ = experimentalMuxFrameDataEnd
		}
		if err := t.writeFrame(ctx, experimentalMuxFrame{typ: typ, streamID: streamID, payload: payload[offset:end]}); err != nil {
			return err
		}
		t.fragmentFramesOut.Add(1)
		offset = end
	}
	t.dataFramesOut.Add(1)
	return nil
}

func (t *ExperimentalMuxTransport) shouldFragment(payload []byte) bool {
	encoded, err := encodeExperimentalMuxFrame(experimentalMuxFrame{typ: experimentalMuxFrameData, streamID: 1, payload: payload})
	return err == nil && int64(len(encoded)) > t.maxFrame
}

func (t *ExperimentalMuxTransport) fragmentPayloadSize(streamID uint64) (int, error) {
	if t.maxFrame > int64(math.MaxInt) {
		return math.MaxInt, nil
	}
	encodedEmpty, err := encodeExperimentalMuxFrame(experimentalMuxFrame{typ: experimentalMuxFrameDataFrag, streamID: streamID})
	if err != nil {
		return 0, err
	}
	headroom := int(t.maxFrame) - len(encodedEmpty) - 1
	if headroom <= 0 {
		return 0, ErrFrameTooLarge
	}
	return headroom, nil
}

func (t *ExperimentalMuxTransport) readFrame() (experimentalMuxFrame, int64, error) {
	var header [4]byte
	if _, err := io.ReadFull(t.conn, header[:]); err != nil {
		return experimentalMuxFrame{}, 0, fmt.Errorf("read rpc experimental mux frame header: %w", err)
	}
	length := int64(binary.BigEndian.Uint32(header[:]))
	if length <= 0 || length > t.maxFrame {
		return experimentalMuxFrame{}, 4, ErrFrameTooLarge
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(t.conn, data); err != nil {
		return experimentalMuxFrame{}, 4, fmt.Errorf("read rpc experimental mux frame body: %w", err)
	}
	t.lastFrameReadAt.Store(time.Now().UnixNano())
	frame, err := decodeExperimentalMuxFrame(data)
	if err != nil {
		return experimentalMuxFrame{}, 4 + length, err
	}
	return frame, 4 + length, nil
}

func encodeExperimentalMuxFrame(frame experimentalMuxFrame) ([]byte, error) {
	if frame.streamID == 0 && !isExperimentalMuxControlFrame(frame.typ) {
		return nil, errors.New("rpc experimental mux stream id is required")
	}
	if frame.streamID != 0 && isExperimentalMuxControlFrame(frame.typ) {
		return nil, errors.New("rpc experimental mux control frame must not have stream id")
	}
	var b bytes.Buffer
	b.Grow(16 + len(frame.reason) + len(frame.payload))
	b.WriteByte(experimentalMuxFrameVersion)
	b.WriteByte(frame.typ)
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], frame.streamID)
	b.Write(id[:])
	writeFrameString(&b, string(frame.code))
	writeFrameString(&b, frame.reason)
	var window [4]byte
	binary.BigEndian.PutUint32(window[:], frame.window)
	b.Write(window[:])
	writeFrameBytes(&b, frame.payload)
	return b.Bytes(), nil
}

func decodeExperimentalMuxFrame(data []byte) (experimentalMuxFrame, error) {
	if len(data) < 10 {
		return experimentalMuxFrame{}, io.ErrUnexpectedEOF
	}
	r := bytes.NewReader(data)
	version, err := r.ReadByte()
	if err != nil {
		return experimentalMuxFrame{}, err
	}
	if version != experimentalMuxFrameVersion {
		return experimentalMuxFrame{}, fmt.Errorf("unsupported rpc experimental mux frame version %d", version)
	}
	typ, err := r.ReadByte()
	if err != nil {
		return experimentalMuxFrame{}, err
	}
	var id [8]byte
	if _, err := io.ReadFull(r, id[:]); err != nil {
		return experimentalMuxFrame{}, err
	}
	code, err := readFrameString(r)
	if err != nil {
		return experimentalMuxFrame{}, err
	}
	reason, err := readFrameString(r)
	if err != nil {
		return experimentalMuxFrame{}, err
	}
	var window [4]byte
	if _, err := io.ReadFull(r, window[:]); err != nil {
		return experimentalMuxFrame{}, err
	}
	payload, err := readFrameBytes(r)
	if err != nil {
		return experimentalMuxFrame{}, err
	}
	if r.Len() != 0 {
		return experimentalMuxFrame{}, fmt.Errorf("rpc experimental mux frame has %d trailing bytes", r.Len())
	}
	streamID := binary.BigEndian.Uint64(id[:])
	if streamID == 0 && !isExperimentalMuxControlFrame(typ) {
		return experimentalMuxFrame{}, errors.New("rpc experimental mux stream id is required")
	}
	if streamID != 0 && isExperimentalMuxControlFrame(typ) {
		return experimentalMuxFrame{}, errors.New("rpc experimental mux control frame must not have stream id")
	}
	return experimentalMuxFrame{typ: typ, streamID: streamID, code: Code(code), reason: reason, window: binary.BigEndian.Uint32(window[:]), payload: payload}, nil
}

func isExperimentalMuxControlFrame(typ byte) bool {
	return typ == experimentalMuxFramePing ||
		typ == experimentalMuxFramePong ||
		typ == experimentalMuxFrameGoAway ||
		typ == experimentalMuxFrameWindowConn
}

func unixNanoToTime(nano int64) time.Time {
	if nano <= 0 {
		return time.Time{}
	}
	return time.Unix(0, nano)
}

func isExperimentalMuxDrainReason(reason string) bool {
	return reason == experimentalMuxReasonDraining || reason == experimentalMuxReasonPeerDraining
}

func reasonFromError(err error) string {
	if err == nil {
		return ""
	}
	var rpcErr *Error
	if errors.As(err, &rpcErr) {
		return rpcErr.Text
	}
	return err.Error()
}
