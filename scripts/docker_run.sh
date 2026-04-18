#!/bin/bash

docker run -it --rm -p 8080:8080 -e GEOIP_DB_URL=https://<user>:<token>@download.maxmind.com/geoip/databases/GeoLite2-City/download?suffix=tar.gz -e GEOIP_DB_DIR=/data -e GEOIP_UPDATE_HOUR=12:00 -e GEOIP_LISTEN_ADDR=0.0.0.0:8080 -e GEOIP_MAX_IPS=200 -v /etc/localtime:/etc/localtime:ro -v ./db:/data:rw letstool/http2geoip:latest
