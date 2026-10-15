package main

// ahdbmerge -serve: keeps the merged price history of this computer and the others it syncs with, answers their syncs,
// and syncs with them (the -sync one and every trusted one whose -serve answered before) when there is something new:
// at start, when the AuctionDB.lua files change (WoW saves them at logout, /reload and exit) and after getting new
// data from one of them (so with a hub, what one computer sends reaches the others). The files here are written
// whenever WoW isn't running (it would overwrite them on exit), at the latest when it exits.

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"fortio.org/log"
)

const watchEvery = 5 * time.Second

type server struct {
	id    *identity
	dir   string
	peer  string // -sync address
	port  int
	mu    sync.Mutex
	state Houses // never changed once set, replaced instead
	lua   string // of state, to see changes
	kick  chan struct{}
	write chan struct{}
	// only used by the files goroutine
	mtimes map[string]time.Time
	wow    string // running WoW client at the last check
	// only used by the peers goroutine
	refused map[string]bool // addresses refused, not asked again
}

func newServer(id *identity, dir, peer string, port int) *server {
	s := &server{
		id: id, dir: dir, port: port, state: Houses{}, lua: Houses{}.Lua("\n"),
		kick: make(chan struct{}, 1), write: make(chan struct{}, 1),
		mtimes: map[string]time.Time{}, refused: map[string]bool{},
	}
	if peer != "" {
		s.peer = withPort(peer)
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
	s.state, s.lua = next, l
	return added
}

func (s *server) snapshot() Houses {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
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

// files reads the files into the state, syncs with the others if they brought something new, and writes the files
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
		writeLocal(files, s.snapshot(), false)
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

var (
	errNotAllowed = errors.New("not allowed")
	errSelf       = errors.New("that's this computer")
)

// syncPeers syncs with every other computer, again if one brought something new (for the ones before it).
func (s *server) syncPeers() {
	for range 3 {
		news := false
		done := map[string]bool{}
		known := s.id.servers()
		keyOf := map[string]string{}
		var targets []string
		if s.peer != "" {
			targets = append(targets, s.peer)
		}
		for fp, a := range known {
			keyOf[a] = fp
			targets = append(targets, a)
		}
		for _, addr := range targets {
			expect := keyOf[addr]
			if s.refused[addr] || (expect != "" && done[expect]) {
				continue
			}
			hs, rem, err := syncWith(s.id, addr, s.snapshot(), expect, s.port)
			if rem.key != "" {
				done[rem.key] = true
			}
			if err != nil {
				log.Warnf("Sync with %s: %v", addr, err)
				if errors.Is(err, errNotAllowed) || errors.Is(err, errSelf) {
					s.refused[addr] = true
				}
				continue
			}
			if added := s.absorb(hs); added != "" {
				log.Infof("Synced with %s (%s), new here: %s", rem.name, addr, added)
				news = true
			} else {
				log.Infof("Synced with %s (%s): nothing new here", rem.name, addr)
			}
		}
		if !news {
			return
		}
		signal(s.write)
	}
}

// merge answers a sync from another computer.
func (s *server) merge(rem remote, hs Houses, dry bool) (Houses, error) {
	if dry {
		res := Houses{}
		res.Merge(s.snapshot())
		res.Merge(hs)
		res.Compact(now())
		return res, nil
	}
	if added := s.absorb(hs); added != "" {
		log.Infof("Sync from %s, new here: %s", rem.name, added)
		signal(s.write)
		signal(s.kick)
	} else {
		log.Infof("Sync from %s: nothing new here", rem.name)
	}
	return s.snapshot(), nil
}

func (s *server) run() {
	ln, err := tls.Listen("tcp", fmt.Sprintf(":%d", s.port), s.id.tlsConfig())
	if err != nil {
		log.Fatalf("Listening on port %d: %v", s.port, err)
	}
	log.Infof("%s, key %s: syncing on port %d", hostname(), short(s.id.key), s.port)
	s.wow = wowRunning()
	if s.wow != "" {
		log.Infof("WoW is running (%s): the files here get updated when it exits", s.wow)
	}
	s.files()
	signal(s.kick)
	go func() {
		for range s.kick {
			s.syncPeers()
		}
	}()
	go s.watchFiles()
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Errf("Accept: %v", err)
			continue
		}
		if err := answer(s.id, conn.(*tls.Conn), s.merge); err != nil {
			log.Warnf("Sync from %s: %v", conn.RemoteAddr(), err)
		}
		_ = conn.Close()
	}
}
