@echo off
set GEOIP_DB_URL=https://<user>:<token>@download.maxmind.com/geoip/databases/GeoLite2-City/download?suffix=tar.gz
set GEOIP_DB_DIR=c:\Windows\TEMP\
set GEOIP_UPDATE_HOUR=12:00
set GEOIP_MAX_IPS=200
set LISTEN_ADDR=0.0.0.0:8080
.\out\http2geoip.exe
