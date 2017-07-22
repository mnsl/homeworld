#!/bin/bash

set -e -u

cd "$(dirname "$0")"
reprepro -Vb . includedeb homeworld ../build-debs/binaries/homeworld-*.deb
rsync -av --progress ./dists ./pool /mit/hyades/debian/

