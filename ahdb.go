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
	"strconv"
	"strings"

	"fortio.org/fortio/log"
)

// ScanEntry is 1 auction house scan result
type ScanEntry struct {
	DataFormatVersion int
	Ts                int
	Realm             string
	Faction           string
	Count             int
	ItemDBCount       int
	ItemsCount        int
	Data              string
}

// JSONData is toplevel structure produced by ahdbSavedVars2Json
type JSONData struct {
	Ah []ScanEntry
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

// Go version of :AHGetAuctionInfoByLink() https://github.com/mooreatv/MoLib/blob/v7.11.01/MoLibAH.lua#L86

func main() {
	flag.Parse()
	log.Infof("AHDB parser started...")
	var ahdb JSONData
	if err := json.NewDecoder(os.Stdin).Decode(&ahdb); err != nil {
		log.Fatalf("Unable to unmarshal json result from stdin: %v", err)
	}
	// Will fully parse the data later, for now... for demo
	// "The Price of Linen"
	fmt.Println(`"Ts","Version","Realm","Faction","Count","ItemDBCount","ItemsCount", "LinenCount", "Linen Price per cloth (in silver)"`)
	for idx := range ahdb.Ah {
		entry := ahdb.Ah[idx]
		numItems, count, price := ahDeserializeScanResult(entry.Data)
		if numItems != entry.ItemsCount {
			log.Errf("Mismatch between deserialization item count %d and saved %d", numItems, entry.ItemsCount)
		}
		fmt.Printf("%d,%d,%q,%q,%d,%d,%d,%d,%.3f\n",
			entry.Ts, entry.DataFormatVersion, entry.Realm, entry.Faction,
			entry.Count, entry.ItemDBCount, entry.ItemsCount, count, price/100.)
	}
	log.Infof("Done, found %d scans", len(ahdb.Ah))
}
