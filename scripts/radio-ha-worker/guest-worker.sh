#!/bin/sh
# Ordinary S6/defaultcap/defaultseccomp, network-none. Test-only provider/receiver adapter.
set -eu
umask 077
ROOT=/tmp/radio-production-factory
C=/out/qualified-c-fixture
OLD_PID= NATIVE_PID=
cleanup() {
  if [ -f "$ROOT/peer-metrics.json" ]; then cp "$ROOT/peer-metrics.json" /out/factory-peer-metrics.json; fi
  if [ -d "$ROOT/radio-owner-journal" ]; then cp -R "$ROOT/radio-owner-journal" /out/factory-manager-journal; fi
  if [ -d "$ROOT/radio-sender" ]; then
    cp -R "$ROOT/radio-sender" /out/factory-private-radio-state
    find "$ROOT/radio-sender" -maxdepth 2 -type f -printf "%m %U %G %p\n" > /out/factory-files.txt || true
    for log in "$ROOT/radio-sender"/*.log; do if [ -f "$log" ]; then cp "$log" /out/factory-radio-runtime.log; fi; done
  fi
  for pid in "$NATIVE_PID" "$OLD_PID"; do
    if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; fi
  done
  for pid in "$NATIVE_PID" "$OLD_PID"; do
    if [ -n "$pid" ]; then wait "$pid" 2>/dev/null || true; fi
  done
}
trap cleanup EXIT INT TERM
printf executed > /out/worker-entry-executed
cp /c-source/tests/fixtures/radio-sender-fixture.mp3 /tmp/radio-sender-fixture.mp3
chmod 644 /tmp/radio-sender-fixture.mp3
mkdir "$ROOT"
chmod 711 "$ROOT"
mkdir "$ROOT/owntone" "$ROOT/private"
TOKEN=2222222222222222222222222222222222222222222222222222222222222222
SECRET=1111111111111111111111111111111111111111111111111111111111111111
ADMIN=3333333333333333333333333333333333333333333333333333333333333333
if [ "${PC_RADIO_CHAIN_DISCOVERY:-}" = 1 ]; then
 export PC_RADIO_CHAIN_SECOND_DISCOVERY_OLD="$ROOT/old-second-discovered" PC_RADIO_CHAIN_SECOND_DISCOVERY_SENDER="$ROOT/sender-second-discovered"
 export PC_RADIO_HTTP_SECOND_DISCOVERY_FILE="$PC_RADIO_CHAIN_SECOND_DISCOVERY_OLD"
fi
PC_RADIO_HTTP_TARGET_ID=1 "$C" --radio-http-prepare-fixture "$ROOT/owntone" > /out/factory-old-prepare.json 2>/out/factory-old-prepare.log
OLD_CONFIG="$ROOT/owntone/owntone.conf"
sed -i 's@/owntone/owntone.db@/owntone/songs3.db@g' "$OLD_CONFIG"
mv "$ROOT/owntone/owntone.db" "$ROOT/owntone/songs3.db"
chmod 711 "$ROOT/owntone"
: > /out/factory-old-diagnostic.json
env -u PODCONNECT_RADIO_SENDER PC_RADIO_HTTP_CONFIG="$OLD_CONFIG" PC_RADIO_HTTP_METRICS="$ROOT/peer-metrics.json" PC_RADIO_HTTP_TARGET_ID=1 PC_RADIO_HTTP_NATIVE_FIFO="$ROOT/owntone/native.fifo" "$C" --radio-http-owner > /out/factory-old-diagnostic.json 2>/out/factory-old-server.log &
OLD_PID=$!
export PC_RADIO_CHAIN_OLD_PID="$OLD_PID"
if [ "${PC_RADIO_CHAIN_DISCOVERY:-}" = 1 ]; then export PC_RADIO_HTTP_SECOND_DISCOVERY_FILE="$PC_RADIO_CHAIN_SECOND_DISCOVERY_SENDER"; fi
i=0
while ! [ -s /out/factory-old-diagnostic.json ]; do kill -0 "$OLD_PID"; i=$((i+1)); [ "$i" -lt 100 ]; sleep 0.1; done
if [ "${PC_RADIO_HTTP_SECOND_TARGET_ID:-}" = 2 ]; then
 export PC_RADIO_HTTP_SECOND_RECEIVER_PORT=$(jq -r .second_receiver_port /out/factory-old-diagnostic.json)
 PAIR_IDS="1 2"
else
 PAIR_IDS=1
fi
CONTROLLER=$(jq -r '.controller_id' /out/factory-old-diagnostic.json)
RECEIVER_PORT=$(jq -r '.receiver_port' /out/factory-old-diagnostic.json)
# Only this synthetic DB is assigned its real production service uid. Nothing
# outside the fresh fixture directory is chowned or copied into the radio DB.
for fixture_db in "$ROOT"/owntone/songs3.db*; do chown owntone:owntone "$fixture_db"; done
PODCONNECT_RADIO_SENDER=1 /out/production-c-cli --radio-pairing-export "$ROOT/owntone/songs3.db" "$ROOT/private/source-pair-stage.json" "$CONTROLLER" $PAIR_IDS > /out/factory-source-export.log 2>&1
cp "$ROOT/private/source-pair-stage.json" /out/factory-initial-source-stage.json
PC_NATIVE_START_MODE=actual-c-radio-http PC_NATIVE_START_URL=http://127.0.0.1:36899 PC_NATIVE_START_PIPE="$ROOT/owntone/native.fifo" PC_NATIVE_RADIO_CONTROL_TOKEN="$TOKEN" PC_NATIVE_RADIO_OWNER_URL=http://127.0.0.1:36901 PC_NATIVE_RADIO_HTTP_READY="$ROOT/native-ready.json" PC_NATIVE_RADIO_HTTP_RELEASE="$ROOT/native-release" PC_NATIVE_RADIO_HTTP_TIMEOUT_SECONDS=120 /out/native-radio-final-http.test -test.run '^TestPreparedDealerSTARTUsesRealFIFOAndPreservesTransfer$' -test.v > /out/factory-native.log 2>&1 &
NATIVE_PID=$!
i=0
while ! [ -s "$ROOT/native-ready.json" ]; do kill -0 "$NATIVE_PID"; i=$((i+1)); [ "$i" -lt 200 ]; sleep 0.1; done
NATIVE_URL=$(jq -r '.native_url' "$ROOT/native-ready.json")
TARGET=$(jq -r '.target_id' "$ROOT/native-ready.json")
cp "$ROOT/native-ready.json" /out/factory-native-ready.json
cp "$C" "$ROOT/private/sender-fixture"
export PC_RADIO_HTTP_TARGET_ID=1 PC_RADIO_HTTP_RECEIVER_PORT="$RECEIVER_PORT"
# Exact nonsecret inert provider/receiver values restored only by compiled adapter.
( env | LC_ALL=C sort | sed -n '/^PC_RADIO_HTTP_/p' ) > /out/fixture.env
chmod 644 /out/fixture.env
PC_RADIO_CHAIN_RUN=1 PC_RADIO_CHAIN_WORKER=1 PC_RADIO_CHAIN_PEER_METRICS="$ROOT/peer-metrics.json" PC_RADIO_CHAIN_FIXTURE_ROOT="$ROOT" PC_RADIO_CHAIN_NATIVE_URL="$NATIVE_URL" PC_RADIO_CHAIN_NATIVE_TOKEN="$TOKEN" PC_RADIO_CHAIN_TARGET_ID="$TARGET" PC_RADIO_CHAIN_RECEIVER_PORT="$RECEIVER_PORT" PC_RADIO_CHAIN_SENDER_BINARY="$ROOT/private/sender-fixture" PC_RADIO_CHAIN_PAIRING_STAGE="$ROOT/private/source-pair-stage.json" PC_RADIO_CHAIN_HANDOFF_SECRET="$SECRET" PC_RADIO_CHAIN_ADMIN_PASSWORD="$ADMIN" PC_RADIO_CHAIN_API_PORT=36900 PC_RADIO_CHAIN_WS_PORT=36902 PC_RADIO_CHAIN_MANAGER_PORT=36901 /out/go-radio-full-chain.test -test.run '^TestRadioProductionFactoryExternalChain$' -test.v > /out/factory-manager.log 2>&1

cp "$ROOT/manager-chain-result.json" /out/factory-chain-result.json
: > "$ROOT/native-release"
wait "$NATIVE_PID"
NATIVE_PID=
echo 'PASS actual HA worker factory/export/init/manager/Native/two-C PIDFD/seccomp; isolated inerts, not household or shipping acceptance'
