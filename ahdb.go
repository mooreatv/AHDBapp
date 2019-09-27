// Copyright 2019 MooreaTv moorea@ymail.com
// All Rights Reserved
//
// GPLv3 License (which means no commercial integration)
// ask if you need a different License
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"database/sql"

	_ "github.com/go-sql-driver/mysql"

	"github.com/mooreatv/AHDBapp/lua2json"

	"fortio.org/fortio/log"
)

// ScanEntry is 1 auction house scan result
type ScanEntry struct {
	DataFormatVersion int
	Ts                int
	Realm             string
	Faction           string
	Char              string
	Count             int
	ItemDBCount       int
	ItemsCount        int
	Data              string
}

// AHData is toplevel structure produced by ahdbSavedVars2Json
type AHData struct {
	ItemDB map[string]interface{} `json:"itemDB_2"` // most values are strings except _formatVersion_ and _count_
	Ah     []ScanEntry            `json:"ah"`
}

// AuctionEntry is the data we have about each listing
type AuctionEntry struct {
	TimeLeft  int
	ItemCount int
	MinBid    int
	Buyout    int
	CurBid    int
}

// Go version of :extractAuctionData() https://github.com/mooreatv/MoLib/blob/v7.11.01/MoLibAH.lua#L437
func extractAuctionData(auction string) AuctionEntry {
	split := strings.Split(auction, ",")
	splitI := make([]int, len(split))
	for i := range split {
		splitI[i], _ = strconv.Atoi(split[i])
	}
	return AuctionEntry{TimeLeft: splitI[0], ItemCount: splitI[1], MinBid: splitI[2], Buyout: splitI[3], CurBid: splitI[4]}
}

// Go version of :ahDeserializeScanResult() https://github.com/mooreatv/MoLib/blob/v7.11.01/MoLibAH.lua#L375
func ahDeserializeScanResult(data string) (int, int, float64) { //map[string]string {
	// kr empty map
	//kr := map[string]string{}
	log.Debugf("Deserializing data length %d", len(data))
	numItems := 0
	count := 0
	itemEntries := strings.Split(data, " ")
	minPrice := float64(0)
	for itemEntryIdx := range itemEntries {
		itemEntry := itemEntries[itemEntryIdx]
		numItems = numItems + 1
		itemSplit := strings.SplitN(itemEntry, "!", 2)
		if len(itemSplit) != 2 {
			log.Errf("Couldn't split %q into 2 by '!': %#v", itemEntry, itemSplit)
		}
		item := itemSplit[0]
		if item != "i2589" {
			continue
		}
		rest := itemSplit[1]
		// kr[item] = {}
		// entry := kr[item]
		log.Debugf("for %s rest is '%s'", item, rest)
		bySellerEntries := strings.Split(rest, "!")
		for sellerAuctionsIdx := range bySellerEntries {
			sellerAuctions := bySellerEntries[sellerAuctionsIdx]
			sellerAuctionsSplit := strings.SplitN(sellerAuctions, "/", 2)
			seller := sellerAuctionsSplit[0]
			auctions := strings.Split(sellerAuctionsSplit[1], "&")
			log.Debugf("seller %s auctions are '%#v'", seller, auctions)
			// entry[seller] = {}
			for aIdx := range auctions {
				a := extractAuctionData(auctions[aIdx])
				log.Debugf("Auction %#v", a)
				// table.insert(entry[seller], a)
				count += a.ItemCount
				// todo: use fancy fortio stats lib
				if a.Buyout > 0 {
					price := float64(a.Buyout) / float64(a.ItemCount)
					if minPrice > 0 {
						if price < minPrice {
							minPrice = price
						}
					} else {
						minPrice = price
					}
				}
			}
		}
	}
	log.Debugf("Recreated %d items results", numItems)
	// return kr
	return numItems, count, minPrice
}

// SaveScans exports the scan to the DB
func SaveScans(db *sql.DB, scans []ScanEntry) {
	stmt := "INSERT INTO scanmeta (realm, faction, scanner, ts) VALUES(?,?,?,FROM_UNIXTIME(?))"
	stmtIns, err := db.Prepare(stmt)
	if err != nil {
		log.Fatalf("Can't prepare statement for scanmeta insert: %v", err)
	}
	for idx := range scans {
		entry := scans[idx]
		_, err = stmtIns.Exec(entry.Realm, entry.Faction, entry.Char, entry.Ts)
		if err != nil {
			log.Warnf("Skipping duplicate entry: %s %d : %v", entry.Char, entry.Ts, err)
			continue
		}
		numItems, count, price := ahDeserializeScanResult(entry.Data)
		if numItems != entry.ItemsCount {
			log.Errf("Mismatch between deserialization item count %d and saved %d", numItems, entry.ItemsCount)
		}
		fmt.Printf("%d,%d,%q,%q,%d,%d,%d,%d,%.3f\n",
			entry.Ts, entry.DataFormatVersion, entry.Realm, entry.Faction,
			entry.Count, entry.ItemDBCount, entry.ItemsCount, count, price/100.)
	}
}

