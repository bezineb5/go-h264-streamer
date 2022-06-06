package stream

import (
	"bytes"
	"context"
	"io"
	"log"
	"os/exec"
	"strconv"
	"sync"
)

const (
	readBufferSize = 4096
	bufferSizeKB   = 256

	legacyCommand    = "raspivid"
	libcameraCommand = "libcamera-vid"
)

var nalSeparator = []byte{0, 0, 0, 1} //NAL break

// CameraOptions sets the options to send to raspivid
type CameraOptions struct {
	Width          int
	Height         int
	Fps            int
	HorizontalFlip bool
	VerticalFlip   bool
	Rotation       int
	UseLibcamera   bool // Set to true to enable libcamera, otherwise use legacy raspivid stack
}

// Video streams the video for the Raspberry Pi camera to a websocket
func Video(options CameraOptions, writer io.Writer, connectionsChange chan int) {
	stopChan := make(chan struct{})
	defer close(stopChan)
	cameraStarted := sync.Mutex{}
	firstConnection := true

	for n := range connectionsChange {
		if n == 0 {
			firstConnection = true
			stopChan <- struct{}{}
		} else if firstConnection {
			firstConnection = false
			go startCamera(options, writer, stopChan, &cameraStarted)
		}
	}
}

func startCamera(options CameraOptions, writer io.Writer, stop <-chan struct{}, mutex *sync.Mutex) {
	mutex.Lock()
	defer mutex.Unlock()
	defer log.Println("Stopped raspivid")

	args := []string{
		"--inline", // H264: Force PPS/SPS header with every I frame
		"-t", "0",  // Disable timeout
		"-o", "-", // Output to stdout
		"--width", strconv.Itoa(options.Width),
		"--height", strconv.Itoa(options.Height),
		"--framerate", strconv.Itoa(options.Fps),
		"-n",                    // Do not show a preview window
		"--profile", "baseline", // H264 profile
	}

	if options.HorizontalFlip {
		args = append(args, "--hflip")
	}
	if options.VerticalFlip {
		args = append(args, "--vflip")
	}
	if options.Rotation != 0 {
		args = append(args, "--rotation")
		args = append(args, strconv.Itoa(options.Rotation))
	}
	var command string
	if options.UseLibcamera {
		command = libcameraCommand
	} else {
		command = legacyCommand
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, command, args...)
	defer cmd.Wait()
	defer cancel()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	log.Println("Started "+command, cmd.Args)

	p := make([]byte, readBufferSize)
	buffer := make([]byte, bufferSizeKB*1024)
	currentPos := 0
	NALlen := len(nalSeparator)

	for {
		select {
		case <-stop:
			log.Println("Stop requested")
			return
		default:
			n, err := stdout.Read(p)
			if err != nil {
				if err == io.EOF {
					//fmt.Println(string(p[:n])) //should handle any remainding bytes.
					log.Println("[" + command + "] EOF")
					return
				}
				log.Println(err)
			}

			copied := copy(buffer[currentPos:], p[:n])
			startPosSearch := currentPos - NALlen
			endPos := currentPos + copied

			if startPosSearch < 0 {
				startPosSearch = 0
			}
			nalIndex := bytes.Index(buffer[startPosSearch:endPos], nalSeparator)

			currentPos = endPos
			if nalIndex > 0 {
				nalIndex += startPosSearch

				// Boadcast before the NAL
				broadcast := make([]byte, nalIndex)
				copy(broadcast, buffer)
				writer.Write(broadcast)

				// Shift
				copy(buffer, buffer[nalIndex:currentPos])
				currentPos = currentPos - nalIndex
			}
		}
	}
}
