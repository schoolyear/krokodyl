package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/schollz/peerdiscovery"
	"github.com/sirupsen/logrus"
)

// Nearby-device discovery: every GUI instance multicasts a tiny identity
// payload on a krokodyl-specific port and listens for others. Liveness is
// expiry-based (peers announce every announceInterval; silence beyond
// peerTTL drops them). Hearing our own announcements (AllowSelf) doubles as
// a health check — if the socket is blocked by a firewall or AP isolation,
// we cannot even hear ourselves and report discovery as unavailable instead
// of showing an empty list that looks broken.

const (
	// Dedicated port — deliberately not croc's 9009-9013 family nor
	// peerdiscovery's 9999 default, so the croc CLI and other tools using
	// the library never cross-talk with krokodyl discovery.
	discoveryPort = "42791"

	announceInterval = 2 * time.Second
	peerTTL          = 5 * time.Second
	sweepInterval    = 1 * time.Second
	// After a peer says goodbye, ignore any re-add for this long. UDP can
	// deliver a normal announcement that was already in flight just after
	// the goodbye; without this window the peer would briefly reappear and
	// then linger until its TTL expired (the visible "bounce-back").
	byeSuppression = 3 * time.Second
	// No self-echo for this long after start → assume blocked.
	healthTimeout = 5 * time.Second

	maxPayloadBytes = 1024
	maxPeerNameLen  = 64
	maxMachineIDLen = 64
	maxAddrLen      = 45 // longest possible IPv6 textual form
)

type NearbyPeer struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Addr        string   `json:"addr"`            // address the announcement arrived from
	Addrs       []string `json:"addrs,omitempty"` // all reachable addresses the peer advertised
	Port        int      `json:"port"`
	MachineID   string   `json:"machineId"` // stable per-install id; survives restarts/renames
	Fingerprint string   `json:"-"`         // control-channel cert pin; backend-only
}

type DiscoveryState struct {
	Available bool `json:"available"`
}

type discoveryIdentity struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Port        int      `json:"port,omitempty"`        // control-channel TCP port
	Fingerprint string   `json:"fingerprint,omitempty"` // control-channel cert SHA-256 (hex)
	MachineID   string   `json:"machineId,omitempty"`   // stable per-install id
	Addrs       []string `json:"addrs,omitempty"`       // reachable addresses to dial
	// Gen rises each time this instance becomes visible again. A goodbye
	// suppresses only same-or-older-generation announcements (in-flight
	// stragglers); a higher generation means an intentional unhide and is
	// honored immediately, so reappearing is instant.
	Gen int  `json:"gen,omitempty"`
	Bye bool `json:"bye,omitempty"` // clean-shutdown farewell
}

func encodeIdentity(id discoveryIdentity) []byte {
	data, err := json.Marshal(id)
	if err != nil {
		logrus.WithError(err).Error("could not encode discovery identity")
		return nil
	}
	return data
}

// decodeIdentity validates an announcement payload from the network.
// Payloads are untrusted input: size-capped, must parse, must carry an id.
func decodeIdentity(payload []byte) (discoveryIdentity, error) {
	var id discoveryIdentity
	if len(payload) == 0 || len(payload) > maxPayloadBytes {
		return id, fmt.Errorf("payload size %d out of bounds", len(payload))
	}
	if err := json.Unmarshal(payload, &id); err != nil {
		return id, fmt.Errorf("malformed discovery payload: %w", err)
	}
	if id.ID == "" {
		return id, fmt.Errorf("discovery payload missing id")
	}
	if id.Bye {
		// Farewell packets only need the id.
		return id, nil
	}
	if id.Port < 1 || id.Port > 65535 {
		return id, fmt.Errorf("discovery payload has invalid port %d", id.Port)
	}
	if len(id.Fingerprint) != 64 {
		return id, fmt.Errorf("discovery payload has invalid fingerprint")
	}
	if _, err := hex.DecodeString(id.Fingerprint); err != nil {
		return id, fmt.Errorf("discovery payload fingerprint is not hex")
	}
	// Machine id is optional (older peers omit it; they just aren't
	// repeat-matchable) but bounded when present.
	if len(id.MachineID) > maxMachineIDLen {
		return id, fmt.Errorf("discovery payload has oversized machine id")
	}
	if len(id.Addrs) > maxAdvertisedAddrs {
		return id, fmt.Errorf("discovery payload advertises too many addresses")
	}
	for _, a := range id.Addrs {
		if len(a) > maxAddrLen {
			return id, fmt.Errorf("discovery payload has an oversized address")
		}
	}
	if len(id.Name) > maxPeerNameLen {
		// Truncate on a rune boundary so a clamped multi-byte name never
		// becomes invalid UTF-8 when re-encoded to the frontend.
		id.Name = strings.ToValidUTF8(id.Name[:maxPeerNameLen], "")
	}
	// Control chars would let a hostile announce forge log lines; BiDi marks
	// could visually reorder UI text built around the name.
	id.Name = sanitizeDisplayName(id.Name)
	if id.Name == "" {
		id.Name = "unknown device"
	}
	return id, nil
}