// SaveItems exports the items to the DB
func SaveItems(db *sql.DB, items map[string]interface{}) {
	count := -1
	err := db.QueryRow("select count(*) from items").Scan(&count)
	if err != nil {
		log.Fatalf("Can't count items: %v", err)
	}
	log.Infof("ItemDB at start has %d items", count)
	tx, err := db.BeginTx(context.Background(), nil)
	/* 	this (also) works to conditionally update only if changed (when passed k,v twice but is slower
		stmt := `
	REPLACE INTO items (id, link) select ?,?
		WHERE (SELECT COUNT(*) FROM items WHERE id=? AND link=?) = 0;
	`
	*/
	stmt := `INSERT INTO items (id, link) VALUES(?,?) ON  DUPLICATE KEY UPDATE 
				ts=IF(VALUES(link) = link, ts, CURRENT_TIMESTAMP),
				link=VALUES(link)`
	stmtIns, err := tx.Prepare(stmt)
	if err != nil {
		log.Fatalf("Can't prepare statement for insert: %v", err)
	}
	defer stmtIns.Close()
	n := 0
	bytes := 0
	start := time.Now()
	for k, vi := range items {
		v, ok := vi.(string)
		if !ok {
			continue
		}
		lk := len(k)
		if lk == 0 {
			log.Warnf("Invalid empty key %v value %v in itemDB", k, v)
			continue
		}
		bytes = bytes + lk + len(v)
		// _, err = stmtIns.Exec(k, v, k, v)
		_, err = stmtIns.Exec(k, v)
		if err != nil {
			log.Fatalf("Can't insert in DB: %v", err)
		}
		n = n + 1
	}
	if err = tx.Commit(); err != nil {
		log.Fatalf("Can't DB commit: %v", err)
	}
	elapsed := time.Since(start)
	log.Infof("Inserted/updated %d items, %.2f Mbytes in MySQL DB in %s", n, float64(bytes)/1024./1024., elapsed)
	if err = db.QueryRow("select count(*) from items").Scan(&count); err != nil {
		log.Fatalf("Can't count items after insert: %v", err)
	}
	log.Infof("ItemDB now has %d items", count)
}

// SaveToDb saves items -> db
func SaveToDb(ahd AHData) {
	user := os.Getenv("MYSQL_USER")
	passwd := os.Getenv("MYSQL_PASSWORD")
	if user == "" {
		user = "root"
	}
	log.Infof("Starting DB save...")
	db, err := sql.Open("mysql", user+":"+passwd+"@/ahdb")
	if err != nil {
		log.Fatalf("Can't open DB: %v", err)
	}
	defer db.Close()
	SaveItems(db, ahd.ItemDB)
	SaveScans(db, ahd.Ah)
}

// Go version of :AHGetAuctionInfoByLink() https://github.com/mooreatv/MoLib/blob/v7.11.01/MoLibAH.lua#L86

var (
	jsonOnly = flag.Bool("jsonOnly", false, "Only do the lua to json conversion")
	// BufferSize flag (needs to be big enough for long packed AH scan lines)
	buffSize = flag.Float64("bufferSize", 16, "Buffer size in Mbytes")
	// Whether to skip the top level
	skipToplevel = flag.Bool("jsonSkipToplevel", false, "Skip top level entity")
	jsonInput    = flag.Bool("jsonInput", false, "Input is already Json and not Lua needing conversion")
)

func main() {
	flag.Parse()
	if *jsonOnly {
		log.Infof("AHDB lua2json started...")
		lua2json.Lua2Json(os.Stdin, os.Stdout, *skipToplevel, *buffSize)
		return
	}
	log.Infof("AHDB parser started...")
	var jR io.Reader
	if *jsonInput {
		jR = os.Stdin
	} else {
		var jW io.Writer
		jR, jW = io.Pipe()
		go func() {
			lua2json.Lua2Json(os.Stdin, jW, true /* need to skip to level */, *buffSize)
		}()
	}
	var ahdb AHData
	jdec := json.NewDecoder(jR)
	jdec.UseNumber()
	if err := jdec.Decode(&ahdb); err != nil {
		log.Fatalf("Unable to unmarshal json result: %v", err)
	}
	if ahdb.ItemDB["_formatVersion_"].(json.Number).String() != "4" {
		log.Errf("Unexpected itemDB format version %v", ahdb.ItemDB["_formatVersion_"])
	}
	ic, _ := ahdb.ItemDB["_count_"].(json.Number).Int64()
	if int(ic) != len(ahdb.ItemDB)-4 {
		log.Errf("Unexpected itemDB count %v vs %d - 4", ahdb.ItemDB["_count_"], len(ahdb.ItemDB))
	}
	log.Infof("Deserialization done, found %d scans. ItemDB has %d items.", len(ahdb.Ah), len(ahdb.ItemDB)-4) // 4 _ meta keys so far
	SaveToDb(ahdb)
}
