# Introduction

Go port of the backend of [https://github.com/131/h264-live-player](https://github.com/131/h264-live-player)
The front is mostly unchanged, except that the size negotiation has been removed.

The goal is to use this as a library to integrate in Golang programs running on Raspberry Pi.

# Build
Run:
```
# Raspberry Pi 2 and more recent (ARM7)
env GOOS=linux GOARCH=arm GOARM=7 go build
# Raspberry Pi 1 and Zero (ARM6)
env GOOS=linux GOARCH=arm GOARM=6 go build
```

# Run
* Copy the binary and the static directory on the device.
* Run it
* In your browser, navigate to: http://<your_device>:8080/static/