type trackedPeer struct {
	NearbyPeer
	lastSeen time.Time
}

// peerRegistry tracks live peers and notifies on membership/name changes.
type peerRegistry struct {
	mu       sync.Mutex
	selfID   string
	peers    map[string]*trackedPeer
	byeUntil map[string]time.Time // peer id -> garbage-collect suppression after
	byeGen   map[string]int       // peer id -> generation that said goodbye
	selfSeen time.Time

	onChange func([]NearbyPeer)
}

func newPeerRegistry(selfID string, onChange func([]NearbyPeer)) *peerRegistry {
	if onChange == nil {
		onChange = func([]NearbyPeer) {}
	}
	return &peerRegistry{
		selfID:   selfID,
		peers:    make(map[string]*trackedPeer),
		byeUntil: make(map[string]time.Time),
		byeGen:   make(map[string]int),
		onChange: onChange,
	}
}

// observe ingests one announcement. Self-announcements only refresh the
// health timestamp; farewell packets remove the peer immediately; other
// peers are added or refreshed.
func (r *peerRegistry) observe(id discoveryIdentity, addr string, now time.Time) {
	r.mu.Lock()
	if id.ID == r.selfID {
		r.selfSeen = now
		r.mu.Unlock()
		return
	}

	if id.Bye {
		_, existed := r.peers[id.ID]
		delete(r.peers, id.ID)
		r.byeUntil[id.ID] = now.Add(byeSuppression)
		r.byeGen[id.ID] = id.Gen
		var snap []NearbyPeer
		if existed {
			snap = r.snapshotLocked()
		}
		r.mu.Unlock()
		if existed {
			r.onChange(snap)
		}
		return
	}

	// Suppress a re-add only when it is a straggler from the same generation
	// that just said goodbye (an in-flight announcement). A higher generation
	// is an intentional unhide and must be honored immediately.
	if until, suppressed := r.byeUntil[id.ID]; suppressed {
		if now.Before(until) && id.Gen <= r.byeGen[id.ID] {
			r.mu.Unlock()
			return
		}
		delete(r.byeUntil, id.ID)
		delete(r.byeGen, id.ID)
	}

	existing, ok := r.peers[id.ID]
	changed := !ok || existing.Name != id.Name
	if ok {
		existing.lastSeen = now
		existing.Name = id.Name
		existing.Addr = addr
		existing.Addrs = id.Addrs
		existing.Port = id.Port
		existing.MachineID = id.MachineID
		existing.Fingerprint = id.Fingerprint
	} else {
		r.peers[id.ID] = &trackedPeer{
			NearbyPeer: NearbyPeer{
				ID:          id.ID,
				Name:        id.Name,
				Addr:        addr,
				Addrs:       id.Addrs,
				Port:        id.Port,
				MachineID:   id.MachineID,
				Fingerprint: id.Fingerprint,
			},
			lastSeen: now,
		}
	}
	var snap []NearbyPeer
	if changed {
		snap = r.snapshotLocked()
	}
	r.mu.Unlock()

	if changed {
		r.onChange(snap)
	}
}

