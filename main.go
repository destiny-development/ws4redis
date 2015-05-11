package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"expvar"

	"github.com/gorilla/websocket"
	"github.com/paulbellamy/ratecounter"
)

const (
	version        = "1.5.5-production"
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
	mux        *http.ServeMux // facilities lock
	listenOn   string
	rate       *ratecounter.RateCounter
}

// Lock facilities list
func (a Application) Lock() {
	a.l.Lock()
}

// Unlock facilities list
func (a Application) Unlock() {
	a.l.Unlock()
}

// Facility creates, initializes and returns new facility with provided name
func (a *Application) Facility(name string) (f *Facility) {
	// lock only in non-strict mode
	if !strictMode {
		a.Lock()
		defer a.Unlock()
	}
	f, ok := a.facilities[name]
	if ok {
		return f
	}
	f = NewRedisFacility(name)
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

func (a Application) clients() interface{} {
	var totalClients int64
	for _, f := range a.facilities {
		totalClients += int64(len(f.clients))
	}
	return totalClients
}

func (a Application) requestsPerSecond() interface{} {
	return a.rate.Rate()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func (a Application) handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	defer func() {
		recover()
	}()
	must(err)
	defer conn.Close()
	conn.SetReadLimit(maxEchoSize)
	a.rate.Incr(1)

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
		t, rd, err := conn.NextReader()
		must(err)
		if t != websocket.TextMessage {
			continue
		}
		wr, err := conn.NextWriter(t)
		_, err = io.Copy(wr, rd)
		must(err)
		must(wr.Close())
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
	a.rate = ratecounter.NewRateCounter(time.Second)
	mux := http.NewServeMux()
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

	if scaleCPU {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	mux.Handle("/debug/vars", http.DefaultServeMux)
	mux.HandleFunc("/", a.handler)
	mux.HandleFunc("/stat", a.stat)
	listenOn := fmt.Sprintf("%s:%d", host, port)

	// print information about modes/version
	a.mux = mux
	a.listenOn = listenOn
	return a
}

// ServeHTTP is proxy to a.mux.ServeHTTP
func (a *Application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mux.ServeHTTP(w, r)
}

// goroutines is an expvar.Func compliant wrapper for runtime.NumGoroutine function.
func goroutines() interface{} {
	return runtime.NumGoroutine()
}

func currentVersion() interface{} {
	return version
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

func getApplication() *Application {
	a := New()
	fmt.Println("ws4redis", version)
	fmt.Println("listening on", a.listenOn)
	if !scaleCPU {
		fmt.Println("warning: using one core")
	}
	if !strictMode {
		fmt.Println("warning: running in unstrict mode")
	} else {
		fmt.Println("running in strict mode; allowed facilities:", permittedFacilities)
	}
	return a
}

func main() {
	flag.Parse()
	a := getApplication()
	expvar.Publish("Clients", expvar.Func(a.clients))
	expvar.Publish("Goroutines", expvar.Func(goroutines))
	expvar.Publish("RPS", expvar.Func(a.requestsPerSecond))
	expvar.Publish("Version", expvar.Func(currentVersion))
	log.Fatal(http.ListenAndServe(a.listenOn, a))
}
