#!/usr/bin/env bash

rm -f stpd.so

cd ../transcend/plugin
STPD_TAGS=()
if [ "${STP_MAINNET:-}" = "1" ]; then
    STPD_TAGS+=("stp_mainnet")
fi
if [ "${STP_TESTNET_FAULT:-}" = "1" ]; then
    STPD_TAGS+=("stp_testnet_fault")
fi

if [ ${#STPD_TAGS[@]} -gt 0 ]; then
    STPD_TAG_LIST=$(IFS=, ; echo "${STPD_TAGS[*]}")
    go build -tags "$STPD_TAG_LIST" -buildmode=plugin -ldflags="-s -w" -o ../../satoshinet/stpd.so main.go
else
    go build -buildmode=plugin -ldflags="-s -w" -o ../../satoshinet/stpd.so main.go
fi
cd ../../satoshinet

rm -f satoshinet_core
go build -tags stp_plugin -o satoshinet_core -ldflags="-s -w"


echo build completed.
