# BUILD_FROM is injected per-arch by publish.yaml / the HA builder; this default just keeps a bare
# `docker build` (and the Dockerfile linter) valid. CI always overrides it with the matching base.
ARG BUILD_FROM=ghcr.io/home-assistant/amd64-base-debian:bookworm

# --- PodConnect manager (Ingress picker UI) — tiny static Go binary, stdlib only ---
FROM golang:1.22-bookworm@sha256:3d699e4d15d0f8f13c9195c0632a16702b8cbdece2955af1c23b37ae5d55a253 AS manager-build
WORKDIR /src
COPY manager/ ./
RUN go vet ./... && go test ./... \
  && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /podconnect-manager .

# --- go-librespot device-aliases fork — compiled in a THROWAWAY builder so the final image stays slim
# (no Go toolchain / module + build cache baked in — that bloat made the image fail to pull/extract on
# device). Outputs only the stripped binary, copied into the final image below. ---
FROM golang:1.25-bookworm@sha256:3b4a11519ad929d1e1d261a12cff056f0c85b735253d7d861346b9c6f8b36437 AS gl-fork
ARG GO_LIBRESPOT_VERSION=v0.7.3
RUN apt-get update && apt-get install -y --no-install-recommends \
      pkg-config git ca-certificates libflac-dev libvorbis-dev libogg-dev libasound2-dev \
  && rm -rf /var/lib/apt/lists/*
COPY patches/ /patches/
RUN git clone --depth 1 --branch "${GO_LIBRESPOT_VERSION}" https://github.com/devgianlu/go-librespot.git /gl \
  && cd /gl \
  && test "${GO_LIBRESPOT_VERSION}" = v0.7.3 \
  && test "$(git rev-parse HEAD)" = c191a43c548e698c1dfc83590665f455fc54f084 \
  && sha256sum -c /patches/native-stack.sha256 \
  && git apply --check "/patches/aliases-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply "/patches/aliases-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply --check "/patches/recovery-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply "/patches/recovery-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply --check "/patches/autoplay-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply "/patches/autoplay-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply --check "/patches/context-commit-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply "/patches/context-commit-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply --check "/patches/volume-action-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply "/patches/volume-action-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply --check "/patches/initial-publication-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply "/patches/initial-publication-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply --check "/patches/queue-occurrence-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply "/patches/queue-occurrence-${GO_LIBRESPOT_VERSION}.patch" \
  && git apply --check "/patches/room-transport-native.patch" \
  && git apply "/patches/room-transport-native.patch" \
  && git apply --check "/patches/room-volume-native.patch" \
  && git apply "/patches/room-volume-native.patch" \
  && git apply --check "/patches/room-burst-native.patch" \
  && git apply "/patches/room-burst-native.patch" \
  && git apply --check "/patches/room-start35-native.patch" \
  && git apply "/patches/room-start35-native.patch" \
  && git apply --check "/patches/room-start35-native-amendment.patch" \
  && git apply "/patches/room-start35-native-amendment.patch" \
  && git apply --check "/patches/room-move-native.patch" \
  && git apply "/patches/room-move-native.patch" \
  && git apply --check -p2 "/patches/room-retirement-native.patch" \
  && git apply -p2 "/patches/room-retirement-native.patch" \
  && git apply --check "/patches/room-startcredit-handoff-native.patch" \
  && git apply "/patches/room-startcredit-handoff-native.patch" \
  && git apply --check "/patches/room-startcredit-reviewfix-native.patch" \
  && git apply "/patches/room-startcredit-reviewfix-native.patch" \
  && git apply --check "/patches/room-queue-native.patch" \
  && git apply "/patches/room-queue-native.patch" \
  && git apply --check "/patches/room-next-native.patch" \
  && git apply "/patches/room-next-native.patch" \
  && git apply --check "/patches/connect-recovery-native.patch" \
  && git apply "/patches/connect-recovery-native.patch" \
  && git apply --check "/patches/connect-recovery-diagnostics-native.patch" \
  && git apply "/patches/connect-recovery-diagnostics-native.patch" \
  && git apply --check "/patches/paused-route-native.patch" \
  && git apply "/patches/paused-route-native.patch" \
  && git apply --check "/patches/vorbis-cleanup-native.patch" \
  && git apply "/patches/vorbis-cleanup-native.patch" \
  && git apply --check "/patches/room-start-diagnostics-native.patch" \
  && git apply "/patches/room-start-diagnostics-native.patch" \
  && git apply --check "/patches/paused-track-native.patch" \
  && git apply "/patches/paused-track-native.patch" \
  && git apply --check "/patches/connect-output-admission-diagnostics-native.patch" \
  && git apply "/patches/connect-output-admission-diagnostics-native.patch" \
  && git apply --check "/patches/connect-closed-prior-native.patch" \
  && git apply "/patches/connect-closed-prior-native.patch" \
  && git apply --check "/patches/connect-output-failure-native.patch" \
  && git apply "/patches/connect-output-failure-native.patch" \
  && git apply --check "/patches/connect-no-output-transport-native.patch" \
  && git apply "/patches/connect-no-output-transport-native.patch" \
  && git apply --check "/patches/pipe-control-liveness-native.patch" \
  && git apply "/patches/pipe-control-liveness-native.patch" \
  && git apply --check "/patches/connect-load-failure-native.patch" \
  && git apply "/patches/connect-load-failure-native.patch" \
  && git apply --check "/patches/connect-paused-load-receipt-native.patch" \
  && git apply "/patches/connect-paused-load-receipt-native.patch" \
  && git apply --check "/patches/connect-explicit-resume-native.patch" \
  && git apply "/patches/connect-explicit-resume-native.patch" \
  && git apply --check "/patches/connect-prepared-start-shutdown-native.patch" \
  && git apply "/patches/connect-prepared-start-shutdown-native.patch" \
  && git apply --check "/patches/exact-transferred-track-native.patch" \
  && git apply "/patches/exact-transferred-track-native.patch" \
  && git apply --check "/patches/direct-native-pause-native.patch" \
  && git apply "/patches/direct-native-pause-native.patch" \
  && git apply --check /patches/room-release-native.patch \
  && git apply /patches/room-release-native.patch \
  && git apply --check /patches/room-release-native-tests.patch \
  && git apply /patches/room-release-native-tests.patch \
  && git apply --check /patches/move-quiesce-native.patch \
  && git apply /patches/move-quiesce-native.patch \
  && git apply --check /patches/move-quiesce-native-tests.patch \
  && git apply /patches/move-quiesce-native-tests.patch \
  && git apply --check /patches/room-owner-parity-native.patch \
  && git apply /patches/room-owner-parity-native.patch \
  && git apply --check /patches/room-owner-parity-native-tests.patch \
  && git apply /patches/room-owner-parity-native-tests.patch \
  && git apply --check /patches/dealer-roomchange-native.patch \
  && git apply /patches/dealer-roomchange-native.patch \
  && git apply --check /patches/dealer-roomchange-native-tests.patch \
  && git apply /patches/dealer-roomchange-native-tests.patch \
  && git apply --check /patches/private-diagnostics-native.patch \
  && git apply /patches/private-diagnostics-native.patch \
  && git apply --check /patches/private-diagnostics-native-tests.patch \
  && git apply /patches/private-diagnostics-native-tests.patch \
  && git apply --check /patches/blocked-decoder-controls-native.patch \
  && git apply /patches/blocked-decoder-controls-native.patch \
  && git apply --check /patches/blocked-decoder-controls-native-tests.patch \
  && git apply /patches/blocked-decoder-controls-native-tests.patch \
  && git apply --check /patches/queue-metadata-background-native.patch \
  && git apply /patches/queue-metadata-background-native.patch \
  && go test ./ap ./daemon ./player ./output ./tracks ./audio ./session ./zeroconf ./spclient ./vorbis \
  && go build -trimpath -ldflags="-s -w" -o /go-librespot ./cmd/daemon

FROM ${BUILD_FROM}

# Build deps for OwnTone (from source) + the runtime shared libs go-librespot's fork links against
# (libflac/libvorbis/libogg/libasound — kept as -dev to avoid chasing version-suffixed runtime names).
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential git autoconf automake libtool gettext gawk gperf bison flex pkg-config \
      libsqlite3-dev libavcodec-dev libavformat-dev libavfilter-dev libswscale-dev libavutil-dev \
      libasound2-dev libgcrypt20-dev libavahi-client-dev zlib1g-dev libevent-dev libplist-dev \
      libsodium-dev libjson-c-dev libwebsockets-dev libcurl4-openssl-dev libprotobuf-c-dev \
      libunistring-dev libgnutls28-dev libconfuse-dev libxml2-dev nodejs npm ca-certificates \
      libflac-dev libvorbis-dev libogg-dev \
      avahi-daemon dbus libnss-mdns ffmpeg curl jq tar iproute2 \
  && rm -rf /var/lib/apt/lists/*

# --- OwnTone (AirPlay 2 sender) — built from source (no ARM64 package exists) ---
ARG OWNTONE_REF=29.2
COPY patches/native-attention-fa607.patch /patches/native-attention-fa607.patch
COPY patches/room-transport-owntone.patch /patches/room-transport-owntone.patch
COPY patches/room-volume-owntone.patch /patches/room-volume-owntone.patch
COPY patches/room-burst-owntone.patch /patches/room-burst-owntone.patch
COPY patches/room-move-owntone.patch /patches/room-move-owntone.patch
COPY patches/owntone-move.sha256 /patches/owntone-move.sha256
COPY patches/room-queue-owntone.patch /patches/room-queue-owntone.patch
COPY patches/room-start-proof-owntone.patch /patches/room-start-proof-owntone.patch
COPY patches/connect-recovery-owntone.patch /patches/connect-recovery-owntone.patch
COPY patches/connect-recovery-owntone.sha256 /patches/connect-recovery-owntone.sha256
COPY patches/connect-recovery-diagnostics-owntone.patch /patches/connect-recovery-diagnostics-owntone.patch
COPY patches/connect-recovery-diagnostics-owntone.sha256 /patches/connect-recovery-diagnostics-owntone.sha256
COPY patches/paused-route-owntone.patch /patches/paused-route-owntone.patch
COPY patches/connect-recovery-busy-owntone.patch /patches/connect-recovery-busy-owntone.patch
COPY patches/paused-route-owntone.sha256 /patches/paused-route-owntone.sha256
COPY patches/connect-recovery-busy-owntone.sha256 /patches/connect-recovery-busy-owntone.sha256
COPY patches/fifo-reader-handoff-owntone.patch /patches/fifo-reader-handoff-owntone.patch
COPY patches/fifo-reader-handoff-owntone.sha256 /patches/fifo-reader-handoff-owntone.sha256
COPY patches/start-starvation-owner-owntone.patch /patches/start-starvation-owner-owntone.patch
COPY patches/start-starvation-owner-owntone.sha256 /patches/start-starvation-owner-owntone.sha256
COPY patches/paused-track-owntone.patch /patches/paused-track-owntone.patch
COPY patches/paused-track-owntone.sha256 /patches/paused-track-owntone.sha256
COPY patches/connect-closed-prior-owntone.patch /patches/connect-closed-prior-owntone.patch
COPY patches/connect-closed-prior-owntone.sha256 /patches/connect-closed-prior-owntone.sha256
COPY patches/connect-atomic-init-owntone.patch /patches/connect-atomic-init-owntone.patch
COPY patches/connect-atomic-init-owntone.sha256 /patches/connect-atomic-init-owntone.sha256
COPY patches/connect-retired-autostart-owntone.patch /patches/connect-retired-autostart-owntone.patch
COPY patches/connect-retired-autostart-owntone.sha256 /patches/connect-retired-autostart-owntone.sha256
COPY patches/connect-resume-prepcm-busy-owntone.patch /patches/connect-resume-prepcm-busy-owntone.patch
COPY patches/connect-resume-prepcm-busy-owntone.sha256 /patches/connect-resume-prepcm-busy-owntone.sha256
COPY patches/connect-retired-owner-detach-owntone.patch /patches/connect-retired-owner-detach-owntone.patch
COPY patches/connect-retired-owner-detach-owntone.sha256 /patches/connect-retired-owner-detach-owntone.sha256
COPY patches/ordinary-pause-ack-owntone.patch /patches/ordinary-pause-ack-owntone.patch
COPY patches/ordinary-pause-ack-owntone.sha256 /patches/ordinary-pause-ack-owntone.sha256
COPY patches/pipe-metadata-owner-owntone.patch /patches/pipe-metadata-owner-owntone.patch
COPY patches/pipe-metadata-owner-owntone.sha256 /patches/pipe-metadata-owner-owntone.sha256
COPY patches/first-pcm-clock-owntone.patch /patches/first-pcm-clock-owntone.patch
COPY patches/first-pcm-clock-owntone.sha256 /patches/first-pcm-clock-owntone.sha256
COPY patches/purpose-bound-transport-ack-owntone.patch /patches/purpose-bound-transport-ack-owntone.patch
COPY patches/purpose-bound-transport-ack-owntone.sha256 /patches/purpose-bound-transport-ack-owntone.sha256
COPY patches/move-first-pcm-clock-owntone.patch /patches/move-first-pcm-clock-owntone.patch
COPY patches/move-first-pcm-clock-owntone.sha256 /patches/move-first-pcm-clock-owntone.sha256
COPY patches/owntone-start-proof.sha256 /patches/owntone-start-proof.sha256
COPY patches/room-release-owntone.patch /patches/room-release-owntone.patch
COPY patches/room-release-owntone-tests.patch /patches/room-release-owntone-tests.patch
COPY patches/room-release-owntone-timer-tests.patch /patches/room-release-owntone-timer-tests.patch
COPY patches/room-release-owntone-target2-tests.patch /patches/room-release-owntone-target2-tests.patch
COPY patches/room-release-owntone.sha256 /patches/room-release-owntone.sha256
COPY patches/room-owner-parity-owntone.patch /patches/room-owner-parity-owntone.patch
COPY patches/room-owner-parity-owntone-tests.patch /patches/room-owner-parity-owntone-tests.patch
COPY patches/room-owner-parity-owntone.sha256 /patches/room-owner-parity-owntone.sha256
COPY patches/private-diagnostics-owntone.patch /patches/private-diagnostics-owntone.patch
COPY patches/private-diagnostics-owntone.sha256 /patches/private-diagnostics-owntone.sha256
COPY patches/retire-diagnostics-owntone.patch /patches/retire-diagnostics-owntone.patch
COPY patches/retire-diagnostics-tests.patch /patches/retire-diagnostics-tests.patch
COPY patches/retire-diagnostics-owntone.sha256 /patches/retire-diagnostics-owntone.sha256
COPY patches/owntone-queue.sha256 /patches/owntone-queue.sha256
RUN git clone --depth 1 --branch "${OWNTONE_REF}" https://github.com/owntone/owntone-server.git /src \
  && cd /src \
  && test "$(git rev-parse HEAD)" = fa607a02768d9e29cd87065ac30ac66ea673f1ae \
  && git apply --check /patches/native-attention-fa607.patch \
  && git apply /patches/native-attention-fa607.patch \
  && git apply --check /patches/room-transport-owntone.patch \
  && git apply /patches/room-transport-owntone.patch \
  && git apply --check /patches/room-volume-owntone.patch \
  && git apply /patches/room-volume-owntone.patch \
  && git apply --check /patches/room-burst-owntone.patch \
  && git apply /patches/room-burst-owntone.patch \
  && sha256sum -c /patches/owntone-move.sha256 \
  && git apply --check /patches/room-move-owntone.patch \
  && git apply /patches/room-move-owntone.patch \
  && sha256sum -c /patches/owntone-queue.sha256 \
  && git apply --check /patches/room-queue-owntone.patch \
  && git apply /patches/room-queue-owntone.patch \
  && sha256sum -c /patches/owntone-start-proof.sha256 \
  && git apply --check /patches/room-start-proof-owntone.patch \
  && git apply /patches/room-start-proof-owntone.patch \
  && sha256sum -c /patches/connect-recovery-owntone.sha256 \
  && git apply --check /patches/connect-recovery-owntone.patch \
  && git apply /patches/connect-recovery-owntone.patch \
  && sha256sum -c /patches/connect-recovery-diagnostics-owntone.sha256 \
  && git apply --check /patches/connect-recovery-diagnostics-owntone.patch \
  && git apply /patches/connect-recovery-diagnostics-owntone.patch \
  && sha256sum -c /patches/paused-route-owntone.sha256 \
  && git apply --check /patches/paused-route-owntone.patch \
  && git apply /patches/paused-route-owntone.patch \
  && sha256sum -c /patches/connect-recovery-busy-owntone.sha256 \
  && git apply --check /patches/connect-recovery-busy-owntone.patch \
  && git apply /patches/connect-recovery-busy-owntone.patch \
  && sha256sum -c /patches/fifo-reader-handoff-owntone.sha256 \
  && git apply --check /patches/fifo-reader-handoff-owntone.patch \
  && git apply /patches/fifo-reader-handoff-owntone.patch \
  && sha256sum -c /patches/start-starvation-owner-owntone.sha256 \
  && git apply --check /patches/start-starvation-owner-owntone.patch \
  && git apply /patches/start-starvation-owner-owntone.patch \
  && sha256sum -c /patches/paused-track-owntone.sha256 \
  && git apply --check /patches/paused-track-owntone.patch \
  && git apply /patches/paused-track-owntone.patch \
  && sha256sum -c /patches/connect-closed-prior-owntone.sha256 \
  && git apply --check /patches/connect-closed-prior-owntone.patch \
  && git apply /patches/connect-closed-prior-owntone.patch \
  && sha256sum -c /patches/connect-atomic-init-owntone.sha256 \
  && git apply --check /patches/connect-atomic-init-owntone.patch \
  && git apply /patches/connect-atomic-init-owntone.patch \
  && sha256sum -c /patches/connect-retired-autostart-owntone.sha256 \
  && git apply --check /patches/connect-retired-autostart-owntone.patch \
  && git apply /patches/connect-retired-autostart-owntone.patch \
  && sha256sum -c /patches/connect-resume-prepcm-busy-owntone.sha256 \
  && git apply --check /patches/connect-resume-prepcm-busy-owntone.patch \
  && git apply /patches/connect-resume-prepcm-busy-owntone.patch \
  && sha256sum -c /patches/connect-retired-owner-detach-owntone.sha256 \
  && git apply --check /patches/connect-retired-owner-detach-owntone.patch \
  && git apply /patches/connect-retired-owner-detach-owntone.patch \
  && sha256sum -c /patches/ordinary-pause-ack-owntone.sha256 \
  && git apply --check /patches/ordinary-pause-ack-owntone.patch \
  && git apply /patches/ordinary-pause-ack-owntone.patch \
  && sha256sum -c /patches/pipe-metadata-owner-owntone.sha256 \
  && git apply --check /patches/pipe-metadata-owner-owntone.patch \
  && git apply /patches/pipe-metadata-owner-owntone.patch \
  && sha256sum -c /patches/first-pcm-clock-owntone.sha256 \
  && git apply --check /patches/first-pcm-clock-owntone.patch \
  && git apply /patches/first-pcm-clock-owntone.patch \
  && sha256sum -c /patches/purpose-bound-transport-ack-owntone.sha256 \
  && git apply --check /patches/purpose-bound-transport-ack-owntone.patch \
  && git apply /patches/purpose-bound-transport-ack-owntone.patch \
  && sha256sum -c /patches/move-first-pcm-clock-owntone.sha256 \
  && git apply --check /patches/move-first-pcm-clock-owntone.patch \
  && git apply /patches/move-first-pcm-clock-owntone.patch \
  && sha256sum -c /patches/room-release-owntone.sha256 \
  && git apply --check /patches/room-release-owntone.patch \
  && git apply /patches/room-release-owntone.patch \
  && git apply --check /patches/room-release-owntone-tests.patch \
  && git apply /patches/room-release-owntone-tests.patch \
  && git apply --check /patches/room-release-owntone-timer-tests.patch \
  && git apply /patches/room-release-owntone-timer-tests.patch \
  && git apply --check /patches/room-release-owntone-target2-tests.patch \
  && git apply /patches/room-release-owntone-target2-tests.patch \
  && sha256sum -c /patches/room-owner-parity-owntone.sha256 \
  && git apply --check /patches/room-owner-parity-owntone.patch \
  && git apply /patches/room-owner-parity-owntone.patch \
  && git apply --check /patches/room-owner-parity-owntone-tests.patch \
  && git apply /patches/room-owner-parity-owntone-tests.patch \
  && sha256sum -c /patches/private-diagnostics-owntone.sha256 \
  && git apply --check /patches/private-diagnostics-owntone.patch \
  && git apply /patches/private-diagnostics-owntone.patch \
  && sha256sum -c /patches/retire-diagnostics-owntone.sha256 \
  && git apply --check /patches/retire-diagnostics-owntone.patch \
  && git apply /patches/retire-diagnostics-owntone.patch \
  && git apply --check /patches/retire-diagnostics-tests.patch \
  && git apply /patches/retire-diagnostics-tests.patch \
  && autoreconf -fi \
  && ./configure --prefix=/usr --sysconfdir=/etc --localstatedir=/var \
       --disable-install_systemd --disable-install_user \
  && make -j"$(nproc)" \
  && { make check || { cat src/test-suite.log; exit 1; }; } \
  && src/airplay_callback_owner --queue-wire \
  && src/airplay_callback_owner --queue-connect-wire \
  && src/airplay_callback_owner --start-retire-closed-wire \
  && src/airplay_callback_owner --start-retire-closed-no-session-wire \
  && src/airplay_callback_owner --start-retire-closed-empty-wire \
  && make install \
  && cd / && rm -rf /src

# --- go-librespot (Spotify Connect receiver) — the device-aliases fork, copied from the slim builder
# stage above. The runtime libs it links (flac/vorbis/ogg/asound) are installed in the apt block above. ---
COPY --from=gl-fork /go-librespot /usr/local/bin/go-librespot
RUN chmod +x /usr/local/bin/go-librespot

# Dedicated user for OwnTone (it drops privileges to this uid).
RUN useradd -r -s /usr/sbin/nologin owntone || true

# PodConnect manager binary (serves the Ingress picker UI)
COPY --from=manager-build /podconnect-manager /usr/local/bin/podconnect-manager

# s6-overlay v3 services + init scripts
COPY rootfs /

# No CMD/ENTRYPOINT: the base image's s6-overlay runs everything.
