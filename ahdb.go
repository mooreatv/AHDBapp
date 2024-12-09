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
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"database/sql"

	_ "github.com/go-sql-driver/mysql"

	"github.com/mooreatv/AHDBapp/lua2json"

	"fortio.org/log"
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

// ItemEntry is what the raw link gets parsed into
type ItemEntry struct {
	ID         string
	ShortID    int
	Name       string
	SellPrice  int
	StackCount int
	ClassID    int
	SubClassID int
	Rarity     int
	MinLevel   int
	Link       string
	Olink      string
}

// AuctionEntry is the data we have about each listing
type AuctionEntry struct {
	TimeLeft  int
	ItemCount int
	MinBid    int
	Buyout    int
	CurBid    int
}

// '5000,1,1,0,1,0|cffffffff|Hitem:14046::::::::5:::::::|h[Runecloth Bag]|h|r'
var itemRegex = regexp.MustCompile(`^([0-9]+),([0-9]+),([0-9]+),([0-9]+),([0-9]+),([0-9]+)(\|[^|]+\|Hitem:([0-9]+)[^|]+\|h\[([^]]+)\]\|h\|r)$`)

func extractItemInfo(id, olink string) *ItemEntry {
	e := ItemEntry{ID: id, Olink: olink}
	if len(olink) == 0 || olink[0] == '|' {
		return &e
	}
	res := itemRegex.FindStringSubmatch(olink)
	if res == nil {
		log.Critf("Unexpected mismatch for item %q", olink)
		return &e
	}
	e.SellPrice, _ = strconv.Atoi(res[1])
	e.StackCount, _ = strconv.Atoi(res[2])
	e.ClassID, _ = strconv.Atoi(res[3])
	e.SubClassID, _ = strconv.Atoi(res[4])
	e.Rarity, _ = strconv.Atoi(res[5])
	e.MinLevel, _ = strconv.Atoi(res[6])
	e.ShortID, _ = strconv.Atoi(res[8])
	e.Link = res[7]
	e.Name = res[9]
	return &e
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
func ahDeserializeScanResult(stmt *sql.Stmt, scan ScanEntry, scanID int64) {
	data := scan.Data
	log.LogVf("Deserializing data length %d", len(data))
	numItems := 0
	opCount := 0
	itemEntries := strings.Split(data, " ")
	for itemEntryIdx := range itemEntries {
		itemEntry := itemEntries[itemEntryIdx]
		numItems = numItems + 1
		itemSplit := strings.SplitN(itemEntry, "!", 2)
		if len(itemSplit) != 2 {
			log.Errf("Couldn't split %q into 2 by '!': %#v", itemEntry, itemSplit)
		}
		item := itemSplit[0]
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
				opCount = opCount + 1
				// scanId, itemId, ts, seller, timeLeft, itemCount, minBid, buyout, curBid)
				if stmt != nil {
					_, err := stmt.Exec(scanID, item, scan.Ts, seller, a.TimeLeft, a.ItemCount, a.MinBid, a.Buyout, a.CurBid)
					if err != nil {
						log.Fatalf("Can't insert in DB op#%d for scanid %d: %v", opCount, scanID, err)
					}
				}
			}
		}
	}
	log.Infof("Inserted %d auctions for %d items for scanId %d", opCount, numItems, scanID)
	if numItems != scan.ItemsCount {
		log.Errf("Mismatch between deserialization item count %d and saved %d", numItems, scan.ItemsCount)
	}
}

// SaveScans exports the scan to the DB
func SaveScans(db *sql.DB, scans []ScanEntry) {
	stmtMeta := "INSERT INTO scanmeta (realm, faction, scanner, ts) VALUES(?,?,?,FROM_UNIXTIME(?))"
	var stmtMetaIns *sql.Stmt
	var err error
	if db != nil {
		stmtMetaIns, err = db.Prepare(stmtMeta)
		if err != nil {
			log.Fatalf("Can't prepare statement for scanmeta insert: %v", err)
		}
	}
	stmtAuction := `
INSERT INTO auctions (scanId, itemId, ts, seller, timeLeft, itemCount, minBid, buyout, curBid)
			 VALUES (?,?, FROM_UNIXTIME(?), ?,   ?,         ?,        ?,      ?,      ?)
`
	for idx := range scans {
		entry := scans[idx]
		if db == nil {
			ahDeserializeScanResult(nil, entry, -1)
			continue
		}
		res, err := stmtMetaIns.Exec(entry.Realm, entry.Faction, entry.Char, entry.Ts)
		if err != nil {
			log.Infof("Skipping duplicate entry: %s %d : %v", entry.Char, entry.Ts, err)
			continue
		}
		scanID := int64(-1)
		if scanID, err = res.LastInsertId(); err != nil {
			log.Fatalf("Unable to get id after scanmeta insert: %v", err)
		}
		log.LogVf("Inserted successfully scan meta id %d", scanID)
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			log.Fatalf("Can't start a transaction: %v", err)
		}
		stmtIns, err := tx.Prepare(stmtAuction)
		if err != nil {
			log.Fatalf("Can't prepare statement for insert: %v", err)
		}
		ahDeserializeScanResult(stmtIns, entry, scanID)
		if err = tx.Commit(); err != nil {
			log.Fatalf("Can't DB commit auction for scan %d: %v", scanID, err)
		}
	}
	//log.Infof("After big commit of all the scans...")
}

