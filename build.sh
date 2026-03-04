#!/usr/bin/env bash

rm -f stpd.so

cd ../transcend/plugin
go build -buildmode=plugin -ldflags="-s -w" -o ../../satoshinet/stpd.so main.go
cd ../../satoshinet

rm -f satoshinet_core
go build -tags stp_plugin -o satoshinet_core -ldflags="-s -w"


echo build completed.