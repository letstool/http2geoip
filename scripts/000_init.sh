#!/bin/bash

# 0. Create module
go mod init letstool/http2geoip

# 1.  Install the dependency
go get github.com/oschwald/geoip2-golang

go mod tidy
