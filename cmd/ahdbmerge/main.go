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
package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"fortio.org/cli"
	"fortio.org/log"
)

const savedName = "AuctionDBForeverSaved"

var (
	force  = flag.Bool("force", false, "merge even when WoW seems to be running (it would overwrite the files on exit)")
	dryRun = flag.Bool("n", false, "only show what would be merged, don't write anything")
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

func main() {
	cli.ArgsHelp = "<WoW WTF folder, or the game folder containing it>"
	cli.MinArgs = 1
	cli.Main()
	dir := flag.Arg(0)
	paths := findFiles(dir)
	if len(paths) == 0 {
		log.Fatalf("No Account\\*\\SavedVariables\\AuctionDB.lua under %s", dir)
	}
	if exe := wowRunning(); exe != "" && !*dryRun && !*force {
		log.Fatalf("WoW is running (%s): close the game first (it saves its own copy of the files on exit), or -force",
			exe)
	}
	merged := Houses{}
	var files []*savedFile
	for _, p := range paths {
		f, err := readFile(p)
		if err != nil {
			log.Fatalf("%s: %v", p, err)
		}
		if f.houses == nil {
			log.Infof("%s: no price history, skipped", p)
			continue
		}
		for name, h := range f.houses {
			items, recent, daily, scans := h.Counts()
			log.Infof("%s: %s AH: %d items, %d recent scan prices, %d daily, %d scans", p, name, items, recent, daily,
				scans)
		}
		merged.Merge(f.houses)
		files = append(files, f)
	}
	if len(files) < 2 {
		log.Infof("Nothing to merge: %d file(s) with price history", len(files))
		return
	}
	for name, h := range merged {
		items, recent, daily, scans := h.Counts()
		log.Infof("Merged %s AH: %d items, %d recent scan prices, %d daily, %d scans", name, items, recent, daily, scans)
	}
	for _, f := range files {
		nl := "\n"
		if strings.Contains(f.text, "\r\n") {
			nl = "\r\n"
		}
		if f.houses.Lua(nl) == merged.Lua(nl) {
			log.Infof("%s: already up to date", f.path)
			continue
		}
		text := f.text[:f.start] + merged.Lua(nl) + f.text[f.end:]
		if *dryRun {
			log.Infof("%s: would be updated", f.path)
			continue
		}
		if err := os.WriteFile(f.path+".bak", []byte(f.text), 0o644); err != nil {
			log.Fatalf("Backup of %s: %v", f.path, err)
		}
		if err := os.WriteFile(f.path, []byte(text), 0o644); err != nil {
			log.Fatalf("Writing %s: %v", f.path, err)
		}
		log.Infof("%s: updated (previous one in %s.bak)", f.path, filepath.Base(f.path))
	}
}
