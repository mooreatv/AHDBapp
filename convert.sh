#! /usr/bin/bash
# Crude Lua table (wow saved variables) to Json "converter"
# (c) 2019 moorea@ymail.com
# If you don't like regular expressions, don't look further :)

echo "{"
# In order, sed expressions:
# Add "" around toplevel array names
# Remove -- comments
# Change = to :
# Change ["foo"] to "foo"
# Then Awk to remove trailing coma and turn list to arrays
sed -E -e 's/^([^": }\t]+)/"\1"/' \
    -e "s/ -- .*$//g" \
    -e "s/ = /: /g" \
    -e 's/\["/"/g' \
    -e 's/\"]/"/g' | \
    awk '
    BEGIN {startnest=0; inarray=0}
    /},?$/ {gsub(",$", "", l); if (inarray) gsub("}", "]")} 
    /^[^:]+$/ {if (startnest) gsub("{$", "[", l); startnest=0; inarray=1} 
    /:/ {inarray=0} 
    /{$/ {startnest=1}
    {if (l) print l; l=$0} 
    END {print l}
    '
echo "}"
