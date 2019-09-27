# schema

create database if not exists ahdb;
use ahdb;

# drop table if exists items;
create table if not exists items (
    id  VARCHAR(32) NOT NULL,
    link VARCHAR(255) NOT NULL,
    PRIMARY KEY (id)
)
