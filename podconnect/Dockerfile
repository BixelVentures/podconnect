ARG BUILD_FROM

# --- PodConnect manager (Ingress picker UI) — tiny static Go binary, stdlib only ---
FROM golang:1.22-bookworm AS manager-build
WORKDIR /src
COPY manager/ ./
RUN go vet ./... && go test ./... \
  && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /podconnect-manager .

FROM ${BUILD_FROM}

# Single-stage on purpose for this test slice: keeping the -dev packages guarantees the
# matching runtime shared libraries are present, so we avoid chasing version-suffixed
# runtime package names. (The production image will slim this with a multi-stage build.)
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential git autoconf automake libtool gettext gawk gperf bison flex pkg-config \
      libsqlite3-dev libavcodec-dev libavformat-dev libavfilter-dev libswscale-dev libavutil-dev \
      libasound2-dev libgcrypt20-dev libavahi-client-dev zlib1g-dev libevent-dev libplist-dev \
      libsodium-dev libjson-c-dev libwebsockets-dev libcurl4-openssl-dev libprotobuf-c-dev \
      libunistring-dev libgnutls28-dev libconfuse-dev libxml2-dev nodejs npm ca-certificates \
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

# --- go-librespot (Spotify Connect receiver) — prebuilt binary for the target arch ---
ARG GO_LIBRESPOT_VERSION=v0.7.3
ARG BUILD_ARCH
RUN set -eux; \
    case "${BUILD_ARCH}" in \
      aarch64) ASSET="go-librespot_linux_arm64.tar.gz" ;; \
      amd64)   ASSET="go-librespot_linux_x86_64.tar.gz" ;; \
      armv7)   ASSET="go-librespot_linux_armv6.tar.gz" ;; \
      *) echo "unsupported arch: ${BUILD_ARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/gl.tgz \
      "https://github.com/devgianlu/go-librespot/releases/download/${GO_LIBRESPOT_VERSION}/${ASSET}"; \
    tar -xzf /tmp/gl.tgz -C /usr/local/bin go-librespot; \
    chmod +x /usr/local/bin/go-librespot; \
    rm -f /tmp/gl.tgz

# Dedicated user for OwnTone (it drops privileges to this uid).
RUN useradd -r -s /usr/sbin/nologin owntone || true

# PodConnect manager binary (serves the Ingress picker UI)
COPY --from=manager-build /podconnect-manager /usr/local/bin/podconnect-manager

# s6-overlay v3 services + init scripts
COPY rootfs /

# No CMD/ENTRYPOINT: the base image's s6-overlay runs everything.
