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

// startServer answers one sync, like -serve, on a free port.
func startServer(t *testing.T, id *identity, dir string) (string, chan error) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", id.tlsConfig())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = conn.Close() }()
		s := newServer(id, dir, nil, 0)
		s.files()
		done <- answer(id, conn.(*tls.Conn), s.merge)
		s.files() // what the files goroutine does after a merge
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
	addr, done := startServer(t, server, dirB)
	got, rem, err := syncWith(client, addr, local, "", 1234)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
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
	// both remember where the other's -serve answers
	if a := client.trusted[server.key].Addr; a != addr {
		t.Errorf("client has server at %q", a)
	}
	if a := server.trusted[client.key].Addr; a != "127.0.0.1:1234" {
		t.Errorf("server has client at %q", a)
	}
	if wowRunning() == "" {
		files, onServer, err := loadLocal(dirB)
		if err != nil || len(files) != 1 {
			t.Fatal(files, err)
		}
		if onServer.Lua("\n") != want.Lua("\n") {
			t.Error("server files not updated")
		}
	}
	// with the wrong expected key: no sync
	addr, done = startServer(t, server, dirB)
	if _, _, err := syncWith(client, addr, local, client.key, 0); err == nil {
		t.Error("synced with an unexpected key")
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
	addr, done := startServer(t, server, dirB)
	_, _, err := syncWith(client, addr, Houses{}, "", 0)
	if !errors.Is(err, errNotAllowed) {
		t.Errorf("sync not refused: %v", err)
	}
	<-done
	if len(server.trusted) != 0 {
		t.Errorf("refused key trusted: %v", server.trusted)
	}
}

func TestSetAddr(t *testing.T) {
	id, _ := identityIn(t.TempDir())
	id.trusted["k"] = &peer{Name: "laptop"}
	id.setAddr("k", "192.168.1.5:8642", false)
	id.setAddr("k", "192.168.1.6:8642", false) // new IP
	if a := id.trusted["k"].Addr; a != "192.168.1.6:8642" {
		t.Errorf("addr = %q", a)
	}
	id.setAddr("k", "laptop:8642", true)
	id.setAddr("k", "192.168.1.7:8642", false) // the name given to -peer stays
	if a := id.trusted["k"].Addr; a != "laptop:8642" {
		t.Errorf("addr = %q", a)
	}
	id.setAddr("other", "x:1", true) // not trusted: nothing
	if len(id.trusted) != 1 {
		t.Error(id.trusted)
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
