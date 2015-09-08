package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"expvar"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"runtime"
	rpprof "runtime/pprof"
	"strings"
	"sync"
	"syscall"
	"time"

	"bufio"
	"strconv"

	"github.com/gorilla/websocket"
	"github.com/paulbellamy/ratecounter"
)

const (
	version              = "2.1-stable"
	secureFacilityPrefix = "secure"
	keySeparator         = ":"
	attemptWait          = time.Second * 1
	heartbeatMessage     = "ping"
	facilityPrefix       = "broadcast"
)

var (
	port int
	host string

	// debug flags
	profileCPU bool
	profile    bool

	redisAddr     string
	redisDatabase int64
	redisNetwork  string
	redisPrefix   string

	strictMode          bool
	heartbeats          bool
	scaleCPU            bool
	permittedFacilities string
	defaultFacility     string
	secret              []byte
	secretFilePath      string
	clientTimeOut       time.Duration
	timeStampDelta      time.Duration
	maxEchoSize         int64
	heartbeat           = bytes.NewBufferString(heartbeatMessage).Bytes()

	errTokenIncorrect = errors.New("Error: token incorrect")
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
	factory  *RedisFactory
	access   sync.Locker // facilities lock
	mux      *http.ServeMux
	listenOn string
	rate     *ratecounter.RateCounter
}

// Facility creates, initializes and returns new facility with provided name
func (a *Application) Facility(name string) (f *Facility) {
	return a.factory.Facility(name)
}

func tokenCorrect(name string, u *url.URL, deadline time.Time) bool {
	q := u.Query()
	token := q.Get("token")
	timestampS := q.Get("ts")
	timestamp, err := strconv.ParseInt(timestampS, 10, 64)
	if err != nil {
		// log.Println("warning:", "unable to convert timestamp")
		return false
	}

	tokenTime := time.Unix(timestamp, 0)
	delta := deadline.Sub(tokenTime)
	if delta < 0 {
		delta *= -1
	}

	// log.Println(tokenTime.Unix(), time.Now().Unix(), deadline.Unix())
	if delta > timeStampDelta {
		log.Println("warning: bad delta", tokenTime.Unix(), time.Now().Unix(), deadline.Unix())
		return false
	}

	hash := sha256.New()
	hash.Write(secret)
	fmt.Fprint(hash, name, timestampS)
	expectedToken := fmt.Sprintf("%x", hash.Sum(nil))
	if expectedToken != token {
		// log.Println("warning: unexpected token", token, "should be", expectedToken)
		return false
	}

	return true
}

// FacilityFromURL wraps Application.Facility
func (a *Application) FacilityFromURL(u *url.URL) (f *Facility, err error) {
	name := u.Path[strings.LastIndex(u.Path, "/")+1:]
	if strings.Index(name, secureFacilityPrefix) == 0 {
		// checking secure facility
		if !tokenCorrect(name, u, time.Now().Add(-timeStampDelta)) {
			return nil, errTokenIncorrect
		}
	}
	return a.Facility(name), nil
}

func (a Application) clients() interface{} {
	var totalClients int64
	for _, f := range a.factory.facilities {
		totalClients += int64(len(f.clients))
	}
	return totalClients
}

func (a Application) requestsPerSecond() interface{} {
	return a.rate.Rate()
}

func (a Application) handler(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			if rec == io.EOF {
				// ignoring EOF errors, they are expected
				return
			}
			log.Println("recovered:", rec)
		}
	}()

	// log.Println("connected", r.RemoteAddr, r.URL)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// failed to upgrade connection to ws protocol
		fmt.Fprintln(w, "Error: Failed to upgrade")
		return
	}

	f, err := a.FacilityFromURL(r.URL)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintln(w, "Error: Permission denied")
		return
	}

	defer func() {
		if err := conn.Close(); err != nil {
			log.Println("conn.Close:", err)
		}
	}()
	conn.SetReadLimit(maxEchoSize)

	a.rate.Incr(1)
	c := f.Get()
	defer a.factory.RemoveClient(f, c)

	// listening for data from redis
	go func() {
		for message := range c {
			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				break
			}
		}
	}()

	// handling heartbeat
	// listening until conn timed-out/error/closed or heartbeat timeout
	for {
		deadline := time.Now().Add(clientTimeOut)
		if err := conn.SetReadDeadline(deadline); err != nil {
			break
		}
		t, rd, err := conn.NextReader()
		if err != nil {
			break
		}
		if _, err := io.Copy(ioutil.Discard, rd); err != nil {
			break
		}
		if t != websocket.TextMessage {
			continue
		}
		if err := conn.WriteMessage(websocket.TextMessage, heartbeat); err != nil {
			break
		}
	}
}

