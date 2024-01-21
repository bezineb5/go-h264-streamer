package main

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

type connection struct {
	ws   *websocket.Conn // The websocket connection.
	send chan []byte     // Buffered channel of outbound messages.
}

// WebSocketHandler represents a websocket
type WebSocketHandler interface {
	io.Writer
	Handler(w http.ResponseWriter, r *http.Request)
}

// webSocketHandler main structure
type webSocketHandler struct {
	connections     map[*connection]bool // Registered connections.
	broadcast       chan []byte          // Inbound messages from the connections.
	register        chan *connection     // Register requests from the connections.
	unregister      chan *connection     // Unregister requests from connections.
	connectionCount chan int
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// handles messages coming from websocket
func (c *connection) reader(errCh chan bool) {
	for {
		messageType, message, err := c.ws.ReadMessage()
		if err != nil {
			slog.Error("connection: Error reading message from websocket: ", err)
			defer func() { errCh <- true }()
			return
		}

		slog.Info("connection: Received message; ignoring", slog.Int("messageType", messageType), slog.String("message", string(message)))
	}
}

// handles messages to a connected client
func (c *connection) writer(errCh chan bool) {
	for msg := range c.send {
		err := c.ws.WriteMessage(websocket.BinaryMessage, msg)
		if err != nil {
			slog.Error("connection: Error writing message to websocket: ", err)
			errCh <- true
			break
		}
	}
}

// Echo the data received on the WebSocket.
// WebSocket handler. It perform user authentication, upgrades connection
// to websocket and spawns goroutines to handle data transfers.
func (wsh *webSocketHandler) Handler(w http.ResponseWriter, r *http.Request) {

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("connection: Error upgrading connection to websocket: ", err)
		return
	}
	defer ws.Close()

	// we have a initialized websocket connection.
	c := &connection{ws, make(chan []byte, 10)}

	slog.Debug("connection: Got connection")
	// put it in the registration channel for the hub to take it.
	wsh.register <- c
	// create error channel. It will be used in case of errors to
	// end the connection
	errorCh := make(chan bool)
	defer func() {
		wsh.unregister <- c
	}()
	// spawn go routing to send/receive data
	go c.reader(errorCh)
	go c.writer(errorCh)
	// wait for errors or connection end
	<-errorCh
}

// Main worker loop. Three things can happen: (i) we got a new
// connection from a client. Handler created connection object and
// sent it in the connections channel. Connection is stored in
// connections map in order to know to who send data. (ii) Connection
// is closed, we remove the object from connections map. (iii) We got
// a message from rabbitmq: we iterate over connections map and send
// the message in each connection channel for the writer goroutine to
// pick it up.
func (wsh *webSocketHandler) run() {
	for {
		select {
		case c := <-wsh.register:
			wsh.connections[c] = true
			slog.Debug("webSocketHandler: Register call", slog.Int("number of connections", len(wsh.connections)))
			if wsh.connectionCount != nil {
				wsh.connectionCount <- len(wsh.connections)
			}

		case c := <-wsh.unregister:
			if _, ok := wsh.connections[c]; ok {
				delete(wsh.connections, c)
				close(c.send)
			}
			slog.Debug("webSocketHandler: Unregister call", slog.Int("number of connections", len(wsh.connections)))
			if wsh.connectionCount != nil {
				wsh.connectionCount <- len(wsh.connections)
			}

		case msg := <-wsh.broadcast:
			for c := range wsh.connections {
				select {
				case c.send <- msg:
					continue
				case <-time.After(100 * time.Millisecond):
					slog.Warn("webSocketHandler: Timeout sending message to connection")
					// skip message if timeout
				}
			}
		}
	}
}

// Send puts message body into the queue of messages that have to be
// broadcasted to clients.
func (wsh *webSocketHandler) Write(data []byte) (int, error) {
	// Optimization: don't send if there is no connection
	if len(wsh.connections) <= 0 {
		return 0, nil
	}

	wsh.broadcast <- data
	return len(data), nil
}

// NewWebSocketHandler builds new websocket handler to communicate upstream
func NewWebSocketHandler(connectionCount chan int) WebSocketHandler {
	wsh := webSocketHandler{
		broadcast:       make(chan []byte),
		register:        make(chan *connection),
		unregister:      make(chan *connection),
		connections:     make(map[*connection]bool),
		connectionCount: connectionCount,
	}

	go wsh.run()
	return &wsh
}
