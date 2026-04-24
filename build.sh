#!/usr/bin/env bash

rm -f stpd.so

rm -f satoshinet
go build -o satoshinet -ldflags="-s -w"


echo build completed.