func (a Application) muninEndpoint(w http.ResponseWriter, r *http.Request) {
	var totalClients int
	for _, f := range a.factory.facilities {
		totalClients += len(f.clients)
	}
	fmt.Fprint(w, totalClients)
}

// statistics information
func (a Application) stat(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ws4redis")
	fmt.Fprintln(w, "Version", version)

	fmt.Fprintln(w, "Facilities", len(a.factory.facilities))
	var totalClients int64
	for name, f := range a.factory.facilities {
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
	factory, err := NewRedisFactory()
	if err != nil {
		panic(err)
	}
	a.factory = factory
	a.access = new(sync.Mutex)

	if profile {
		mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
		mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
		mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
		mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
		// mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	}

	mux.Handle("/debug/vars", http.DefaultServeMux)
	mux.HandleFunc("/", a.handler)
	mux.HandleFunc("/stat", a.stat)
	mux.HandleFunc("/monitoring_endpoint/", a.muninEndpoint)
	listenOn := fmt.Sprintf("%s:%d", host, port)

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
	flag.BoolVar(&strictMode, "strict", false, "Allow only white-listed facilities DEPRECATED")
	flag.StringVar(&permittedFacilities, "facilities", "launcher,launcher-staff", "Permitted facilities for strict mode")
	flag.StringVar(&defaultFacility, "default", "launcher", "Default facility")
	flag.BoolVar(&heartbeats, "heartbeats", false, "Use heartbeats DEPRECATED")
	flag.BoolVar(&scaleCPU, "scale", false, "Use all cpus DEPRECATED")
	flag.DurationVar(&clientTimeOut, "timeout", time.Second*10, "Heartbeat timeout")
	flag.Int64Var(&maxEchoSize, "max-size", 32, "Maximum message size")
	flag.BoolVar(&profileCPU, "profile-cpu", false, "Profile cpu")
	flag.BoolVar(&profile, "profile", true, "Profile with pprof endpoint")
	flag.DurationVar(&timeStampDelta, "timestamp-delta", time.Minute*10, "Maximum timestamp delta")
	flag.StringVar(&secretFilePath, "secret", "/home/tera/ws4redis/secret", "Filepath to text file with secret phrase")
}

func getApplication() *Application {
	a := New()
	fmt.Println("ws4redis", version)
	fmt.Println("listening on", a.listenOn)
	fmt.Println("running in unstrict scaling mode with heartbeats")
	return a
}

func printLimits() {
	var rLimit syscall.Rlimit
	err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	if err != nil {
		fmt.Println("Error Getting Rlimit ", err)
	}
	fmt.Println("File limit:", rLimit.Cur, "of", rLimit.Max)
}

func main() {
	printLimits()
	flag.Parse()
	if profileCPU {
		f, err := os.Create("ws4redis-cpu.prof")
		if err != nil {
			log.Fatal(err)
		}
		rpprof.StartCPUProfile(f)
		defer rpprof.StopCPUProfile()
	}
	f, err := os.Open(secretFilePath)
	if err != nil {
		log.Fatalln("unable to open secret file", err)
	}
	secret, _, err = bufio.NewReader(f).ReadLine()
	if err != nil {
		log.Fatalln("unable to read secret", err)
	}
	f.Close()
	a := getApplication()
	expvar.Publish("Clients", expvar.Func(a.clients))
	expvar.Publish("Goroutines", expvar.Func(goroutines))
	expvar.Publish("RPS", expvar.Func(a.requestsPerSecond))
	expvar.Publish("Version", expvar.Func(currentVersion))
	log.Fatal(http.ListenAndServe(a.listenOn, a))
}
