#!/bin/bash

curl -X POST http://localhost:8080/api/v1/geoip \
     -H "Content-Type: application/json" \
     -d '{
	     "lang": "fr",
	     "ip":"31.37.67.38"
     }' | jq

curl -X POST http://localhost:8080/api/v1/geoip \
     -H "Content-Type: application/json" \
     -d '{
	     "lang": "fr",
	     "ips":["31.37.67.38","8.8.4.4","127.0.0.1"]
     }' | jq
