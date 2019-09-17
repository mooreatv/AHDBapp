# AHDBapp
The out of game parser, processor, uploader for AHDB wow addon

WIP, works with https://github.com/mooreatv/AuctionDB

Already includes a pretty cool
- [lua2json.sh](lua2json.sh) Lua (tables/WoW saved variables) to JSON converter
- [lua2json golang package](lua2json/) Golang version of the sed+awk script hack


## Getting started

You used to need
- bash and some basic unix utilities; easiest way to get those is through git bash that comes with https://git-scm.com/downloads
- golang https://golang.org/dl/
- then type `go get github.com/mooreatv/AHDBapp` it will download into `~/go/src/github/mooreatv/AHDBapp` 
- ./ahdbSavedVars2Json.sh YOURWOWACCOUNT
- go run ahdb.go < auctiondb.json > auctiondb.csv

But now you just need golang as `go run ahdb.go` can process the saved variables directly
