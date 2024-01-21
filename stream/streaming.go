package stream

import (
	"bytes"
	"context"
	"io"
	"log/slog"
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
	Width               int
	Height              int
	Fps                 int
	HorizontalFlip      bool
	VerticalFlip        bool
	Rotation            int
	UseLibcamera        bool // Set to true to enable libcamera, otherwise use legacy raspivid stack
	AutoDetectLibCamera bool // Set to true to automatically detect if libcamera is available. If true, UseLibcamera is ignored.
}

// Video streams the video for the Raspberry Pi camera to a websocket
func Video(options CameraOptions, writer io.Writer, connectionsChange chan int) {
	stopChan := make(chan struct{})
	defer close(stopChan)
	cameraStarted := sync.Mutex{}
	firstConnection := true

	for n := range connectionsChange {
		if n == 0 {
			// No more connections, stop the camera
			firstConnection = true
			stopChan <- struct{}{}
		} else if firstConnection {
			// First connection, start the camera
			firstConnection = false
			go startCamera(options, writer, stopChan, &cameraStarted)
		}
	}
}

func startCamera(options CameraOptions, writer io.Writer, stop <-chan struct{}, mutex *sync.Mutex) {
	mutex.Lock()
	defer mutex.Unlock()
	defer slog.Info("startCamera: Stopped camera")

	args := []string{
		"--inline", // H264: Force PPS/SPS header with every I frame
		"-t", "0",  // Disable timeout
		"-o", "-", // Output to stdout
		"--flush", // Flush output files immediately
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

	command := determineCameraCommand(options)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, command, args...)
	defer cmd.Wait()
	defer cancel()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		slog.Error("startCamera: Error getting stdout pipe", slog.Any("error", err))
		return
	}
	if err := cmd.Start(); err != nil {
		slog.Error("startCamera: Error starting camera", slog.Any("error", err))
		return
	}
	slog.Debug("startCamera: Started camera", slog.String("command", command), slog.Any("args", args))

	p := make([]byte, readBufferSize)
	buffer := make([]byte, bufferSizeKB*1024)
	currentPos := 0
	NALlen := len(nalSeparator)

	for {
		select {
		case <-stop:
			slog.Debug("startCamera: Stop requested")
			return
		default:
			n, err := stdout.Read(p)
			if err != nil {
				if err == io.EOF {
					slog.Debug("startCamera: EOF", slog.String("command", command))
					return
				}
				slog.Error("startCamera: Error reading from camera; ignoring", slog.Any("error", err))
				continue
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

func determineCameraCommand(options CameraOptions) string {
	if options.AutoDetectLibCamera {
		_, err := exec.LookPath(libcameraCommand)
		if err == nil {
			return libcameraCommand
		}
		return legacyCommand
	}

	if options.UseLibcamera {
		return libcameraCommand
	} else {
		return legacyCommand
	}
}
