package voice

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/disgoorg/godave"
	"github.com/gorilla/websocket"
	"github.com/thomas-vilte/dave-go/session"
	"golang.org/x/crypto/chacha20poly1305"
)

const FrameDuration = 20 * time.Millisecond

const (
	frameSamples     = 960
	handshakeTimeout = 10 * time.Second
	writeTimeout     = 10 * time.Second
	discoveryTimeout = 2 * time.Second
	discoveryRetries = 5
	maxReconnects    = 5
	watchInterval    = 2 * time.Second
	lostTimeout      = 5 * time.Second
	maxRejoins       = 3
	holdTimeout      = 30 * time.Second
	leaveTimeout     = 2 * time.Second
)

const (
	opIdentify                 = 0
	opSelectProtocol           = 1
	opReady                    = 2
	opHeartbeat                = 3
	opSessionDescription       = 4
	opSpeaking                 = 5
	opResume                   = 7
	opHello                    = 8
	opResumed                  = 9
	opClientsConnect           = 11
	opClientDisconnect         = 13
	opDavePrepareTransition    = 21
	opDaveExecuteTransition    = 22
	opDaveTransitionReady      = 23
	opDavePrepareEpoch         = 24
	opDaveExternalSender       = 25
	opDaveKeyPackage           = 26
	opDaveProposals            = 27
	opDaveCommitWelcome        = 28
	opDaveAnnounceCommit       = 29
	opDaveWelcome              = 30
	opDaveInvalidCommitWelcome = 31
)

var (
	ErrDisconnected = errors.New("disconnected from the voice channel")
	ErrClosed       = errors.New("voice connection closed")
	errLost         = errors.New("lost the voice connection")
	errGatewayDown  = errors.New("discord gateway is not connected")
	errNoSocket     = errors.New("voice gateway is not connected")
)

var (
	encryptionModes = []string{"aead_aes256_gcm_rtpsize", "aead_xchacha20_poly1305_rtpsize"}
	silenceFrame    = []byte{0xF8, 0xFF, 0xFE}
)

type Conn struct {
	s       *discordgo.Session
	guildID string
	userID  string
	done    chan struct{}

	mu             sync.Mutex
	removeHandlers []func()
	joined         bool
	channelID      string
	sessionID      string
	stateFresh     bool
	server         *discordgo.VoiceServerUpdate
	gw             *gateway
	tr             *transport
	changed        chan struct{}
	lostAt         time.Time
	err            error

	sendMu    sync.Mutex
	holdSince time.Time
}

type gateway struct {
	c         *Conn
	ctx       context.Context
	cancel    context.CancelFunc
	endpoint  string
	token     string
	sessionID string
	channelID string
	dave      *session.Session
	seq       atomic.Int64

	wmu sync.Mutex
	ws  *websocket.Conn

	usersMu sync.Mutex
	users   map[string]struct{}

	udp         *net.UDPConn
	ssrc        uint32
	established bool
	readTimeout time.Duration
}

type transport struct {
	g         *gateway
	channelID string
	udp       *net.UDPConn
	ssrc      uint32
	aead      cipher.AEAD
	nonce     []byte
	header    [12]byte
	sequence  uint16
	timestamp uint32
	counter   uint32
	speaking  bool
	failed    bool
	next      time.Time
	frame     []byte
	packet    []byte
}

func Join(ctx context.Context, s *discordgo.Session, guildID, channelID string) (*Conn, error) {
	c := &Conn{
		s:         s,
		guildID:   guildID,
		userID:    s.State.User.ID,
		channelID: channelID,
		changed:   make(chan struct{}),
		done:      make(chan struct{}),
	}
	c.mu.Lock()
	c.removeHandlers = []func(){s.AddHandler(c.onVoiceStateUpdate), s.AddHandler(c.onVoiceServerUpdate)}
	c.mu.Unlock()

	if err := c.requestChannel(channelID); err != nil {
		c.shutdown(err)
		return nil, err
	}
	if _, err := c.awaitTransport(ctx, nil); err != nil {
		c.Close()
		return nil, err
	}
	go c.watch()
	return c, nil
}

