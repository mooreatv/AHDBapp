# AHDB schema
# (c) 2019 MooreaTv <moorea@ymail.com> All Rights Reserved

create database if not exists ahdb;
use ahdb;

# drop table if exists items;
create table if not exists items (
    id  VARCHAR(32) NOT NULL,    # in classic, longest so far is 15
    link VARCHAR(255) NOT NULL,  # in classic, longest so far is 104
    ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id)
);

# drop table if exists scanmeta
create table if not exists scanmeta (
	id INT AUTO_INCREMENT,
    realm VARCHAR(16) NOT NULL,
    faction ENUM('Neutral', 'Alliance', 'Horde') NOT NULL,
    scanner VARCHAR(64) NOT NULL, -- who scanned
    ts TIMESTAMP,
	PRIMARY KEY (`id`),
	CONSTRAINT unique_scan UNIQUE (ts, scanner)
);

create table if not exists auctions (
	scanId INT NOT NULL REFERENCES scanmeta(id),
	itemId VARCHAR(32) NOT NULL REFERENCES items(id)
);