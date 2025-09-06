#!/usr/bin/env bash

# ./sync.sh


# rm -f stpd.so

# cd ../transcend/plugin
# go build -buildmode=plugin -o ../../satoshinet/stpd.so main.go
# cd ../../satoshinet

# GOOS=linux GOARCH=amd64 go build -tags=stp_plugin -o satoshinet-linux
go build -tags=stp_plugin -o satoshinet

