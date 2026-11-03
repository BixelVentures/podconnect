#!/bin/sh
# Test artifact adapter only: production export/initialize, existing actual C
# HTTP/player/output fixture for the finite provider and inert receiver edges.
set -eu
if [ "${1:-}" = --radio-pairing-export ] || [ "${1:-}" = --radio-pairing-export-known ] || [ "${PODCONNECT_RADIO_INITIALIZE_ONLY:-}" = 1 ]; then
  exec /out/production-c-cli "$@"
fi
[ "${PODCONNECT_RADIO_SENDER:-}" = 1 ]
[ "${1:-}" = -f ] && [ "${2:-}" = -c ] && [ "$#" = 3 ]
export PC_RADIO_HTTP_CONFIG="$3"
exec /out/qualified-c-fixture --radio-http-owner > /out/factory-supervised-c-diagnostic.json 2> /out/factory-supervised-c.log
