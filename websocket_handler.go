package main

import (
	"io"
	"log"
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
	connections      map[*connection]bool // Registered connections.
	broadcast        chan []byte          // Inbound messages from the connections.
	register         chan *connection     // Register requests from the connections.
	unregister       chan *connection     // Unregister requests from connections.
	connectionNumber chan int
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
			log.Println("[Reader] Error", err)
			defer func() { errCh <- true }()
			return
		}

		log.Println("Received message ", messageType, message, err)
		log.Println("Received message " + string(message))

		//parseMessageSent(string(message))
	}
}

// handles messages to a connected client
func (c *connection) writer(errCh chan bool) {
	for msg := range c.send {
		err := c.ws.WriteMessage(websocket.BinaryMessage, msg)
		if err != nil {
			log.Println("[Writer] Error", err)
			defer func() { errCh <- true }()
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
		log.Println("Got error while upgrading connection", err)
		return
	}
	defer ws.Close()

	// we have a initialized websocket connection.
	c := &connection{ws, make(chan []byte, 10)}

	log.Println("Got connection")
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
			log.Println("[hub] register")
			wsh.connections[c] = true
			log.Println("Register call end -> Number of current connections: ", len(wsh.connections))
			if wsh.connectionNumber != nil {
				wsh.connectionNumber <- len(wsh.connections)
			}
			break

		case c := <-wsh.unregister:
			log.Println("[hub] unregister")
			if _, ok := wsh.connections[c]; ok {
				delete(wsh.connections, c)
				close(c.send)
			}
			log.Println("Unregister call end -> Number of current connections: ", len(wsh.connections))
			if wsh.connectionNumber != nil {
				wsh.connectionNumber <- len(wsh.connections)
			}
			break

		case msg := <-wsh.broadcast:
			for c := range wsh.connections {
				select {
				case c.send <- msg:
					continue
				case <-time.After(100 * time.Millisecond):
					log.Println("[WSHandler] Skipping message to connection")
					// skip message if timeout
				}
			}
			break
		}
	}
}

// Send puts message body into the queue of messages that have to be
// broadcasted to clients.
func (wsh *webSocketHandler) Write(data []byte) (n int, err error) {
	// Optimization: don't send if there is no connection
	if len(wsh.connections) <= 0 {
		return
	}

	wsh.broadcast <- data
	n = len(data)
	return
}

// NewWebSocketHandler builds new websocket handler to communicate upstream
func NewWebSocketHandler(connectionNumber chan int) WebSocketHandler {
	wsh := webSocketHandler{
		broadcast:        make(chan []byte),
		register:         make(chan *connection),
		unregister:       make(chan *connection),
		connections:      make(map[*connection]bool),
		connectionNumber: connectionNumber,
	}

	go wsh.run()
	return &wsh
}
