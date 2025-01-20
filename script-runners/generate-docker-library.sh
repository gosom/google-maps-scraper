#!/usr/bin/env bash

#
# official-images tag file generator
#
# usage: ./generate-docker-library.sh > [official-images-folder]/library/tusd
#

cat <<-EOH
# This file is generated via https://github.com/tus/tusd/blob/main/generate-docker-library.sh
Maintainers: tus.io (@tus), Thomas A. Hirsch (@thirsch)
GitRepo: https://github.com/tus/tusd.git
EOH

skipBeforeVersion="0.13.0"
previousVersions=();

function printVersion() {
    version=( ${1//./ } )
    majorMinor="${version[0]}.${version[1]}"

    match=$(echo "${previousVersions[@]:0}" | grep -oE "\s?$majorMinor\s?$")
    previousVersionCount=${#previousVersions[@]}

    # add the majorMinor-Version only, if it is not present yet.
    if [[ ! -z $match ]] ; then
        versionString=$1
    else
        versionString="$1 $majorMinor"
        previousVersions+=($majorMinor)
    fi

    # as the versions are sorted, the very first version gets latest.
    if [[ ${previousVersionCount} -eq 0 ]]; then
        versionString="$versionString, latest"
    fi

  cat <<-EOE

Tags: $versionString
GitCommit: $2
	EOE
}

for version in `git tag -l --sort=-v:refname | grep "^[0-9.]\+$"`; do
  commit=`git rev-parse ${version}`

  # no official release before this version
  if [[ ${version} = ${skipBeforeVersion} ]] ; then
    exit 0
  fi

  printVersion "${version}" ${commit}
done