func (c *Conn) ChannelID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channelID
}

func (c *Conn) Done() <-chan struct{} {
	return c.done
}

func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) Move(ctx context.Context, channelID string) error {
	if err := c.requestChannel(channelID); err != nil {
		return err
	}
	for {
		c.mu.Lock()
		moved := c.tr != nil && c.tr.channelID == channelID
		changed := c.changed
		c.mu.Unlock()
		if moved {
			return nil
		}
		select {
		case <-changed:
		case <-c.done:
			return c.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Conn) Close() {
	if c.requestChannel("") == nil {
		select {
		case <-c.done:
		case <-time.After(leaveTimeout):
		}
	}
	c.shutdown(ErrClosed)
}

func (c *Conn) WriteOpus(ctx context.Context, frame []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	var stale *transport
	for {
		t, err := c.awaitTransport(ctx, stale)
		if err != nil {
			return err
		}
		// Discord only finishes the DAVE handshake once someone else is in the call, and until
		// then there's nobody to encrypt for.
		if t.g.dave.ShouldHoldFrames() && !t.g.alone() {
			if err := c.hold(ctx); err != nil {
				return err
			}
			continue
		}
		c.holdSince = time.Time{}

		err = t.write(frame)
		if errors.Is(err, net.ErrClosed) {
			stale = t
			continue
		}
		if err != nil && !t.failed {
			t.failed = true
			log.Println("Error sending voice packet", err)
		}
		return nil
	}
}

func (c *Conn) Silence(ctx context.Context) {
	for range 5 {
		if c.WriteOpus(ctx, silenceFrame) != nil {
			return
		}
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	t := c.tr
	c.mu.Unlock()
	if t != nil {
		t.setSpeaking(false)
	}
}

func (c *Conn) hold(ctx context.Context) error {
	if c.holdSince.IsZero() {
		c.holdSince = time.Now()
	} else if time.Since(c.holdSince) > holdTimeout {
		c.holdSince = time.Time{}
		log.Printf("DAVE handshake in guild %s is stuck, rejoining", c.guildID)
		if err := c.requestChannel(c.ChannelID()); err != nil {
			log.Println("Error rejoining voice channel", err)
		}
	}
	timer := time.NewTimer(FrameDuration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Conn) awaitTransport(ctx context.Context, stale *transport) (*transport, error) {
	for {
		c.mu.Lock()
		t, changed := c.tr, c.changed
		c.mu.Unlock()
		if t != nil && t != stale {
			return t, nil
		}
		select {
		case <-changed:
		case <-c.done:
			return nil, c.Err()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Holding the session's read lock keeps discordgo from swapping out its websocket mid-write,
// which would otherwise panic inside ChannelVoiceJoinManual.
func (c *Conn) requestChannel(channelID string) error {
	c.s.RLock()
	defer c.s.RUnlock()
	if !c.s.DataReady {
		return errGatewayDown
	}
	return c.s.ChannelVoiceJoinManual(c.guildID, channelID, false, true)
}

func (c *Conn) onVoiceStateUpdate(_ *discordgo.Session, e *discordgo.VoiceStateUpdate) {
	if e.VoiceState == nil || e.GuildID != c.guildID || e.UserID != c.userID {
		return
	}
	c.mu.Lock()
	if e.ChannelID == "" {
		joined := c.joined
		c.mu.Unlock()
		if joined {
			c.shutdown(ErrDisconnected)
		}
		return
	}
	c.joined = true
	c.channelID, c.sessionID, c.stateFresh = e.ChannelID, e.SessionID, true
	c.startGatewayLocked()
	c.mu.Unlock()
}

func (c *Conn) onVoiceServerUpdate(_ *discordgo.Session, e *discordgo.VoiceServerUpdate) {
	if e.GuildID != c.guildID || e.Endpoint == "" {
		return
	}
	c.mu.Lock()
	c.server = e
	c.startGatewayLocked()
	c.mu.Unlock()
}

// Discord sends a state and a server update for every join or move, and the new session
// can't be opened until both have arrived.
func (c *Conn) startGatewayLocked() {
	if !c.stateFresh || c.server == nil || c.closedLocked() {
		return
	}
	c.stopGatewayLocked()
	ctx, cancel := context.WithCancel(context.Background())
	g := &gateway{
		c:         c,
		ctx:       ctx,
		cancel:    cancel,
		endpoint:  c.server.Endpoint,
		token:     c.server.Token,
		sessionID: c.sessionID,
		channelID: c.channelID,
		users:     map[string]struct{}{},
	}
	g.seq.Store(-1)
	g.dave = session.New(godave.UserID(c.userID), g)
	if id, err := strconv.ParseUint(c.channelID, 10, 64); err == nil {
		g.dave.SetChannelID(godave.ChannelID(id))
	}
	c.stateFresh, c.server, c.gw = false, nil, g
	go g.run()
}

func (c *Conn) stopGatewayLocked() {
	if c.gw != nil {
		c.gw.cancel()
		c.gw = nil
	}
	c.setTransportLocked(nil)
}

func (c *Conn) setTransportLocked(t *transport) {
	if c.tr == t {
		return
	}
	if t == nil {
		c.lostAt = time.Now()
	}
	c.tr = t
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *Conn) gatewayReady(g *gateway, t *transport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gw == g {
		c.setTransportLocked(t)
	}
}

func (c *Conn) gatewayEnded(g *gateway) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gw == g {
		c.gw = nil
		c.setTransportLocked(nil)
	}
}

func (c *Conn) closedLocked() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *Conn) shutdown(err error) {
	c.mu.Lock()
	if c.closedLocked() {
		c.mu.Unlock()
		return
	}
	c.err = err
	close(c.done)
	c.stopGatewayLocked()
	removeHandlers := c.removeHandlers
	c.mu.Unlock()
	for _, remove := range removeHandlers {
		remove()
	}
}

func (c *Conn) watch() {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	rejoins := 0
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
		}
		c.mu.Lock()
		if c.tr != nil {
			rejoins = 0
		}
		lost := c.tr == nil && time.Since(c.lostAt) > lostTimeout
		if lost {
			c.lostAt = time.Now()
		}
		channelID := c.channelID
		c.mu.Unlock()
		if !lost {
			continue
		}
		if rejoins++; rejoins > maxRejoins {
			c.shutdown(errLost)
			return
		}
		log.Printf("Rejoining voice channel %s in guild %s", channelID, c.guildID)
		if err := c.requestChannel(channelID); err != nil {
			log.Println("Error rejoining voice channel", err)
		}
	}
}

func (g *gateway) run() {
	defer g.close()
	resume := false
	for failures := 1; failures <= maxReconnects; failures++ {
		ready, err := g.connect(resume)
		if g.ctx.Err() != nil {
			return
		}
		log.Printf("Voice gateway for guild %s closed: %v", g.c.guildID, err)
		if !reconnectable(err) {
			break
		}
		if ready {
			failures = 0
		}
		resume = g.established
		select {
		case <-g.ctx.Done():
			return
		case <-time.After(time.Duration(failures) * time.Second):
		}
	}
	g.c.gatewayEnded(g)
}

func (g *gateway) close() {
	g.cancel()
	if g.udp != nil {
		g.udp.Close()
	}
	g.dave.Close()
}

func reconnectable(err error) bool {
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		return true
	}
	switch closeErr.Code {
	case 4004, 4006, 4009, 4011, 4012, 4014, 4016, 4017, 4021, 4022:
		return false
	}
	return true
}

func (g *gateway) connect(resume bool) (bool, error) {
	dialCtx, cancel := context.WithTimeout(g.ctx, handshakeTimeout)
	ws, _, err := websocket.DefaultDialer.DialContext(dialCtx, "wss://"+g.endpoint+"/?v=8", nil)
	cancel()
	if err != nil {
		return false, err
	}
	defer ws.Close()
	stop := context.AfterFunc(g.ctx, func() { ws.Close() })
	defer stop()
	g.setSocket(ws)
	defer g.setSocket(nil)
	beat, stopBeat := context.WithCancel(g.ctx)
	defer stopBeat()

	if resume {
		err = g.send(opResume, map[string]any{
			"server_id":  g.c.guildID,
			"session_id": g.sessionID,
			"token":      g.token,
			"seq_ack":    g.seq.Load(),
		})
	} else {
		err = g.send(opIdentify, map[string]any{
			"server_id":                 g.c.guildID,
			"user_id":                   g.c.userID,
			"session_id":                g.sessionID,
			"token":                     g.token,
			"max_dave_protocol_version": g.dave.MaxSupportedProtocolVersion(),
		})
	}
	if err != nil {
		return false, err
	}

	g.readTimeout = handshakeTimeout
	ready := false
	for {
		ws.SetReadDeadline(time.Now().Add(g.readTimeout))
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return ready, err
		}
		if kind == websocket.BinaryMessage {
			g.onBinary(data)
			continue
		}
		var msg struct {
			Op  int             `json:"op"`
			D   json.RawMessage `json:"d"`
			Seq *int64          `json:"seq"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Println("Error decoding voice gateway message", err)
			continue
		}
		if msg.Seq != nil {
			g.seq.Store(*msg.Seq)
		}
		if err := g.onMessage(beat, msg.Op, msg.D); err != nil {
			return ready, err
		}
		if msg.Op == opSessionDescription || msg.Op == opResumed {
			ready = true
		}
	}
}

func (g *gateway) onMessage(beat context.Context, op int, d json.RawMessage) error {
	switch op {
	case opHello:
		var hello struct {
			HeartbeatInterval float64 `json:"heartbeat_interval"`
		}
		if err := json.Unmarshal(d, &hello); err != nil {
			return err
		}
		interval := time.Duration(hello.HeartbeatInterval * float64(time.Millisecond))
		g.readTimeout = 2*interval + handshakeTimeout
		go g.heartbeat(beat, interval)

	case opReady:
		var ready struct {
			SSRC  uint32   `json:"ssrc"`
			IP    string   `json:"ip"`
			Port  int      `json:"port"`
			Modes []string `json:"modes"`
		}
		if err := json.Unmarshal(d, &ready); err != nil {
			return err
		}
		return g.selectProtocol(ready.SSRC, ready.IP, ready.Port, ready.Modes)

	case opSessionDescription:
		var desc struct {
			Mode                string   `json:"mode"`
			SecretKey           [32]byte `json:"secret_key"`
			DaveProtocolVersion uint16   `json:"dave_protocol_version"`
		}
		if err := json.Unmarshal(d, &desc); err != nil {
			return err
		}
		aead, err := newAEAD(desc.Mode, desc.SecretKey[:])
		if err != nil {
			return err
		}
		g.dave.OnSelectProtocolAck(desc.DaveProtocolVersion)
		g.dave.AssignSsrcToCodec(g.ssrc, godave.CodecOpus)
		g.established = true
		g.c.gatewayReady(g, newTransport(g, aead))

	case opClientsConnect:
		var clients struct {
			UserIDs []string `json:"user_ids"`
		}
		if json.Unmarshal(d, &clients) == nil {
			g.usersMu.Lock()
			for _, id := range clients.UserIDs {
				g.users[id] = struct{}{}
			}
			g.usersMu.Unlock()
			for _, id := range clients.UserIDs {
				g.dave.AddUser(godave.UserID(id))
			}
		}

	case opClientDisconnect:
		var client struct {
			UserID string `json:"user_id"`
		}
		if json.Unmarshal(d, &client) == nil {
			g.usersMu.Lock()
			delete(g.users, client.UserID)
			g.usersMu.Unlock()
			g.dave.RemoveUser(godave.UserID(client.UserID))
		}

	case opDavePrepareTransition:
		var transition struct {
			TransitionID    uint16 `json:"transition_id"`
			ProtocolVersion uint16 `json:"protocol_version"`
		}
		if json.Unmarshal(d, &transition) == nil {
			g.dave.OnDavePrepareTransition(transition.TransitionID, transition.ProtocolVersion)
		}

	case opDaveExecuteTransition:
		var transition struct {
			TransitionID uint16 `json:"transition_id"`
		}
		if json.Unmarshal(d, &transition) == nil {
			g.dave.OnDaveExecuteTransition(transition.TransitionID)
		}

	case opDavePrepareEpoch:
		var epoch struct {
			Epoch           int    `json:"epoch"`
			ProtocolVersion uint16 `json:"protocol_version"`
		}
		if json.Unmarshal(d, &epoch) == nil {
			g.dave.OnDavePrepareEpoch(epoch.Epoch, epoch.ProtocolVersion)
		}
	}
	return nil
}

func (g *gateway) onBinary(data []byte) {
	if len(data) < 3 {
		return
	}
	g.seq.Store(int64(binary.BigEndian.Uint16(data)))
	op, payload := data[2], data[3:]
	switch op {
	case opDaveExternalSender:
		g.dave.OnDaveMLSExternalSenderPackage(payload)
	case opDaveProposals:
		g.dave.OnDaveMLSProposals(payload)
	case opDaveAnnounceCommit, opDaveWelcome:
		if len(payload) < 2 {
			return
		}
		transitionID := binary.BigEndian.Uint16(payload)
		if op == opDaveAnnounceCommit {
			g.dave.OnDaveMLSPrepareCommitTransition(transitionID, payload[2:])
		} else {
			g.dave.OnDaveMLSWelcome(transitionID, payload[2:])
		}
	}
}

func (g *gateway) selectProtocol(ssrc uint32, ip string, port int, modes []string) error {
	i := slices.IndexFunc(encryptionModes, func(mode string) bool { return slices.Contains(modes, mode) })
	if i < 0 {
		return fmt.Errorf("no supported encryption mode in %v", modes)
	}
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return err
	}
	localIP, localPort, err := discoverAddress(conn, ssrc)
	if err != nil {
		conn.Close()
		return err
	}
	if g.udp != nil {
		g.udp.Close()
	}
	g.udp, g.ssrc = conn, ssrc
	return g.send(opSelectProtocol, map[string]any{
		"protocol": "udp",
		"data":     map[string]any{"address": localIP, "port": localPort, "mode": encryptionModes[i]},
	})
}

func discoverAddress(conn *net.UDPConn, ssrc uint32) (string, int, error) {
	request := make([]byte, 74)
	binary.BigEndian.PutUint16(request, 1)
	binary.BigEndian.PutUint16(request[2:], 70)
	binary.BigEndian.PutUint32(request[4:], ssrc)
	response := make([]byte, 74)
	defer conn.SetReadDeadline(time.Time{})

	err := errors.New("no response")
	for range discoveryRetries {
		if _, err = conn.Write(request); err != nil {
			return "", 0, err
		}
		conn.SetReadDeadline(time.Now().Add(discoveryTimeout))
		var n int
		if n, err = conn.Read(response); err != nil {
			continue
		}
		if n < len(response) || binary.BigEndian.Uint16(response) != 2 {
			err = errors.New("malformed response")
			continue
		}
		address, _, _ := bytes.Cut(response[8:72], []byte{0})
		return string(address), int(binary.BigEndian.Uint16(response[72:])), nil
	}
	return "", 0, fmt.Errorf("voice IP discovery failed: %w", err)
}

func (g *gateway) alone() bool {
	g.usersMu.Lock()
	defer g.usersMu.Unlock()
	return len(g.users) == 0
}

func (g *gateway) heartbeat(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := g.send(opHeartbeat, map[string]int64{"t": time.Now().UnixMilli(), "seq_ack": g.seq.Load()}); err != nil {
			return
		}
	}
}

func (g *gateway) send(op int, d any) error {
	data, err := json.Marshal(map[string]any{"op": op, "d": d})
	if err != nil {
		return err
	}
	return g.write(websocket.TextMessage, data)
}

func (g *gateway) sendBinary(op byte, payload []byte) error {
	return g.write(websocket.BinaryMessage, append([]byte{op}, payload...))
}

func (g *gateway) write(kind int, data []byte) error {
	g.wmu.Lock()
	defer g.wmu.Unlock()
	if g.ws == nil {
		return errNoSocket
	}
	g.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return g.ws.WriteMessage(kind, data)
}

func (g *gateway) setSocket(ws *websocket.Conn) {
	g.wmu.Lock()
	g.ws = ws
	g.wmu.Unlock()
}

func (g *gateway) SendMLSKeyPackage(keyPackage []byte) error {
	return g.sendBinary(opDaveKeyPackage, keyPackage)
}

func (g *gateway) SendMLSCommitWelcome(commitWelcome []byte) error {
	return g.sendBinary(opDaveCommitWelcome, commitWelcome)
}

func (g *gateway) SendReadyForTransition(transitionID uint16) error {
	return g.send(opDaveTransitionReady, map[string]uint16{"transition_id": transitionID})
}

func (g *gateway) SendInvalidCommitWelcome(transitionID uint16) error {
	return g.send(opDaveInvalidCommitWelcome, map[string]uint16{"transition_id": transitionID})
}

func newAEAD(mode string, key []byte) (cipher.AEAD, error) {
	switch mode {
	case "aead_aes256_gcm_rtpsize":
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	case "aead_xchacha20_poly1305_rtpsize":
		return chacha20poly1305.NewX(key)
	}
	return nil, fmt.Errorf("unsupported encryption mode %q", mode)
}

func newTransport(g *gateway, aead cipher.AEAD) *transport {
	t := &transport{
		g:         g,
		channelID: g.channelID,
		udp:       g.udp,
		ssrc:      g.ssrc,
		aead:      aead,
		nonce:     make([]byte, aead.NonceSize()),
	}
	t.header[0], t.header[1] = 0x80, 0x78
	binary.BigEndian.PutUint32(t.header[8:], t.ssrc)
	return t
}

func (t *transport) setSpeaking(on bool) {
	if t.speaking == on {
		return
	}
	flags := 0
	if on {
		flags = 1
	}
	if t.g.send(opSpeaking, map[string]any{"speaking": flags, "delay": 0, "ssrc": t.ssrc}) == nil {
		t.speaking = on
	}
}

func (t *transport) write(frame []byte) error {
	t.setSpeaking(true)
	size := t.g.dave.MaxEncryptedFrameSize(len(frame))
	if cap(t.frame) < size {
		t.frame = make([]byte, size)
	}
	n, err := t.g.dave.Encrypt(t.ssrc, frame, t.frame[:size])
	if err != nil {
		return fmt.Errorf("DAVE encryption failed: %w", err)
	}

	t.pace()
	binary.BigEndian.PutUint16(t.header[2:], t.sequence)
	binary.BigEndian.PutUint32(t.header[4:], t.timestamp)
	binary.BigEndian.PutUint32(t.nonce, t.counter)
	t.packet = t.aead.Seal(append(t.packet[:0], t.header[:]...), t.nonce, t.frame[:n], t.header[:])
	t.packet = append(t.packet, t.nonce[:4]...)
	t.sequence++
	t.timestamp += frameSamples
	t.counter++
	_, err = t.udp.Write(t.packet)
	return err
}

// After a pause the RTP timestamp jumps ahead by the time that passed, so listeners treat
// the gap as silence instead of late packets.
func (t *transport) pace() {
	now := time.Now()
	if t.next.IsZero() {
		t.next = now
	} else if behind := now.Sub(t.next); behind > 3*FrameDuration {
		t.timestamp += uint32(behind/FrameDuration) * frameSamples
		t.next = now
	}
	time.Sleep(time.Until(t.next))
	t.next = t.next.Add(FrameDuration)
}
