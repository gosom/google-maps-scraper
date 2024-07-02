#!/usr/bin/env bash

package=""
package_name="google-maps-scraper"
output_dir="build"   
platforms=("darwin/amd64"  "darwin/arm64" "linux/amd64")

for platform in "${platforms[@]}"
do
   platform_split=(${platform//\// })
   GOOS=${platform_split[0]}
   GOARCH=${platform_split[1]}
   output_name=$output_dir'/'$package_name'-'$GOOS'-'$GOARCH
   if [ $GOOS = "windows" ]; then
       output_name+='.exe'
   fi   
   echo "Building for $platform..."
   env GOOS=$GOOS CGO_ENABLED=1 GOARCH=$GOARCH go build -o $output_name $package
   if [ $? -ne 0 ]; then
          echo 'An error has occurred! Aborting the script execution...'
       exit 1
   fi
done