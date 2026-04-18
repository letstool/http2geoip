@echo off
go build ^
    -trimpath ^
    -ldflags="-s -w" ^
    -o .\out\http2geoip.exe .\cmd\http2geoip
