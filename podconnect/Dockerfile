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
  && go test ./ap ./daemon ./player ./output ./tracks ./audio ./session ./zeroconf ./spclient \
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
COPY patches/paused-route-owntone.sha256 /patches/paused-route-owntone.sha256
COPY patches/owntone-start-proof.sha256 /patches/owntone-start-proof.sha256
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
  && autoreconf -fi \
  && ./configure --prefix=/usr --sysconfdir=/etc --localstatedir=/var \
       --disable-install_systemd --disable-install_user \
  && make -j"$(nproc)" \
  && { make check || { cat src/test-suite.log; exit 1; }; } \
  && src/airplay_callback_owner --queue-wire \
  && src/airplay_callback_owner --queue-connect-wire \
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
