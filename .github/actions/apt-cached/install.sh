#!/usr/bin/env bash
# Install $PACKAGES. Offline from $CACHE_DIR first; online with bounded retries otherwise.
set -Eeuo pipefail
read -r -a pkgs <<< "$PACKAGES"
apt_opts=(-o Acquire::Retries=3 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20
          -o DPkg::Lock::Timeout=120 -o Dpkg::Use-Pty=0)
export DEBIAN_FRONTEND=noninteractive

if [ -d "$CACHE_DIR/lists" ] && [ -d "$CACHE_DIR/archives" ]; then
  sudo cp -a "$CACHE_DIR/lists/." /var/lib/apt/lists/
  sudo cp -a "$CACHE_DIR/archives/." /var/cache/apt/archives/
  if timeout 600 sudo -E apt-get "${apt_opts[@]}" install -y --no-install-recommends --no-download "${pkgs[@]}"; then
    echo "Installed ${#pkgs[@]} packages from cache without network."
    echo "downloaded=false" >> "$GITHUB_OUTPUT"
    exit 0
  fi
  echo "::notice::Cached apt packages no longer match this runner image; downloading."
fi

attempt=1
until timeout 240 sudo -E apt-get "${apt_opts[@]}" update \
   && timeout 600 sudo -E apt-get "${apt_opts[@]}" install -y --no-install-recommends --download-only "${pkgs[@]}"; do
  if [ "$attempt" -ge 4 ]; then
    echo "::error::apt download failed after $attempt attempts (Ubuntu mirror or DNS unreachable)."
    exit 1
  fi
  # Second and later attempts avoid the Azure mirror that has hung CI before.
  sudo sed -i 's@http://azure.archive.ubuntu.com/ubuntu@http://archive.ubuntu.com/ubuntu@g' \
    /etc/apt/sources.list /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list 2>/dev/null || true
  echo "::warning::apt attempt $attempt failed; retrying in $((attempt * 15)) s."
  sleep $((attempt * 15))
  attempt=$((attempt + 1))
done

rm -rf "$CACHE_DIR"
mkdir -p "$CACHE_DIR/archives" "$CACHE_DIR/lists"
sudo find /var/cache/apt/archives -maxdepth 1 -name '*.deb' -exec cp -t "$CACHE_DIR/archives" {} +
sudo find /var/lib/apt/lists -maxdepth 1 -type f ! -name lock -exec cp -t "$CACHE_DIR/lists" {} +
sudo chown -R "$(id -u):$(id -g)" "$CACHE_DIR"
timeout 600 sudo -E apt-get "${apt_opts[@]}" install -y --no-install-recommends --no-download "${pkgs[@]}"
echo "downloaded=true" >> "$GITHUB_OUTPUT"
