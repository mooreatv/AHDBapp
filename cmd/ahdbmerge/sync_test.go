package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func wtf(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	sv := filepath.Join(dir, "Account", "ACC", "SavedVariables")
	if err := os.MkdirAll(sv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sv, "AuctionDB.lua"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// startServer answers n syncs, like -serve, on a free port.
func startServer(t *testing.T, id *identity, dir string, n int) (string, chan error) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", id.tlsConfig())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, n)
	s := newServer(id, dir, "", 0)
	s.files()
	go func() {
		defer func() { _ = ln.Close() }()
		for range n {
			conn, err := ln.Accept()
			if err != nil {
				done <- err
				return
			}
			err = answer(id, conn.(*tls.Conn), s)
			_ = conn.Close()
			s.files() // what the files goroutine does after a merge
			done <- err
		}
	}()
	return ln.Addr().String(), done
}

// The test data is from 1971.
func fixedNow(t *testing.T) {
	t.Helper()
	old := now
	now = func() int64 { return 86440000 }
	t.Cleanup(func() { now = old })
}

func TestSync(t *testing.T) {
	fixedNow(t)
	server, err := identityIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client, err := identityIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if server.key == client.key {
		t.Fatal("same key for both")
	}
	// already trusted both ways: no questions
	server.trusted[client.key] = &peer{Name: "client"}
	client.trusted[server.key] = &peer{Name: "server"}
	dirA, dirB := wtf(t, accountA), wtf(t, accountB)
	_, local, err := loadLocal(dirA)
	if err != nil {
		t.Fatal(err)
	}
	addr, done := startServer(t, server, dirB, 3)
	got, rem, ver, err := syncWith(client, addr, local, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
	if rem.key != server.key || rem.name != hostname() {
		t.Errorf("remote = %+v", rem)
	}
	want, _ := read(t, accountA)
	b, _ := read(t, accountB)
	want.Merge(b)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("synced = %v, want %v", got, want)
	}
	if ver != versionOf(want.Lua("\n")) {
		t.Errorf("version %q", ver)
	}
	files, onServer, err := loadLocal(dirB)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	if onServer.Lua("\n") != want.Lua("\n") {
		t.Error("server files not updated")
	}
	// nothing new on either side: only checked
	got, _, ver2, err := syncWith(client, addr, local, ver)
	if err != nil || got != nil || ver2 != ver {
		t.Errorf("check: %v, %v, %q", got, err, ver2)
	}
	if err = <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
	// an old version: everything again
	got, _, ver2, err = syncWith(client, addr, local, "old")
	if err != nil || !reflect.DeepEqual(got, want) || ver2 != ver {
		t.Errorf("sync with an old version: %v, %q", err, ver2)
	}
	<-done
}

func TestSyncNotAllowed(t *testing.T) {
	fixedNow(t)
	server, _ := identityIn(t.TempDir())
	client, _ := identityIn(t.TempDir())
	client.trusted[server.key] = &peer{Name: "server"}
	stdin = bufio.NewReader(strings.NewReader("n\n")) // the server says no
	defer func() { stdin = bufio.NewReader(os.Stdin) }()
	dirB := wtf(t, accountB)
	addr, done := startServer(t, server, dirB, 1)
	_, _, _, err := syncWith(client, addr, Houses{}, "") //nolint:dogsled // only the error matters
	if !errors.Is(err, errNotAllowed) {
		t.Errorf("sync not refused: %v", err)
	}
	<-done
	if len(server.trusted) != 0 {
		t.Errorf("refused key trusted: %v", server.trusted)
	}
}

func TestValidate(t *testing.T) {
	if err := (Houses{`x"]=os.exit()--`: newHouse()}).validate(); err == nil {
		t.Error("bad house name accepted")
	}
	if err := (Houses{"Alliance": &House{}}).validate(); err != nil {
		t.Error(err)
	}
}
