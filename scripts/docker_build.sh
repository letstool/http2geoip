#!/bin/bash

IMAGE_TAG=letstool/http2geoip:latest

docker build \
	-t "$IMAGE_TAG" \
       -f build/Dockerfile \
       .
