package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var (
	heartbeatMessage  string
	heartbeatDuration time.Duration
	reconnectWait     time.Duration
	maxFailedAttempts int
	port              int
	host              string
	uri               string
)

const (
	StatusStarting           = "starting"
	StatusConnectFailed      = "connect failed"
	StatusOk                 = "operational"
	StatusConnecting         = "connecting"
	StatusWaitingForResponse = "waiting for response"
)

func init() {
	flag.StringVar(&heartbeatMessage, "heartbeat-message", "ping", "Hearbeat message")
	flag.DurationVar(&heartbeatDuration, "interval", time.Millisecond*500, "Check interval")
	flag.DurationVar(&reconnectWait, "reconnect-wait", time.Millisecond*350, "Interval between reconnections")
	flag.IntVar(&maxFailedAttempts, "attempts", 3, "Maximum failed hearbeat attempts")
	flag.IntVar(&port, "port", 8080, "Listen port")
	flag.StringVar(&host, "host", "", "Listen host")
	flag.StringVar(&uri, "uri", "ws://localhost:9050", "Check URI")
}

type Application struct {
	conn    *websocket.Conn
	ok      bool
	status  string
	attempt int
	message []byte
}

func (a *Application) handler(w http.ResponseWriter, r *http.Request) {
	if a.ok {
		fmt.Fprintln(w, "OK", a.status)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "FAILED", a.status)
	}
}

func (a *Application) connect() error {
	a.ok = false
	a.status = StatusConnecting
	log.Println("connecting to", uri)
	conn, _, err := websocket.DefaultDialer.Dial(uri, nil)
	if err != nil {
		log.Println("connection failed", err)
		a.status = StatusConnectFailed
		return err
	}
	log.Println("connected")
	a.status = StatusWaitingForResponse
	a.conn = conn
	return nil
}

func (a *Application) reconnect() {
	toWait := reconnectWait
	a.ok = false
	for {
		if err := a.connect(); err != nil {
			log.Println("failed to connect", err)
		} else {
			break
		}
		time.Sleep(toWait)
	}
}

func (a *Application) checkUntilFail() {
	ticker := time.NewTicker(heartbeatDuration)
	a.attempt = 0
	for _ = range ticker.C {
		a.attempt++
		if a.attempt > maxFailedAttempts {
			ticker.Stop()
			return
		}
		if err := a.conn.WriteMessage(websocket.TextMessage, a.message); err != nil {
			log.Println("WriteMessage", err)
			continue
		}
		if err := a.conn.SetReadDeadline(time.Now().Add(heartbeatDuration * 2)); err != nil {
			log.Println("SetReadDeadline", err)
			continue
		}
		t, m, err := a.conn.ReadMessage()
		if err != nil {
			log.Println("ReadMessage", err)
			continue
		}
		if t != websocket.TextMessage {
			log.Println("bad message type", t)
			continue
		}
		if bytes.NewBuffer(m).String() != heartbeatMessage {
			log.Println("bad heartbeat message")
			continue
		}
		a.status = StatusOk
		a.ok = true
		a.attempt = 0
	}
}

func (a *Application) loop() {
	for {
		a.reconnect()
		a.checkUntilFail()
	}
}

func (a *Application) start() error {
	listenOn := fmt.Sprintf("%s:%d", host, port)
	http.HandleFunc("/", a.handler)
	a.status = StatusStarting
	a.message = []byte(heartbeatMessage)
	go a.loop()
	return http.ListenAndServe(listenOn, nil)
}

func main() {
	flag.Parse()
	a := new(Application)
	log.Fatal(a.start())
}
