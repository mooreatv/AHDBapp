# AHDB schema
# (c) 2019 MooreaTv <moorea@ymail.com> All Rights Reserved

create database if not exists ahdb;
use ahdb;

drop table if exists items;
create table if not exists items (
    id  VARCHAR(32) NOT NULL,    # in classic, longest so far is 15
    shortid INT NOT NULL,
    name VARCHAR(128) NOT NULL,
    SellPrice INT NOT NULL,
    StackCount INT NOT NULL,
    ClassID INT NOT NULL,
    SubClassID INT NOT NULL,
    Rarity INT NOT NULL,
    MinLevel INT NOT NULL,
    link VARCHAR(255) NOT NULL,  # in classic, longest so far is 104
    olink VARCHAR(255) NOT NULL,  # raw version from addon, includes the above encoded
    ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id)
);

drop table if exists scanmeta;
create table if not exists scanmeta (
    id INT AUTO_INCREMENT NOT NULL,
    realm VARCHAR(16) NOT NULL,
    faction ENUM('Neutral', 'Alliance', 'Horde') NOT NULL,
    scanner VARCHAR(64) NOT NULL, # who scanned
    ts TIMESTAMP NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT unique_scan UNIQUE (ts, scanner)
);

drop table if exists auctions;
create table if not exists auctions (
    scanId INT NOT NULL REFERENCES scanmeta(id),
    itemId VARCHAR(32) NOT NULL REFERENCES items(id),
    ts TIMESTAMP NOT NULL, # denormalized, same as scanmeta's
    seller VARCHAR(64), # denormalized
    timeLeft TINYINT NOT NULL, # enum for time left: 1 is short, 2 medium...
    itemCount SMALLINT NOT NULL, # auction's stack size
    minBid INT NOT NULL, # initial/minbid value (in copper)
    buyout INT NOT NULL, # we use 0 for no buyout specified, like the wow api
    curBid INT NOT NULL # like wise 0 for no bid
);

drop table if exists auctions_bulk;
create table auctions_bulk (
    scanId INT NOT NULL,
    itemId VARCHAR(32) NOT NULL,
    ts TIMESTAMP NOT NULL,
    seller VARCHAR(64),
    timeLeft TINYINT NOT NULL,
    itemCount SMALLINT NOT NULL,
    minBid INT NOT NULL,
    buyout INT NOT NULL,
    curBid INT NOT NULL
);
