#!/usr/bin/env bash

./build.sh

if [ $# -eq 0 ]; then
  nohup ./satoshinet --homedir ./data --txindex 1>/dev/null 2>./nohup.log &
  disown
else
  if [ "$1" = "off" ]; then
    ./satoshinet --homedir ./data --txindex
  else
    echo "unknown parameter"
  fi
fi

