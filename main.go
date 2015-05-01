package main

import (
	"flag"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	version        = "1.0rc"
	facilityPrefix = "broadcast"
	keySeparator   = ":"
	maxEchoSize    = 256
	attemptWait    = time.Second * 1
	clientTimeOut  = time.Second * 15
)

var (
	port int
	host string

	redisAddr     string
	redisDatabase int64
	redisNetwork  string
	redisPrefix   string

	strictMode       bool
	staticFacilities = map[string]bool{"launcher": true, "launcher-staff": true}
	defaultFacility  = "launcher"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // allow any origin
}

type Message []byte

type MessageChan chan Message

type Application struct {
	facilities map[string]*Facility
	l          sync.Locker
}

func (a *Application) Facility(name string) (f *Facility) {
	a.l.Lock()
	defer a.l.Unlock()
	f, ok := a.facilities[name]
	if ok {
		return f
	}
	f = NewFacility(name)
	a.facilities[name] = f
	return f
}

func (a *Application) FacilityFromURL(u *url.URL) (f *Facility) {
	name := getFacility(u)
	if strictMode && !staticFacilities[name] {
		name = defaultFacility
	}
	return a.Facility(name)
}

func getFacility(u *url.URL) string {
	return u.Path[strings.LastIndex(u.Path, "/")+1:]
}

func (a Application) handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f := a.FacilityFromURL(r.URL)
	c := f.Subscribe()
	stop := make(chan bool)
	defer close(stop)

	// listening for data from redis
	go func() {
		for message := range c {
			conn.WriteMessage(websocket.TextMessage, message)
		}
	}()

	// closing connection and removing subscibtion after stop
	go func() {
		<-stop
		conn.Close()
		f.Unsubscibe(c)
	}()

	// handling heartbeat
	// listening until conn timedout/error/closed or heatbeat timeout
	heartBeats := make(chan bool)
	go func() {
		defer close(heartBeats)
		for {
			t, data, err := conn.ReadMessage()
			if t != websocket.TextMessage {
				return
			}
			if err != nil {
				return
			}
			// possible DoS prevention
			if len(data) > maxEchoSize {
				return
			}
			if conn.WriteMessage(websocket.TextMessage, data) != nil {
				return
			}
			heartBeats <- true
		}
	}()

	// handling heartbeat timeouts and errors
	for {
		timeout := time.NewTimer(clientTimeOut)
		select {
		case _, ok := <-heartBeats:
			if ok {
				continue
			}
			break
		case <-timeout.C:
			break
		}
	}
}

// statistics information
func (a Application) stat(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ws4redis")
	fmt.Fprintln(w, "Version", version)

	fmt.Fprintln(w, "Facilities", len(a.facilities))
	var totalClients int64
	for name, f := range a.facilities {
		fmt.Fprintf(w, "\tfacility %s:\n", name)
		clients := len(f.clients)
		fmt.Fprintf(w, "\t\t %d clients\n", clients)
		totalClients += int64(clients)
	}
	fmt.Fprintln(w, "Clients", totalClients)

	fmt.Fprintln(w, "CPU", runtime.NumCPU())
	fmt.Fprintln(w, "Goroutines", runtime.NumGoroutine())

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Fprintln(w, "Memory")
	fmt.Fprintln(w, "\tAlloc", mem.Alloc)
	fmt.Fprintln(w, "\tTotalAlloc", mem.TotalAlloc)
	fmt.Fprintln(w, "\tHeap", mem.HeapAlloc)
	fmt.Fprintln(w, "\tHeapSys", mem.HeapSys)
}

func New() *Application {
	a := new(Application)

	// testing redis connection
	conn, err := redis.Dial(redisNetwork, redisAddr)
	if err != nil {
		log.Fatal("Redis error:", err)
	}
	_, err = conn.Do("select", redisDatabase)
	if err != nil {
		log.Fatal("Redis db change error:", err)
	}

	a.facilities = make(map[string]*Facility)
	a.l = new(sync.Mutex)
	return a
}

func init() {
	flag.IntVar(&port, "port", 9050, "Listen port")
	flag.Int64Var(&redisDatabase, "redis-db", 0, "Redis db")
	flag.StringVar(&redisNetwork, "redis-network", "tcp", "Redis network")
	flag.StringVar(&redisAddr, "redis-addr", "localhost:6379", "Redis addr")
	flag.StringVar(&redisPrefix, "redis-prefix", "ws", "Redis prefix")
	flag.BoolVar(&strictMode, "strict", false, "Allow only white-listed facilities")
}

func main() {
	flag.Parse()
	a := New()
	runtime.GOMAXPROCS(runtime.NumCPU())
	http.HandleFunc("/", a.handler)
	http.HandleFunc("/stat", a.stat)
	listenOn := fmt.Sprintf("%s:%d", host, port)
	log.Println("ws4redis", version)
	log.Println("listening on", listenOn)
	log.Fatal(http.ListenAndServe(listenOn, nil))
}
