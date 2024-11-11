#!/usr/bin/env bash

MANIFEST_FILE=${1:-networks/200-nodes-dog.toml}
TESTNET_DIR=${MANIFEST_FILE%.toml}
CC_ADDR=$(cat $TESTNET_DIR/.cc-ip)
INFRA_DATA_FILE=$TESTNET_DIR/infra-data.json

# Build runner binary.
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/runner-linux ./runner

# Copy to CC files needed to execute runner
ssh root@${CC_ADDR} -t "mkdir -p /root/$TESTNET_DIR && mkdir -p /root/build && mkdir -p monitoring/config-grafana"
scp -C build/runner-linux root@${CC_ADDR}:/root/build/runner
scp -C $MANIFEST_FILE root@${CC_ADDR}:/root/$(dirname $TESTNET_DIR)
scp -C $INFRA_DATA_FILE root@${CC_ADDR}:/root/$TESTNET_DIR
scp -C -r monitoring/config-grafana/ root@${CC_ADDR}:/root/monitoring/config-grafana
scp -C -r monitoring/prometheus.yml root@${CC_ADDR}:/root/monitoring

# Copy other files
scp -C ./scripts/test-dog.sh root@${CC_ADDR}:/root
