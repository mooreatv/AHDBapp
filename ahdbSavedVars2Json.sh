#! /usr/bin/bash
# Extracts Json from AHDB addon SavedVariables
# (c) 2019 moorea@ymail.com
# See GPLv3 License
# Please ask if you need a different license.
# Credit/mention welcome if you reuse this.
#
wowclassic=${WOWCLASSIC:-"C:\\Program Files (x86)\\World of Warcraft\\_classic_\\WTF\\Account\\"}
function help() {
  echo "$0 usage:"
  echo "$0 WowAccountName > auctiondb.json"
  echo "set the WOWCLASSIC env if '$wowclassic' isn't right for your setup"
  exit 1
}

if [[ $# -ne 1 ]]; then
  help
fi

ACCT=$1

FP="$wowclassic\\$ACCT\\SavedVariables\\AuctionDB.lua"

if [[ ! -f "$FP" ]]; then
    echo "Can't find AuctionDB saved var in $wowclassic for account $1"
    ls "$wowclassic"
    help
fi
# Skip toplevel/1 level of structure:
cat "$FP" | $(dirname $0)/lua2json.sh | grep -v -E '^("AuctionDBSaved":|})' > auctiondb.json
echo "}" >> auctiondb.json
