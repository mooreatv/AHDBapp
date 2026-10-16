package main

// Syncing with the ahdbmerge -serve of another computer. TLS with a key per computer (made on first use, kept in the
// user config folder); each side confirms the other's key the first time (like ssh), then remembers it.
//
// Messages are gzipped JSON, in that order: server name and version, client name and version and the server's history version it has if
// nothing is new on its side (server confirms, or says it's still that version: done), client price history, merged
// price history and its version back.

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

	"fortio.org/cli"
	"fortio.org/log"
)

const (
	defaultPort  = 8642
	syncTimeout  = 10 * time.Minute // includes waiting for the other side to confirm
	maxHelloSize = 64 << 10
	maxDataSize  = 1 << 30
)

type message struct {
	Name     string `json:",omitempty"` // computer name
	Release  string `json:",omitempty"` // ahdbmerge version, with the name
	DryRun   bool   `json:",omitempty"`
	Have     string `json:",omitempty"` // client: version of the server's history it has, when nothing new on its side
	UpToDate bool   `json:",omitempty"` // server: still that version, nothing to sync
	Version  string `json:",omitempty"` // server: version of its history
	Error    string `json:",omitempty"`
	Houses   Houses `json:",omitempty"`
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

var versionWarned sync.Map // name and version already warned about

// checkRelease warns (once per computer and version) when the other side runs another ahdbmerge version.
func checkRelease(name, v string) {
	if v == cli.ShortVersion {
		return
	}
	if _, done := versionWarned.LoadOrStore(name+" "+v, true); done {
		return
	}
	if v == "" {
		v = "an older version"
	}
	log.Warnf("%s runs ahdbmerge %s, this computer %s: best to update both to the same version", name, v,
		cli.ShortVersion)
}

// remote is the other side of a sync.
type remote struct {
	key, name string
}

var (
	errNotAllowed = errors.New("not allowed")
	errSelf       = errors.New("that's this computer")
)

// syncWith sends the local price history to the ahdbmerge -serve at addr and returns the merged one, with the
// server's version of it. With have (the server's version from the last sync, when nothing is new here), returns no
// history if the server still has that version.
func syncWith(id *identity, addr string, local Houses, have string) (Houses, remote, string, error) {
	var rem remote
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, id.tlsConfig())
	if err != nil {
		return nil, rem, "", err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(syncTimeout))
	if rem.key, err = peerKey(conn); err != nil {
		return nil, rem, "", err
	}
	r := bufio.NewReader(conn)
	m, err := receive(r, maxHelloSize)
	if err != nil {
		return nil, rem, "", err
	}
	rem.name = m.Name
	switch {
	case rem.key == id.key:
		return nil, rem, "", errSelf
	case !id.allow(rem.key, fmt.Sprintf("%s at %s", m.Name, addr), m.Name):
		return nil, rem, "", errNotAllowed
	}
	checkRelease(rem.name, m.Release)
	if err := send(conn, &message{Name: hostname(), Release: cli.ShortVersion, DryRun: *dryRun, Have: have}); err != nil {
		return nil, rem, "", err
	}
	waiting := time.AfterFunc(2*time.Second, func() {
		log.Infof("Waiting for %s to allow this computer (asked there the first time)...", m.Name)
	})
	m, err = receive(r, maxHelloSize)
	waiting.Stop()
	switch {
	case err != nil:
		return nil, rem, "", err
	case m.Error != "":
		return nil, rem, "", fmt.Errorf("%w on %s", errNotAllowed, rem.name)
	case m.UpToDate:
		return nil, rem, m.Version, nil
	}
	if err := send(conn, &message{Houses: local}); err != nil {
		return nil, rem, "", err
	}
	if m, err = receive(r, maxDataSize); err != nil {
		return nil, rem, "", err
	}
	if m.Error != "" {
		return nil, rem, "", errors.New(m.Error)
	}
	if m.Houses == nil {
		m.Houses = Houses{}
	}
	return m.Houses, rem, m.Version, m.Houses.validate()
}

// answerer is the server side of the history: its version, and merging what another computer sends.
type answerer interface {
	version() string
	merge(rem remote, hs Houses, dry bool) (Houses, string)
}

// answer is the server side of syncWith.
func answer(id *identity, conn *tls.Conn, srv answerer) error {
	_ = conn.SetDeadline(time.Now().Add(syncTimeout))
	key, err := peerKey(conn)
	if err != nil {
		return err
	}
	if err := send(conn, &message{Name: hostname(), Release: cli.ShortVersion}); err != nil {
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
	checkRelease(rem.name, m.Release)
	if v := srv.version(); m.Have != "" && m.Have == v {
		return send(conn, &message{UpToDate: true, Version: v})
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
	merged, v := srv.merge(rem, m.Houses, dry)
	return send(conn, &message{Houses: merged, Version: v})
}
