// Copyright 2026 MooreaTv moorea@ymail.com
// All Rights Reserved
//
// GPLv3 License (which means no commercial integration)
// ask if you need a different License

// ahdbmerge merges the AHDB (AuctionDB, WoW Forever edition) price history of all the WoW accounts of a game
// install, so each account's characters see the prices any of them scanned. Run it with the game closed:
//
//	ahdbmerge "C:\Program Files (x86)\World of Warcraft\_classic_beta_\WTF"
//
// Every Account\*\SavedVariables\AuctionDB.lua gets the merged price history (only that part of the file is
// rewritten, the previous file is kept as AuctionDB.lua.bak).
//
// Between computers: keep "ahdbmerge -serve <WTF>" running on each, with "-peer <other computer>" on one of them (or
// all with -peer <hub> for more than 2), or run "ahdbmerge -sync <computer with -serve> <WTF>" with the game closed.
// Each side confirms the other the first time.
package main

import (
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"fortio.org/cli"
	"fortio.org/log"
)

const savedName = "AuctionDBForeverSaved"

var (
	force     = flag.Bool("force", false, "merge even when WoW seems to be running (it would overwrite the files on exit)")
	dryRun    = flag.Bool("n", false, "only show what would be merged, don't write anything")
	serveFlag = flag.Bool("serve", false, "keep running: sync with your other computers whenever there is something new")
	syncAddr  = flag.String("sync", "", "also merge with the `computer` (name or address) running ahdbmerge -serve")
	peerAddrs = flag.String("peer", "", "with -serve: other `computers` (comma separated) running ahdbmerge -serve")
	port      = flag.Int("port", defaultPort, "network port for -serve and -sync")
	// logs the price history counts (not in -serve)
	logCounts = true
	now       = func() int64 { return time.Now().Unix() }
)

type savedFile struct {
	path   string
	text   string
	houses Houses
	start  int // byte span of the houses table in text
	end    int
}

func findFiles(dir string) []string {
	for _, d := range []string{dir, filepath.Join(dir, "WTF")} {
		files, _ := filepath.Glob(filepath.Join(d, "Account", "*", "SavedVariables", "AuctionDB.lua"))
		if len(files) > 0 {
			return files
		}
	}
	return nil
}

// WoW game clients: Wow.exe (retail), WowB.exe (beta, WoW Forever), WowT.exe (PTR), WowClassic.exe... but not
// other programs starting with Wow (WowUp...).
var wowExe = regexp.MustCompile(`(?i)^"(wow(classic)?[bt]?(-64)?\.exe)"`)

// wowRunning returns the name of a running WoW client, "" if none (Windows only).
func wowRunning() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		log.Warnf("Can't list processes: %v", err)
		return ""
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if m := wowExe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

func readFile(path string) (*savedFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := &savedFile{path: path, text: string(b)}
	t, err := ParseAssignment(f.text, savedName)
	if err != nil {
		return nil, err
	}
	e := t.Get("houses")
	if e == nil {
		return f, nil // never scanned
	}
	ht, ok := e.Val.(*Table)
	if !ok {
		return f, nil
	}
	f.houses, err = ReadHouses(ht)
	f.start, f.end = e.Start, e.End
	return f, err
}

// detail logs what -serve only shows with -loglevel debug.
func detail(format string, args ...any) {
	if logCounts {
		log.Infof(format, args...)
	} else {
		log.Debugf(format, args...)
	}
}

func logHouses(what string, hs Houses) {
	for _, name := range slices.Sorted(maps.Keys(hs)) {
		items, recent, daily, scans := hs[name].Counts()
		detail("%s: %s AH: %d items, %d recent scan prices, %d daily, %d scans", what, name, items, recent, daily, scans)
	}
}

// loadLocal reads the files with price history under dir, and returns them with their merged history.
func loadLocal(dir string) ([]*savedFile, Houses, error) {
	paths := findFiles(dir)
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("no Account\\*\\SavedVariables\\AuctionDB.lua under %s", dir)
	}
	merged := Houses{}
	var files []*savedFile
	for _, p := range paths {
		f, err := readFile(p)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p, err)
		}
		if f.houses == nil {
			detail("%s: no price history, skipped", p)
			continue
		}
		logHouses(p, f.houses)
		merged.Merge(f.houses)
		files = append(files, f)
	}
	merged.Compact(now())
	return files, merged, nil
}

// writeLocal writes the merged history in the files that don't have it yet.
func writeLocal(files []*savedFile, merged Houses, dry bool) {
	logHouses("Merged", merged)
	for _, f := range files {
		nl := "\n"
		if strings.Contains(f.text, "\r\n") {
			nl = "\r\n"
		}
		if f.houses.Lua(nl) == merged.Lua(nl) {
			detail("%s: already up to date", f.path)
			continue
		}
		text := f.text[:f.start] + merged.Lua(nl) + f.text[f.end:]
		added := f.houses.Added(merged)
		if dry {
			log.Infof("%s: would be updated: %s", f.path, added)
			continue
		}
		if err := os.WriteFile(f.path+".bak", []byte(f.text), 0o644); err != nil {
			log.Errf("Backup of %s: %v, not updated", f.path, err)
			continue
		}
		if err := os.WriteFile(f.path, []byte(text), 0o644); err != nil {
			log.Errf("Writing %s: %v", f.path, err)
			continue
		}
		log.Infof("%s: updated: %s (previous one in %s.bak)", f.path, added, filepath.Base(f.path))
	}
}

func main() {
	cli.ArgsHelp = "<WoW WTF folder, or the game folder containing it>"
	cli.MinArgs = 1
	cli.Main()
	dir := flag.Arg(0)
	if *serveFlag || *syncAddr != "" {
		if *serveFlag && *syncAddr != "" {
			log.Fatalf("-serve or -sync, not both")
		}
		id, err := loadIdentity()
		if err != nil {
			log.Fatalf("Key for syncing: %v", err)
		}
		if *serveFlag {
			if len(findFiles(dir)) == 0 {
				log.Fatalf("No Account\\*\\SavedVariables\\AuctionDB.lua under %s", dir)
			}
			logCounts = false
			newServer(id, dir, strings.Split(*peerAddrs, ","), *port).run()
			return
		}
		checkWow()
		files, merged, err := loadLocal(dir)
		if err != nil {
			log.Fatalf("%v", err)
		}
		other, rem, err := syncWith(id, withPort(*syncAddr), merged, "", 0)
		if err != nil {
			log.Fatalf("Sync with %s: %v", *syncAddr, err)
		}
		logHouses(rem.name, other)
		merged.Merge(other)
		merged.Compact(now())
		writeLocal(files, merged, *dryRun)
		return
	}
	if *peerAddrs != "" {
		log.Fatalf("-peer goes with -serve")
	}
	checkWow()
	files, merged, err := loadLocal(dir)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if len(files) < 2 {
		log.Infof("Nothing to merge: %d file(s) with price history", len(files))
		return
	}
	writeLocal(files, merged, *dryRun)
}

func checkWow() {
	if exe := wowRunning(); exe != "" && !*dryRun && !*force {
		log.Fatalf("WoW is running (%s): close the game first (it saves its own copy of the files on exit), or -force",
			exe)
	}
}
