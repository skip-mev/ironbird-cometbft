#!/usr/bin/env bash

set -ex

MANIFEST=${1:-networks/200-nodes-dog.toml}
# MANIFEST=networks/7-nodes.toml

LOAD=200 # total load is $LOAD * $CONN
CONN=1
TARGET_REDUNDANCIES=(0.1 0.5 1)
ADJUST_INTERVALS=("500ms" "1000ms" "2000ms")
TEST_DURATION=1800 # 30 min

# run once
make runner
./build/runner -f $MANIFEST -t DO infra create --yes

# Compile and upload runner to CC
./scripts/upload-runner.sh $MANIFEST

# Open Grafana
open "$CC_ADDR:3000"

# Run command from CC
TESTNET_DIR=${MANIFEST%.toml}
CC_ADDR=$(cat $TESTNET_DIR/.cc-ip)
ssh_cc() {
    ssh -o LogLevel=ERROR -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o GlobalKnownHostsFile=/dev/null root@$CC_ADDR -t "$@"
}

run_instance() {
    # Wait a minimum of T seconds between runs; in the meantime, setup and start next testnet.
    sleep 90 & # sleep in background
    ./build/runner -f $MANIFEST -t DO setup --clean --le=false --keep-address-book
    ./build/runner -f $MANIFEST -t DO start
    echo Waiting...
    wait # until sleeping has finished
    
    # Keep laptop awake while loading (only MacOS)
    caffeinate -u -t $TEST_DURATION &

    # Perturb nodes in parallel.
    sleep 30 && gtimeout $TEST_DURATION ./build/runner -f $MANIFEST -t DO perturb &
    
    # load txs from CC
    echo Loading transactions during $TEST_DURATION seconds...
    ssh_cc ./build/runner -f $MANIFEST -t DO load -r $LOAD -c $CONN -T $TEST_DURATION --internal-ip --duplicate-num-nodes=100 > /dev/null 2>&1 &
    ssh_cc ./build/runner -f $MANIFEST -t DO load -r $LOAD -c $CONN -T $TEST_DURATION --internal-ip
    
    ./build/runner -f $MANIFEST -t DO stop
}

# Baseline (DOG disabled)
sed -i.bak -e "s/.*enable_dog_protocol.*/\t\"mempool.enable_dog_protocol = false\",/g" $MANIFEST 
run_instance
sleep 60

# DOG enabled
sed -i.bak -e "s/.*enable_dog_protocol.*/\t\"mempool.enable_dog_protocol = true\",/g" $MANIFEST 
for TARGET in ${TARGET_REDUNDANCIES[@]}; do
    # change target manifest
    sed -i.bak -e "s/.*target_redundancy .*/\t\"mempool.target_redundancy = $TARGET\",/g" $MANIFEST 
    for INTERVAL in ${ADJUST_INTERVALS[@]}; do
        sed -i.bak -e "s/.*adjust_redundancy_interval .*/\t\"mempool.adjust_redundancy_interval = $INTERVAL\",/g" $MANIFEST 
        echo 🟢 TARGET: $TARGET, INTERVAL: $INTERVAL
        run_instance
    done
    sleep 60
done

# TODO: download prometheus data (it may be huge)

# Remove CC from terraform's state, so it does not get destroyed later.
cd terraform
terraform state rm digitalocean_droplet.cc
cd ..

## Be careful! This will destroy CC with the prometheus data!
./build/runner -f $MANIFEST -t DO clean
