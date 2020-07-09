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
)

var nalSeparator = []byte{0, 0, 0, 1} //NAL break

// Sender is a interface which implements the ability to transmit/broadcast messages
type Sender interface {
	Send([]byte) error
}

// CameraOptions sets the options to send to raspivid
type CameraOptions struct {
	Width          int
	Height         int
	Fps            int
	HorizontalFlip bool
	VerticalFlip   bool
	Rotation       int
}

// Video streams the video for the Raspberry Pi camera to a websocket
func Video(options CameraOptions, sender Sender, connectionsChange chan int) {
	stopChan := make(chan bool)
	cameraStarted := sync.Mutex{}
	firstConnection := true

	for {
		select {
		case n := <-connectionsChange:
			if n == 0 {
				firstConnection = true
				stopChan <- true
			} else if firstConnection {
				firstConnection = false
				go startCamera(options, sender, stopChan, &cameraStarted)
			}
		}
	}

}

func startCamera(options CameraOptions, sender Sender, stop chan bool, mutex *sync.Mutex) {
	mutex.Lock()
	defer mutex.Unlock()
	defer log.Println("Stopped raspivid")

	args := []string{
		"-ih",
		"-t", "0",
		"-o", "-",
		"-w", strconv.Itoa(options.Width),
		"-h", strconv.Itoa(options.Height),
		"-fps", strconv.Itoa(options.Fps),
		"-n",
		"-pf", "baseline",
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

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "raspivid", args...)
	defer cmd.Wait()
	defer cancel()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	log.Println("Started raspivid", cmd.Args)

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
					log.Println("[Raspivid] EOF")
					return
				}
				log.Println(err)
			}

			//fmt.Println("Received", p[:n])
			copied := copy(buffer[currentPos:], p[:n])
			startPosSearch := currentPos - NALlen
			endPos := currentPos + copied
			//fmt.Println("Buffer", buffer[:endPos])

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
				sender.Send(broadcast)

				// Shift
				copy(buffer, buffer[nalIndex:currentPos])
				currentPos = currentPos - nalIndex
			}
		}
	}
}
