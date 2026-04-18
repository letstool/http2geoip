#!/bin/bash

export GEOIP_DB_URL=https://<user>:<token>@download.maxmind.com/geoip/databases/GeoLite2-City/download?suffix=tar.gz
export GEOIP_DB_DIR=/data
export GEOIP_UPDATE_HOUR=12:00
export GEOIP_MAX_IPS=200
export LISTEN_ADDR=0.0.0.0:8080

./out/http2geoip
