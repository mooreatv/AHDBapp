# AHDBapp
The out of game parser, processor, uploader for AHDB wow addon

WIP, works with https://github.com/mooreatv/AuctionDB

Already includes a pretty cool
- [lua2json.sh](lua2json.sh) Lua (tables/WoW saved variables) to JSON converter
- [lua2json golang package](lua2json/) Golang version of the sed+awk script hack


## Merging price history between your WoW accounts and computers (WoW Forever)

`ahdbmerge` gives every WoW account of a game install the AHDB price history all of them scanned (saved data is per
account). With the game closed:

```
go install github.com/mooreatv/AHDBapp/cmd/ahdbmerge@latest
ahdbmerge "C:\Program Files (x86)\World of Warcraft\_classic_beta_\WTF"
```

Only the price history part of each `Account\*\SavedVariables\AuctionDB.lua` is rewritten; the previous file is kept
as `AuctionDB.lua.bak`. `-n` shows what would be merged without writing. While playing, AHDB can also ask your
other accounts' characters for prices directly (`/ahdb trust`).

### Between your computers

Keep `ahdbmerge -serve` running on each computer (allow it through the Windows firewall when asked), and tell one of
them where the other is (its name or IP address):

```
ahdbmerge -serve -peer OTHERCOMPUTER "C:\Program Files (x86)\World of Warcraft\_classic_beta_\WTF"
```

```
ahdbmerge -serve "C:\Program Files (x86)\World of Warcraft\_classic_beta_\WTF"
```

They sync whenever there is something new: at start, and when WoW saves its files (logout, `/reload`, exit). Each
computer's files get the price history of all the accounts of all the computers, while its game is closed (WoW would
overwrite them on exit), else when it exits. So: close the game on one computer, start it on the other, prices are
there.

More than 2 computers: start the others with `-peer` the same one (the hub); what one sends reaches the others
through it. Without keeping it running, `ahdbmerge -sync COMPUTER <WTF>` (game closed) syncs once with a computer
running `-serve`.

The first time, each side asks to allow the other one, showing both keys so you can check they match (do it in a
terminal on both). Allowed computers are remembered, with where they answer, in `%AppData%\ahdbmerge\trusted.json`
(remove one to forget it). `-port` changes the port (8642).

## Getting started

ahdb.go now reads lua and writes to a MySql DB directly (needed schema is in schema.sql)

Environment variables to control the access to the DB:
- optional `MYSQL_USER` (defaults to root)
- `MYSQL_PASSWORD`
- optional `MYSQL_CONNECTION_INFO` (defaults to tcp to 3306)

You need:
- golang https://golang.org/dl/ (on windows you may still need to get git (https://git-scm.com/downloads)
- then type `go install github.com/mooreatv/AHDBapp@latest` it will download and build and install the binary in ~/go/bin
- then
  - On windows `go\bin\AHDBapp.exe < "c:\Program Files (x86)\World of Warcraft\_classic_era_\WTF\Account\YOURACCOUNT\SavedVariables\AuctionDB.lua"`
  - On unix/mac `~/go/bin/AHDBapp < ...path_to_.../SavedVariables/AuctionDB.lua`

### old instructions
You used to need/do
- golang https://golang.org/dl/
- then type `go get github.com/mooreatv/AHDBapp` it will download the source into `~/go/src/github/mooreatv/AHDBapp` (and build the binary in ~/go/bin)
- then
  - On windows `go\bin\AHDBapp.exe < "c:\Program Files (x86)\World of Warcraft\_classic_\WTF\Account\YOURACCOUNT\SavedVariables\AuctionDB.lua" > data.csv`
  - On unix/mac `~/go/bin/AHDBapp < ...path_to_.../SavedVariables/AuctionDB.lua > data.csv`

### even older instructions
You used to need
- bash and some basic unix utilities; easiest way to get those is through git bash that comes with https://git-scm.com/downloads
- golang https://golang.org/dl/
- `go get github.com/mooreatv/AHDBapp` it will download into `~/go/src/github/mooreatv/AHDBapp`
- ./ahdbSavedVars2Json.sh YOURWOWACCOUNT
- go run ahdb.go < auctiondb.json > auctiondb.csv

But now you just need golang as `go run ahdb.go` can process the saved variables directly as above
