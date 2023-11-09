#!/bin/bash

####################### Config variables & functions #######################
# Common
VALIDATOR="validator"
NODE_IP="localhost"

# Main configs
MAIN_CHAIN_ID="andromeda"
MAIN_MONIKER="andromeda"
MAIN_HOME="$HOME/.andromedad"
MAIN_BINARY="andromedad --home=$MAIN_HOME"
MAIN_TX_FLAGS="--keyring-backend test --chain-id $MAIN_CHAIN_ID --from $VALIDATOR -y --fees=1000uhuahua"
MAIN_RPC_LADDR="$NODE_IP:26657"
MAIN_P2P_LADDR="$NODE_IP:26656"
MAIN_GRPC_ADDR="$NODE_IP:9090"

# Counter configs
COUNTER_CHAIN_ID="terra"
COUNTER_MONIKER="terra"
COUNTER_HOME="$HOME/.terrad"
COUNTER_BINARY="terrad --home=$COUNTER_HOME"
COUNTER_TX_FLAGS="--keyring-backend test --chain-id $COUNTER_CHAIN_ID --from $VALIDATOR -y --fees=1000uluna"
COUNTER_RPC_LADDR="$NODE_IP:26658"
COUNTER_P2P_LADDR="$NODE_IP:26646"
COUNTER_GRPC_ADDR="$NODE_IP:9091"


####################### Initializate chains #######################
echo "==============> Starting chain initialization...<=============="
# Clean start
killall $MAIN_BINARY &> /dev/null || true
killall $COUNTER_BINARY &> /dev/null || true
killall rly 2> /dev/null || true
rm -rf $MAIN_HOME
rm -rf $COUNTER_HOME
rm -rf ./test/relayer/keys
rm -rf ./test/logs
mkdir ./test/logs
cp ./test/relayer/config/config_temp.yaml ./test/relayer/config/config.yaml

# Main chain init
$MAIN_BINARY init --chain-id $MAIN_CHAIN_ID $MAIN_MONIKER
sed -i '' 's/"voting_period": "172800s"/"voting_period": "30s"/g' $MAIN_HOME/config/genesis.json
sed -i '' 's/"max_deposit_period": "172800s"/"max_deposit_period": "30s"/g' $MAIN_HOME/config/genesis.json
sed -i '' 's/stake/uhuahua/g' $MAIN_HOME/config/genesis.json
sed -i -E "s|keyring-backend = \".*\"|keyring-backend = \"test\"|g" $MAIN_HOME/config/client.toml
sed -i -E "s|minimum-gas-prices = \".*\"|minimum-gas-prices = \"0uhuahua\"|g" $MAIN_HOME/config/app.toml

$MAIN_BINARY keys add $VALIDATOR --keyring-backend=test
$MAIN_BINARY genesis add-genesis-account $($MAIN_BINARY keys show $VALIDATOR --keyring-backend=test -a) 1000000000000000000uhuahua
$MAIN_BINARY genesis gentx validator 10000000000uhuahua --keyring-backend=test --chain-id=$MAIN_CHAIN_ID
$MAIN_BINARY genesis collect-gentxs 

# Counter chain init
$COUNTER_BINARY init --chain-id $COUNTER_CHAIN_ID $COUNTER_MONIKER
sed -i '' 's/"voting_period": "172800s"/"voting_period": "30s"/g' $COUNTER_HOME/config/genesis.json
sed -i '' 's/"max_deposit_period": "172800s"/"max_deposit_period": "30s"/g' $COUNTER_HOME/config/genesis.json
sed -i '' 's/stake/uluna/g' $COUNTER_HOME/config/genesis.json
sed -i -E "s|keyring-backend = \".*\"|keyring-backend = \"test\"|g" $COUNTER_HOME/config/client.toml
sed -i -E "s|minimum-gas-prices = \".*\"|minimum-gas-prices = \"0uluna\"|g" $COUNTER_HOME/config/app.toml
sed -i -E "s|chain-id = \"\"|chain-id = \"${COUNTER_CHAIN_ID}\"|g" $COUNTER_HOME/config/client.toml
sed -i -E "s|node = \".*\"|node = \"tcp://${COUNTER_RPC_LADDR}\"|g" $COUNTER_HOME/config/client.toml

$COUNTER_BINARY keys add $VALIDATOR --keyring-backend=test
$COUNTER_BINARY add-genesis-account $($COUNTER_BINARY keys show $VALIDATOR --keyring-backend=test -a) 1000000000000000000uluna
$COUNTER_BINARY gentx validator 10000000000uluna --keyring-backend=test --chain-id=$COUNTER_CHAIN_ID
$COUNTER_BINARY collect-gentxs 


####################### Start chains #######################
echo "==============> Starting andromeda...<=============="
$MAIN_BINARY start \
       --rpc.laddr tcp://${MAIN_RPC_LADDR} \
       --grpc.address ${MAIN_GRPC_ADDR} \
       --p2p.laddr tcp://${MAIN_P2P_LADDR} \
       --grpc-web.enable=false \
       --log_level trace \
       --trace \
       &> ./test/logs/chi &
( tail -f -n0 ./test/logs/chi & ) | grep -q "finalizing commit of block"
echo "Chain started"

echo "==============> Starting terra...<=============="
$COUNTER_BINARY start \
       --rpc.laddr tcp://${COUNTER_RPC_LADDR} \
       --grpc.address ${COUNTER_GRPC_ADDR} \
       --p2p.laddr tcp://${COUNTER_P2P_LADDR} \
       --grpc-web.enable=false \
       --log_level trace \
       --trace \
       &> ./test/logs/terra &
( tail -f -n0 ./test/logs/terra & ) | grep -q "finalizing commit of block"
echo "Chain started"

####################### Start relayer #######################

echo "==============> Funding relayers...<=============="
RELAYER_DIR="./test/relayer"
# andromeda1hnuduzstgj2ze7l7g5rk5x4qlw8hd7es5s8xjd
MNEMONIC_1="vessel resist soda upset gadget spread sock egg soft barely hotel local weather image gaze core game once swarm nurse target fame stay small"
# terra1jy6td9r477fwr4q60adr7lz4anye5y89p5cq7q
MNEMONIC_2="panther trial minimum congress note sense immune bounce muscle tray still island hub awful style square gospel fragile eight report game leaf move category"

# send tokens to relayers
$MAIN_BINARY tx bank send $VALIDATOR andromeda1hnuduzstgj2ze7l7g5rk5x4qlw8hd7es5s8xjd 1000000uhuahua $MAIN_TX_FLAGS
sleep 5
$COUNTER_BINARY tx bank send $VALIDATOR terra1jy6td9r477fwr4q60adr7lz4anye5y89p5cq7q 1000000uluna $COUNTER_TX_FLAGS
sleep 5



echo "==============> Restoring relayer accounts...<=============="
rly keys restore andromeda rly1 "$MNEMONIC_1" --home $RELAYER_DIR
rly keys restore terra rly3 "$MNEMONIC_2" --coin-type 330 --home $RELAYER_DIR
rly transact link chi-terra --home $RELAYER_DIR

echo "==============> Starting relayers...<=============="
sleep 5
rly start chi-terra --home $RELAYER_DIR &> ./test/logs/rly