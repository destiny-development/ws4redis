package main

import (
	"flag"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"time"
)

var (
	port          int
	host          string
	redisAddr     string
	redisDatabase int64
	redisNetwork  string = "tcp"
	redisPrefix   string
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // allow any origin
}

type Application struct {
	conn redis.Conn
}

func (a Application) handler(w http.ResponseWriter, r *http.Request) {
	// /ws/launcher-staff?subscribe-session&subscribe-broadcast
	// /ws/launcher?subscribe-session&subscribe-broadcast
	// /ws/{facility}?...
	log.Println(r.URL)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()
	conn.WriteJSON(map[string]string{"hello": "world"})
	time.Sleep(time.Second * 15)
}

func New() *Application {
	a := new(Application)
	conn, err := redis.Dial(redisNetwork, redisAddr)
	if err != nil {
		log.Fatal("Redis error", err)
	}
	conn.Do("set", "kek", "pek")
	return a
}

func (a Application) loop() {

}

func init() {
	flag.IntVar(&port, "port", 9050, "Listen port")
	flag.Int64Var(&redisDatabase, "redis-db", 10, "Redis db")
	flag.StringVar(&redisNetwork, "redis-network", "tcp", "Redis network")
	flag.StringVar(&redisAddr, "redis-addr", "localhost:6379", "Redis addr")
	flag.StringVar(&redisPrefix, "redis-prefix", "ws", "Redis prefix")
}

func main() {
	a := New()
	http.HandleFunc("/", a.handler)
	listenOn := fmt.Sprintf("%s:%d", host, port)
	go a.loop()
	log.Fatal(http.ListenAndServe(listenOn, nil))
}
