#!/bin/bash

go build \
    -trimpath \
    -ldflags="-extldflags -static -s -w" \
    -o ./out/http2geoip ./cmd/http2geoip
