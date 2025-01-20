#!/usr/bin/env bash

set -e

version="$(git tag -l --points-at HEAD)"
commit=$(git log --format="%H" -n 1)

function compile {
  local os=$1
  local arch=$2
  local ext=$3

  echo "Compiling for $os/$arch..."

  local dir="tusd_${os}_${arch}"
  rm -rf "$dir"
  mkdir -p "$dir"
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-X github.com/tus/tusd/v2/cmd/tusd/cli.VersionName=${version} -X github.com/tus/tusd/v2/cmd/tusd/cli.GitCommit=${commit} -X 'github.com/tus/tusd/v2/cmd/tusd/cli.BuildDate=$(date --utc)'" \
    -o "$dir/tusd$ext" ./cmd/tusd/main.go
}

function makezip {
  local os=$1
  local arch=$2
  local ext=$3

  echo "Zipping for $os/$arch..."

  local dir="tusd_${os}_${arch}"
  zip "$dir.zip" "$dir/tusd$ext" LICENSE.txt README.md
  sha256sum "$dir.zip" > "$dir.zip.sha256"
}

function maketar {
  local os=$1
  local arch=$2

  echo "Tarring for $os/$arch..."

  local dir="tusd_${os}_${arch}"
  tar -czf "$dir.tar.gz" "$dir/tusd" LICENSE.txt README.md
  sha256sum "$dir.tar.gz" > "$dir.tar.gz.sha256"
}

function makedep {
  local arch=$1

  echo "Debbing for $arch..."

  local dir="tusd_snapshot_${arch}"
  rm -rf "$dir"
  mkdir -p "$dir"
  mkdir -p "$dir/DEBIAN"
  mkdir -p "$dir/usr/bin"
  cp "./tusd_linux_${arch}/tusd" "./$dir/usr/bin/tusd"

  echo "Package: tusd" >> "./$dir/DEBIAN/control"
  echo "Maintainer: Marius <maerious@gmail.com>" >> "./$dir/DEBIAN/control"
  echo "Section: devel" >> "./$dir/DEBIAN/control"
  echo "Priority: optional" >> "./$dir/DEBIAN/control"
  echo "Version: ${version:1}" >> "./$dir/DEBIAN/control"
  echo "Architecture: ${arch}" >> "./$dir/DEBIAN/control"
  echo "Homepage: https://github.com/tus/tusd" >> "./$dir/DEBIAN/control"
  echo "Built-Using: $(go version)" >> "./$dir/DEBIAN/control"
  echo "Description: The official server implementation of the tus resumable upload protocol." >> "./$dir/DEBIAN/control"

  dpkg-deb --build "$dir"
}

export -f compile
export -f maketar
export -f makezip
export -f makedep
