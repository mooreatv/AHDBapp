// Copyright 2019 MooreaTv moorea@ymail.com
// All Rights Reserved
//
// GPLv3 License (which means no commercial integration)
// ask if you need a different License
//

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"fortio.org/fortio/log"
)

// ScanEntry is 1 auction house scan result
type ScanEntry struct {
	DataFormatVersion int
	Ts                int
	Count             int
	ItemDBCount       int
	ItemsCount        int
}

// JSONData is toplevel structure produced by ahdbSavedVars2Json
type JSONData struct {
	Ah []ScanEntry
}

func main() {
	flag.Parse()
	log.Infof("AHDB parser started...")
	var ahdb JSONData
	if err := json.NewDecoder(os.Stdin).Decode(&ahdb); err != nil {
		log.Fatalf("Unable to unmarshal json result from stdin: %v", err)
	}
	fmt.Println(`"Version","Ts","Count","ItemDBCount","ItemsCount"`)
	for idx := range ahdb.Ah {
		entry := ahdb.Ah[idx]
		fmt.Printf("%d,%d,%d,%d,%d\n", entry.DataFormatVersion, entry.Ts, entry.Count, entry.ItemDBCount, entry.ItemsCount)
	}
	log.Infof("Done, found %d scans", len(ahdb.Ah))
}