// SaveItems exports the items to the DB
func SaveItems(db *sql.DB, items map[string]interface{}) {
	count := -1
	var stmtIns *sql.Stmt
	var tx *sql.Tx
	var err error
	if db != nil {
		err := db.QueryRow("select count(*) from items").Scan(&count)
		if err != nil {
			log.Fatalf("Can't count items: %v", err)
		}
		log.Infof("ItemDB at start has %d items", count)
		tx, err = db.BeginTx(context.Background(), nil)
		if err != nil {
			log.Fatalf("Can't start a transaction: %v", err)
		}
		/* 	this (also) works to conditionally update only if changed (when passed k,v twice but is slower
			stmt := `
		REPLACE INTO items (id, link) select ?,?
			WHERE (SELECT COUNT(*) FROM items WHERE id=? AND link=?) = 0;
		`
		*/
		stmt := `INSERT INTO items (id, shortid, name, sellprice, stackcount, classid, subclassid, rarity, minlevel, link, olink)
							VALUES(?  , ?      , ?   , ?        , ?         , ?      , ?          , ?     , ?       , ?   , ?)
							ON  DUPLICATE KEY UPDATE
				ts=IF(VALUES(olink) = olink, ts, CURRENT_TIMESTAMP),
				shortid=VALUES(shortid),
				name=VALUES(name),
				sellprice=VALUES(sellprice),
				stackcount=VALUES(stackcount),
				classid=VALUES(classid),
				subclassid=VALUES(subclassid),
				rarity=VALUES(rarity),
				minlevel=VALUES(minlevel),
				link=VALUES(link),
				olink=VALUES(olink)`
		stmtIns, err = tx.Prepare(stmt)
		if err != nil {
			log.Fatalf("Can't prepare statement for insert: %v", err)
		}
		defer stmtIns.Close()
	}
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
		if db != nil {
			// _, err = stmtIns.Exec(k, v, k, v)
			e := extractItemInfo(k, v)
			_, err = stmtIns.Exec(e.ID, e.ShortID, e.Name, e.SellPrice, e.StackCount, e.ClassID, e.SubClassID, e.Rarity, e.MinLevel, e.Link, e.Olink)
			if err != nil {
				log.Fatalf("Can't insert in DB: %v", err)
			}
		}
		n = n + 1
	}
	if db != nil {
		if err = tx.Commit(); err != nil {
			log.Fatalf("Can't DB commit: %v", err)
		}
		elapsed := time.Since(start)
		log.Infof("Inserted/updated %d items, %.2f Mbytes in MySQL DB in %s", n, float64(bytes)/1024./1024., elapsed)
		if err = db.QueryRow("select count(*) from items").Scan(&count); err != nil {
			log.Fatalf("Can't count items after insert: %v", err)
		}
		log.Infof("ItemDB now has %d items", count)
	} else {
		log.Infof("Parsed %d items, %.2f Mbytes in %s", n, float64(bytes)/1024./1024., time.Since(start))
	}
}

// SaveToDb saves items -> db
func SaveToDb(ahd AHData, noDB bool) {
	user := os.Getenv("MYSQL_USER")
	passwd := os.Getenv("MYSQL_PASSWORD")
	connect := os.Getenv("MYSQL_CONNECTION_INFO")
	if user == "" {
		user = "root"
	}
	if connect == "" {
		connect = "tcp(:3306)"
	}
	log.Infof("Starting DB save with noDB=%v ...", noDB)
	var db *sql.DB
	var err error
	if !noDB {
		db, err = sql.Open("mysql", user+":"+passwd+"@"+connect+"/ahdb")
		if err != nil {
			log.Fatalf("Can't open DB: %v", err)
		}
		defer db.Close()
	}
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
	noDB         = flag.Bool("nodb", false, "Don't try to connect to a live DB when the flag is passed")
)

func main() {
	flag.Parse()
	if *jsonOnly {
		log.Infof("AHDB lua2json started (reading from stdin)...")
		lua2json.Lua2Json(os.Stdin, os.Stdout, *skipToplevel, *buffSize)
		return
	}
	log.Infof("AHDB parser started (reading from stdin)...")
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
		log.Fatalf("Unable to unmarshal json result: %#v", err)
	}
	fv := ahdb.ItemDB["_formatVersion_"]
	if fv == nil || fv.(json.Number).String() != "5" {
		log.Errf("Unexpected itemDB format version %v", ahdb.ItemDB["_formatVersion_"])
		os.Exit(1)
	}
	ic, _ := ahdb.ItemDB["_count_"].(json.Number).Int64()
	if int(ic) != len(ahdb.ItemDB)-5 {
		log.Errf("Unexpected itemDB count %v vs %d - 5", ahdb.ItemDB["_count_"], len(ahdb.ItemDB))
	}
	log.Infof("Deserialization done, found %d scans. ItemDB has %d items.", len(ahdb.Ah), len(ahdb.ItemDB)-5) // 4 _ meta keys so far
	SaveToDb(ahdb, *noDB)
}
