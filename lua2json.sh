#! /usr/bin/bash
# Crude Lua table (wow saved variables) to Json "converter"
# (c) 2019 moorea@ymail.com
# See GPLv3 License
# Please ask if you need a different license.
# Credit/mention welcome if you reuse this.
#
# Example usage:
# ./lua2json.sh < test1.lua > test1.json
# Or
# ./lua2json.sh < "C:\Program Files (x86)\World of Warcraft\_classic_\WTF\Account\$ACCT\SavedVariables\AuctionDB.lua" > auctiondb.json
#
# If you don't like regular expressions, don't look further :)
#
echo "{"
# In order, sed expressions:
# Add "" around toplevel array names
# Remove -- comments
# Change = to :
# Change ["foo"] to "foo"
# Change [123] to "123" (keys in json can only be strings)
# Change nil array keys to null
# Then Awk to remove trailing coma and turn list to arrays
# NOTE: anchors/quote boundaries are important to not replace inside the middle of a string value
sed -E -e 's/^([^": }\t]+)/"\1"/' \
    -e "s/ -- .*$//g" \
    -e "s/ = /: /g" \
    -e 's/\["/"/g' \
    -e 's/\"]/"/g' \
    -e 's/^([ \t]*)\[([0-9.]+)\]:/\1"\2":/' \
    -e 's/^([ \t]*)nil,$/\1null,/' \
    | awk '
    BEGIN {startnest=0; inarray=0}
    /},?$/ {gsub(",$", "", l); if (inarray) gsub("}", "]"); inarray=0} 
    /^[^:]+$/ {if (startnest) gsub("{$", "[", l); startnest=0; inarray=1} 
    /: / {inarray=0} 
    /{$/ {startnest=1}
    {if (l) print l; l=$0} 
    END {print l}
    '
echo "}"
