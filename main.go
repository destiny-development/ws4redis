package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/gorilla/websocket"
)

const (
	version        = "1.3-production"
	facilityPrefix = "broadcast"
	keySeparator   = ":"
	attemptWait    = time.Second * 1
)

var (
	port int
	host string

	redisAddr     string
	redisDatabase int64
	redisNetwork  string
	redisPrefix   string

	strictMode          bool
	heartbeats          bool
	scaleCPU            bool
	permittedFacilities string
	defaultFacility     string
	clientTimeOut       time.Duration
	maxEchoSize         int64

	staticFacilities = make(map[string]bool)
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // allow any origin
}

// Message is container for messages between server and clients
type Message []byte

// MessageChan is channel of messages
type MessageChan chan Message

// Facilities is map[name]Pointer-to-facility
// should be locked on change (used async)
type Facilities map[string]*Facility

// Clients must be locked too, used as list of clients
// with easy way to add/remove client
type Clients map[MessageChan]bool

// Application contains main logic
type Application struct {
	facilities Facilities
	l          sync.Locker
}

// Facility creates, initializes and returns new facility with provided name
func (a *Application) Facility(name string) (f *Facility) {
	// lock only in non-strict mode
	if !strictMode {
		a.l.Lock()
		defer a.l.Unlock()
	}
	f, ok := a.facilities[name]
	if ok {
		return f
	}
	f = NewFacility(name)
	a.facilities[name] = f
	return f
}

// FacilityFromURL wraps Application.Facility
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
	defer conn.Close()
	conn.SetReadLimit(maxEchoSize)

	// log.Println("connected", r.RemoteAddr, r.URL)
	f := a.FacilityFromURL(r.URL)
	c := f.Subscribe()
	defer f.Unsubscibe(c)

	// listening for data from redis
	go func() {
		for message := range c {
			conn.WriteMessage(websocket.TextMessage, message)
		}
	}()

	// handling heartbeat
	// listening until conn timedout/error/closed or heatbeat timeout
	for {
		conn.SetReadDeadline(time.Now().Add(clientTimeOut))
		t, data, err := conn.ReadMessage()
		// log.Println("got message", t, err)
		if t != websocket.TextMessage {
			return
		}
		if err != nil {
			return
		}
		if conn.WriteMessage(websocket.TextMessage, data) != nil {
			return
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

// New creates and initializes new Application
func New() *Application {
	a := new(Application)

	// testing redis connection
	conn, err := redis.Dial(redisNetwork, redisAddr)
	if err != nil {
		log.Fatal("Redis error:", err)
	}
	// testing database select
	_, err = conn.Do("select", redisDatabase)
	if err != nil {
		log.Fatal("Redis db change error:", err)
	}

	a.facilities = make(Facilities)
	a.l = new(sync.Mutex)

	// pre-initialize facilities in strict mode
	if strictMode {
		facilities := strings.Split(permittedFacilities, ",")
		for _, name := range facilities {
			staticFacilities[name] = true
			a.Facility(name)
		}
		if !staticFacilities[defaultFacility] {
			log.Fatalln("default facility", defaultFacility, "not in", permittedFacilities)
		}
	}
	return a
}

func init() {
	flag.IntVar(&port, "port", 9050, "Listen port")
	flag.Int64Var(&redisDatabase, "redis-db", 0, "Redis db")
	flag.StringVar(&redisNetwork, "redis-network", "tcp", "Redis network")
	flag.StringVar(&redisAddr, "redis-addr", "localhost:6379", "Redis addr")
	flag.StringVar(&redisPrefix, "redis-prefix", "ws", "Redis prefix")
	flag.BoolVar(&strictMode, "strict", false, "Allow only white-listed facilities")
	flag.StringVar(&permittedFacilities, "facilities", "launcher,launcher-staff", "Permitted facilities for strict mode")
	flag.StringVar(&defaultFacility, "default", "launcher", "Default facility")
	flag.BoolVar(&heartbeats, "heartbeats", false, "Use heartbeats")
	flag.BoolVar(&scaleCPU, "scale", false, "Use all cpus")
	flag.DurationVar(&clientTimeOut, "timeout", time.Second*10, "Heartbeat timeout")
	flag.Int64Var(&maxEchoSize, "max-size", 32, "Maximum message size")
}

func main() {
	flag.Parse()

	// initialize
	a := New()
	if scaleCPU {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}
	http.HandleFunc("/", a.handler)
	http.HandleFunc("/stat", a.stat)
	listenOn := fmt.Sprintf("%s:%d", host, port)

	// print information about modes/version
	log.Println("ws4redis", version)
	log.Println("listening on", listenOn)
	if strictMode {
		log.Println("running in strict mode")
	}
	if !scaleCPU {
		log.Println("running on single core")
	}

	// block until error/kill
	log.Fatal(http.ListenAndServe(listenOn, nil))
}
