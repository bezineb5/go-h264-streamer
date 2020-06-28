package stream

import (
	"bytes"
	"io"
	"log"
	"os/exec"
	"strconv"
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
	cameraStarted := false

	for {
		select {
		case n := <-connectionsChange:
			if n <= 0 {
				stopChan <- true
				cameraStarted = false
			} else if !cameraStarted {
				go startCamera(options, sender, stopChan)
				cameraStarted = true
			}
		}
	}

}

func startCamera(options CameraOptions, sender Sender, stop chan bool) {
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

	cmd := exec.Command("raspivid", args...)
	log.Println("Started raspicam", cmd.Args)
	defer cmd.Wait()
	defer log.Println("Stopped raspicam")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}

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
