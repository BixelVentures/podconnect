# BUILD_FROM is injected per-arch by publish.yaml / the HA builder; this default just keeps a bare
# `docker build` (and the Dockerfile linter) valid. CI always overrides it with the matching base.
ARG BUILD_FROM=ghcr.io/home-assistant/amd64-base-debian:bookworm

# --- PodConnect manager (Ingress picker UI) — tiny static Go binary, stdlib only ---
FROM golang:1.22-bookworm AS manager-build
WORKDIR /src
COPY manager/ ./
RUN go vet ./... && go test ./... \
  && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /podconnect-manager .

# --- go-librespot device-aliases fork — compiled in a THROWAWAY builder so the final image stays slim
# (no Go toolchain / module + build cache baked in — that bloat made the image fail to pull/extract on
# device). Outputs only the stripped binary, copied into the final image below. ---
FROM golang:1.25-bookworm AS gl-fork
ARG GO_LIBRESPOT_VERSION=v0.7.3
RUN apt-get update && apt-get install -y --no-install-recommends \
      pkg-config git ca-certificates libflac-dev libvorbis-dev libogg-dev libasound2-dev \
  && rm -rf /var/lib/apt/lists/*
COPY patches/ /patches/
RUN git clone --depth 1 --branch "${GO_LIBRESPOT_VERSION}" https://github.com/devgianlu/go-librespot.git /gl \
  && cd /gl \
  && git apply "/patches/aliases-${GO_LIBRESPOT_VERSION}.patch" \
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
RUN git clone --depth 1 --branch "${OWNTONE_REF}" https://github.com/owntone/owntone-server.git /src \
  && cd /src \
  && autoreconf -fi \
  && ./configure --prefix=/usr --sysconfdir=/etc --localstatedir=/var \
       --disable-install_systemd --disable-install_user \
  && make -j"$(nproc)" \
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
