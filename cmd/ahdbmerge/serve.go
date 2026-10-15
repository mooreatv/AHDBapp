package main

// Keeping running: the merged price history of this computer and the others is kept in memory.
// - With -serve (the hub): answers the other computers' syncs; never connects to them, they come when they have
//   something new or to check if it has.
// - With -sync HUB: syncs with the hub when there is something new here, and checks every pollEvery if the hub has
//   something new (a small exchange when it doesn't).
// Something new here: at start and when the AuctionDB.lua files change (WoW saves them at logout, /reload and exit).
// The files here are written whenever WoW isn't running (it would overwrite them on exit), at the latest when it
// exits.

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"fortio.org/log"
)

const (
	watchEvery = 5 * time.Second
	pollEvery  = 15 * time.Second
)

type server struct {
	id   *identity
	dir  string
	hub  string // -sync address
	port int
	mu   sync.Mutex
	// never changed once set, replaced instead
	state Houses
	lua   string // of state, to see changes
	ver   string // short hash of lua
	kick  chan struct{}
	write chan struct{}
	// only used by the files goroutine
	mtimes map[string]time.Time
	wow    string // running WoW client at the last check
	// only used by the hub goroutine
	hubVer  string // hub's version at the last sync
	synced  string // own version after the last sync
	down    bool   // last sync with the hub failed
	refused bool
}

func versionOf(lua string) string {
	sum := sha256.Sum256([]byte(lua))
	return hex.EncodeToString(sum[:8])
}

func newServer(id *identity, dir, hub string, port int) *server {
	empty := Houses{}.Lua("\n")
	s := &server{
		id: id, dir: dir, port: port, state: Houses{}, lua: empty, ver: versionOf(empty),
		kick: make(chan struct{}, 1), write: make(chan struct{}, 1), mtimes: map[string]time.Time{},
	}
	if hub != "" {
		s.hub = withPort(hub)
	}
	return s
}

// absorb merges hs into the state, returns what that added ("" if nothing changed).
func (s *server) absorb(hs Houses) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := Houses{}
	next.Merge(s.state)
	next.Merge(hs)
	next.Compact(now())
	l := next.Lua("\n")
	if l == s.lua {
		return ""
	}
	added := s.state.Added(next)
	s.state, s.lua, s.ver = next, l, versionOf(l)
	return added
}

func (s *server) snapshot() (Houses, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.ver
}

func (s *server) version() string {
	_, v := s.snapshot()
	return v
}

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// filesChanged returns whether the AuctionDB.lua files changed since the last check.
func (s *server) filesChanged() bool {
	changed := false
	for _, p := range findFiles(s.dir) {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !st.ModTime().Equal(s.mtimes[p]) {
			s.mtimes[p] = st.ModTime()
			changed = true
		}
	}
	return changed
}

// files reads the files into the state, syncs with the hub if they brought something new, and writes the files
// with the state when WoW isn't running.
func (s *server) files() {
	files, hs, err := loadLocal(s.dir)
	if err != nil {
		log.Errf("%v", err)
		return
	}
	if added := s.absorb(hs); added != "" {
		log.Infof("New from the files here: %s", added)
		signal(s.kick)
	}
	if s.wow == "" && !*dryRun {
		state, _ := s.snapshot()
		writeLocal(files, state, false)
	}
	s.filesChanged() // not our own writes
}

func (s *server) watchFiles() {
	tick := time.NewTicker(watchEvery)
	for {
		select {
		case <-tick.C:
			exe := wowRunning()
			exited := s.wow != "" && exe == ""
			s.wow = exe
			if s.filesChanged() || exited {
				s.files()
			}
		case <-s.write:
			s.wow = wowRunning()
			s.files()
		}
	}
}

// syncHub syncs with the hub: everything if something is new here, else only checks if the hub has something new.
func (s *server) syncHub() {
	state, ver := s.snapshot()
	have := ""
	if ver == s.synced {
		have = s.hubVer
	}
	hs, rem, hubVer, err := syncWith(s.id, s.hub, state, have)
	if err != nil {
		if errors.Is(err, errNotAllowed) || errors.Is(err, errSelf) {
			log.Errf("Sync with %s: %v, stopped syncing", s.hub, err)
			s.refused = true
			return
		}
		if !s.down {
			log.Warnf("Can't sync with %s: %v (will keep trying)", s.hub, err)
			s.down = true
		}
		return
	}
	if s.down {
		log.Infof("Syncing with %s (%s) again", rem.name, s.hub)
		s.down = false
	}
	s.hubVer = hubVer
	if hs != nil {
		if added := s.absorb(hs); added != "" {
			log.Infof("Synced with %s, new here: %s", rem.name, added)
			signal(s.write)
		} else {
			log.Infof("Synced with %s: sent what's new here", rem.name)
		}
	}
	_, s.synced = s.snapshot()
}

func (s *server) syncLoop() {
	tick := time.NewTicker(pollEvery)
	for !s.refused {
		s.syncHub()
		select {
		case <-s.kick:
		case <-tick.C:
		}
	}
}

// merge answers a sync from another computer.
func (s *server) merge(rem remote, hs Houses, dry bool) (Houses, string) {
	if dry {
		state, ver := s.snapshot()
		res := Houses{}
		res.Merge(state)
		res.Merge(hs)
		res.Compact(now())
		return res, ver
	}
	if added := s.absorb(hs); added != "" {
		log.Infof("Sync from %s, new here: %s", rem.name, added)
		signal(s.write)
		signal(s.kick)
	} else {
		log.Infof("Sync from %s: nothing new here", rem.name)
	}
	return s.snapshot()
}

func (s *server) run(listen bool) {
	var ln net.Listener
	if listen {
		var err error
		ln, err = tls.Listen("tcp", fmt.Sprintf(":%d", s.port), s.id.tlsConfig())
		if err != nil {
			log.Fatalf("Listening on port %d: %v", s.port, err)
		}
		log.Infof("%s, key %s: waiting for syncs on port %d", hostname(), short(s.id.key), s.port)
	} else {
		log.Infof("%s, key %s: syncing with %s", hostname(), short(s.id.key), s.hub)
	}
	s.wow = wowRunning()
	if s.wow != "" {
		log.Infof("WoW is running (%s): the files here get updated when it exits", s.wow)
	}
	s.files()
	go s.watchFiles()
	if s.hub != "" {
		if !listen {
			s.syncLoop()
			return
		}
		go s.syncLoop()
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Errf("Accept: %v", err)
			continue
		}
		if err := answer(s.id, conn.(*tls.Conn), s); err != nil {
			log.Warnf("Sync from %s: %v", conn.RemoteAddr(), err)
		}
		_ = conn.Close()
	}
}