// sweep drops peers not seen within peerTTL.
func (r *peerRegistry) sweep(now time.Time) {
	r.mu.Lock()
	changed := false
	for id, p := range r.peers {
		if now.Sub(p.lastSeen) > peerTTL {
			delete(r.peers, id)
			changed = true
		}
	}
	for id, until := range r.byeUntil {
		if now.After(until) {
			delete(r.byeUntil, id)
			delete(r.byeGen, id)
		}
	}
	var snap []NearbyPeer
	if changed {
		snap = r.snapshotLocked()
	}
	r.mu.Unlock()

	if changed {
		r.onChange(snap)
	}
}

// selfHealthy reports whether we heard our own announcement recently —
// the proxy for "the discovery socket actually works on this network".
func (r *peerRegistry) selfHealthy(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.selfSeen.IsZero() && now.Sub(r.selfSeen) <= peerTTL
}

func (r *peerRegistry) get(id string) (NearbyPeer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.peers[id]
	if !ok {
		return NearbyPeer{}, false
	}
	return p.NearbyPeer, true
}

func (r *peerRegistry) snapshot() []NearbyPeer {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

func (r *peerRegistry) snapshotLocked() []NearbyPeer {
	out := make([]NearbyPeer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, p.NearbyPeer)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// startDiscovery runs announce+listen and the liveness sweeper until the
// returned stop function is called. State changes (available/blocked) are
// reported through onState. With announce=false ("invisible") we only
// listen: others can't see us, we still see them — and the self-echo health
// check is suspended, since we produce no echoes to hear.
func startDiscovery(identity discoveryIdentity, registry *peerRegistry, onState func(DiscoveryState), announce bool) (stop func()) {
	if onState == nil {
		onState = func(DiscoveryState) {}
	}

	stopChan := make(chan struct{})
	var once sync.Once

	payload := encodeIdentity(identity)
	if payload == nil {
		onState(DiscoveryState{Available: false})
		return func() {}
	}

	go func() {
		_, err := peerdiscovery.Discover(peerdiscovery.Settings{
			Limit:            -1,
			TimeLimit:        -1,
			Port:             discoveryPort,
			Payload:          payload,
			Delay:            announceInterval,
			AllowSelf:        true,
			DisableBroadcast: !announce,
			StopChan:         stopChan,
			Notify: func(d peerdiscovery.Discovered) {
				id, err := decodeIdentity(d.Payload)
				if err != nil {
					logrus.WithError(err).Debug("ignoring invalid discovery announcement")
					return
				}
				registry.observe(id, d.Address, time.Now())
			},
		})
		if err != nil {
			logrus.WithError(err).Warn("nearby discovery stopped: socket unavailable")
			onState(DiscoveryState{Available: false})
		}
	}()

	go func() {
		started := time.Now()
		available := true // optimistic until health says otherwise
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				now := time.Now()
				registry.sweep(now)

				if !announce {
					continue // no self-echo to measure health with
				}
				healthy := registry.selfHealthy(now)
				if !healthy && now.Sub(started) < healthTimeout {
					continue // still warming up
				}
				if healthy != available {
					available = healthy
					onState(DiscoveryState{Available: available})
				}
			}
		}
	}()

	return func() {
		once.Do(func() {
			if announce {
				broadcastGoodbye(identity)
			}
			close(stopChan)
		})
	}
}

// broadcastGoodbye fires a short burst of farewell packets so peers drop us
// instantly on clean shutdown instead of waiting out the TTL. Crashes still
// rely on expiry.
func broadcastGoodbye(identity discoveryIdentity) {
	payload := encodeIdentity(discoveryIdentity{ID: identity.ID, Gen: identity.Gen, Bye: true})
	if payload == nil {
		return
	}
	_, err := peerdiscovery.Discover(peerdiscovery.Settings{
		Limit:     -1,
		Port:      discoveryPort,
		Payload:   payload,
		Delay:     150 * time.Millisecond,
		TimeLimit: 500 * time.Millisecond,
	})
	if err != nil {
		logrus.WithError(err).Debug("goodbye broadcast failed")
	}
}
