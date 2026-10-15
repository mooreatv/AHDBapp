package main

// Syncing with the ahdbmerge -serve of another computer. TLS with a key per computer (made on first use, kept in the
// user config folder); each side confirms the other's key the first time (like ssh), then remembers it (with where
// its -serve answers, if it does).
//
// Messages are gzipped JSON, in that order: server name, client name and -serve port (server confirms), client price
// history, merged price history back.

import (
	"bufio"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"fortio.org/log"
)

const (
	defaultPort  = 8642
	syncTimeout  = 10 * time.Minute // includes waiting for the other side to confirm
	maxHelloSize = 64 << 10
	maxDataSize  = 1 << 30
)

type message struct {
	Name   string `json:",omitempty"` // computer name
	DryRun bool   `json:",omitempty"`
	Port   int    `json:",omitempty"` // where the sender's -serve answers
	Error  string `json:",omitempty"`
	Houses Houses `json:",omitempty"`
}

func send(w io.Writer, m *message) error {
	zw := gzip.NewWriter(w)
	if err := json.NewEncoder(zw).Encode(m); err != nil {
		return err
	}
	return zw.Close()
}

func receive(r *bufio.Reader, limit int64) (*message, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	zr.Multistream(false)
	lr := io.LimitReader(zr, limit)
	m := &message{}
	if err := json.NewDecoder(lr).Decode(m); err != nil {
		return nil, err
	}
	if _, err := io.Copy(io.Discard, lr); err != nil { // rest of this message
		return nil, err
	}
	return m, nil
}

var houseName = regexp.MustCompile(`^[A-Za-z]+$`)

// validate checks what the other side sent can be written as Lua.
func (hs Houses) validate() error {
	for name, h := range hs {
		if !houseName.MatchString(name) || h == nil {
			return fmt.Errorf("bad auction house %q", name)
		}
		if h.Items == nil {
			h.Items = map[int64][]Point{}
		}
		if h.Daily == nil {
			h.Daily = map[int64][]Point{}
		}
	}
	return nil
}

// peer is a trusted computer.
type peer struct {
	Name string
	Addr string `json:",omitempty"` // where its ahdbmerge -serve answers, if it does
}

type identity struct {
	cert    tls.Certificate
	key     string // own fingerprint
	file    string // trusted keys
	trusted map[string]*peer
	mu      sync.Mutex
}

func fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// short is what's shown to compare keys: the first 96 bits.
func short(fp string) string {
	var parts []string
	for i := 0; i+4 <= 24 && i+4 <= len(fp); i += 4 {
		parts = append(parts, fp[i:i+4])
	}
	return strings.Join(parts, "-")
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, err
		}
		return key, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	}
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("%s: not a key", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	key, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an ed25519 key", path)
	}
	return key, nil
}

func loadIdentity() (*identity, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return identityIn(filepath.Join(base, "ahdbmerge"))
}

// identityIn loads (or makes) the key and trusted keys kept in dir.
func identityIn(dir string) (*identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadKey(filepath.Join(dir, "key.pem"))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ahdbmerge"},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	id := &identity{
		cert:    tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		key:     fingerprint(c),
		file:    filepath.Join(dir, "trusted.json"),
		trusted: map[string]*peer{},
	}
	b, err := os.ReadFile(id.file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &id.trusted); err != nil {
			return nil, fmt.Errorf("%s: %w", id.file, err)
		}
	}
	return id, nil
}

var stdin = bufio.NewReader(os.Stdin)

func confirm(question string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	line, _ := stdin.ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// allow returns whether the key is trusted, asking the first time.
func (id *identity) allow(fp, question string, name string) bool {
	id.mu.Lock()
	defer id.mu.Unlock()
	if _, ok := id.trusted[fp]; ok {
		return true
	}
	log.Infof("This computer's key is %s", short(id.key))
	if !confirm(fmt.Sprintf("%s, key %s: allow syncing price history with it?", question, short(fp))) {
		return false
	}
	id.trusted[fp] = &peer{Name: name}
	id.save()
	return true
}

func (id *identity) save() {
	b, _ := json.MarshalIndent(id.trusted, "", "  ")
	if err := os.WriteFile(id.file, b, 0o600); err != nil {
		log.Errf("Saving %s: %v", id.file, err)
	}
}

// setAddr remembers where the -serve of a trusted computer answers. Without given, only replaces an IP address (the
// computer may have a new one), not a name given to -sync or -peer.
func (id *identity) setAddr(fp, addr string, given bool) {
	id.mu.Lock()
	defer id.mu.Unlock()
	p := id.trusted[fp]
	if p == nil || p.Addr == addr {
		return
	}
	if host, _, err := net.SplitHostPort(p.Addr); !given && err == nil && net.ParseIP(host) == nil {
		return
	}
	p.Addr = addr
	id.save()
}

// servers returns the address of the -serve of each trusted computer that has one, by key.
func (id *identity) servers() map[string]string {
	id.mu.Lock()
	defer id.mu.Unlock()
	res := map[string]string{}
	for fp, p := range id.trusted {
		if p.Addr != "" {
			res[fp] = p.Addr
		}
	}
	return res
}

func (id *identity) tlsConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id.cert},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert, // checked against the trusted keys after the handshake
		// Self-made certificates, the key is checked against the trusted ones after the handshake.
		InsecureSkipVerify: true, //nolint:gosec // see above
	}
}

