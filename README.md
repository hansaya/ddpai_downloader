# ddpai_downloader

Simple project to download ddpai dash camera footage when car is parked in your garage. Special thanks goes to author of https://www.eionix.co.in/2019/10/10/reverse-engineer-ddpai-firmware.html

# Docker example
```
docker run --rm -d -v /path/on/host:/mnt/dvr/ --name="ddpai_downloader" -e STORAGE_PATH=/mnt/dvr/ -e RECORDING_HISTORY=96h ghcr.io/hansaya/ddpai_downloader:main
```
