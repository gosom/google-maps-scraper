#make sure run 
#chmod +x ./run_macos.sh

./google-maps-scraper -input input.json   -exit-on-inactivity 5m -lang vi -c 16  -proxyfile proxies.txt -results results.csv 
