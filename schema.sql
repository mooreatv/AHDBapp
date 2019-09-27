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
)

# create table if not exists auctions