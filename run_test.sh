#!/usr/bin/env bash

export ROOT_PATH=`dirname $0 |pwd`
echo ${ROOT_PATH}
cd ${ROOT_PATH}

go test -v -count=1 ./worker
go test -v -count=1 ./controller
