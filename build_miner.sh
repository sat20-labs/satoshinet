#!/usr/bin/env bash

rm -f wallet.so

cd ../sat20wallet/sdk/plugin
go build -buildmode=plugin -ldflags="-s -w" -o ../../../satoshinet/wallet.so main.go
cd ../../../satoshinet

rm -f satoshinet_miner
go build -tags wallet_plugin -o satoshinet_miner -ldflags="-s -w"

echo build completed.