func peerKey(c *tls.Conn) (string, error) {
	if err := c.Handshake(); err != nil {
		return "", err
	}
	certs := c.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("no key")
	}
	return fingerprint(certs[0]), nil
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func withPort(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), strconv.Itoa(*port))
}

// remote is the other side of a sync.
type remote struct {
	key, name string
}

// syncWith sends the local price history to the ahdbmerge -serve at addr and returns the merged one. With expect,
// the other side must have that key (no question asked); servePort is where this side's -serve answers, if it does.
func syncWith(id *identity, addr string, local Houses, expect string, servePort int) (Houses, remote, error) {
	var rem remote
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, id.tlsConfig())
	if err != nil {
		return nil, rem, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(syncTimeout))
	if rem.key, err = peerKey(conn); err != nil {
		return nil, rem, err
	}
	r := bufio.NewReader(conn)
	m, err := receive(r, maxHelloSize)
	if err != nil {
		return nil, rem, err
	}
	rem.name = m.Name
	switch {
	case rem.key == id.key:
		return nil, rem, errSelf
	case expect != "" && rem.key != expect:
		return nil, rem, fmt.Errorf("%s has another key (%s) than before, not syncing", m.Name, short(rem.key))
	case !id.allow(rem.key, fmt.Sprintf("%s at %s", m.Name, addr), m.Name):
		return nil, rem, errNotAllowed
	}
	if err := send(conn, &message{Name: hostname(), DryRun: *dryRun, Port: servePort}); err != nil {
		return nil, rem, err
	}
	waiting := time.AfterFunc(2*time.Second, func() {
		log.Infof("Waiting for %s to allow this computer (asked there the first time)...", m.Name)
	})
	m, err = receive(r, maxHelloSize)
	waiting.Stop()
	if err != nil {
		return nil, rem, err
	}
	if m.Error != "" {
		return nil, rem, fmt.Errorf("%w on %s", errNotAllowed, rem.name)
	}
	id.setAddr(rem.key, addr, true)
	if err := send(conn, &message{Houses: local}); err != nil {
		return nil, rem, err
	}
	if m, err = receive(r, maxDataSize); err != nil {
		return nil, rem, err
	}
	if m.Error != "" {
		return nil, rem, errors.New(m.Error)
	}
	return m.Houses, rem, m.Houses.validate()
}

// answer is the server side of syncWith: merge gets what the other side sent and returns what to send back.
func answer(id *identity, conn *tls.Conn, merge func(remote, Houses, bool) (Houses, error)) error {
	_ = conn.SetDeadline(time.Now().Add(syncTimeout))
	key, err := peerKey(conn)
	if err != nil {
		return err
	}
	if err := send(conn, &message{Name: hostname()}); err != nil {
		return err
	}
	r := bufio.NewReader(conn)
	m, err := receive(r, maxHelloSize)
	if err != nil {
		return err
	}
	ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	rem, dry := remote{key: key, name: m.Name}, m.DryRun
	if key == id.key || !id.allow(key, fmt.Sprintf("Sync from %s (%s)", m.Name, ip), m.Name) {
		_ = send(conn, &message{Error: "not allowed"})
		return errNotAllowed
	}
	if m.Port > 0 {
		id.setAddr(key, net.JoinHostPort(ip, strconv.Itoa(m.Port)), false)
	}
	if err := send(conn, &message{}); err != nil {
		return err
	}
	if m, err = receive(r, maxDataSize); err != nil {
		return err
	}
	if err := m.Houses.validate(); err != nil {
		_ = send(conn, &message{Error: err.Error()})
		return err
	}
	merged, err := merge(rem, m.Houses, dry)
	if err != nil {
		_ = send(conn, &message{Error: hostname() + ": " + err.Error()})
		return err
	}
	return send(conn, &message{Houses: merged})
}
