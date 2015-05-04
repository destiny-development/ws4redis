package main

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
)

// Facility represents channel, which users can listen
// and server can broadcast message to all subscribed users
type Facility struct {
	name    string               // name of facility
	channel MessageChan          // broadcast channel
	clients map[MessageChan]bool // client channels
	l       sync.Locker          // lock for clients
}

// NewFacility creates facility, starts redis and broadcast loops
// and returns initialized *Facility
func NewFacility(name string) *Facility {
	f := new(Facility)
	f.channel = make(MessageChan)
	f.clients = make(map[MessageChan]bool)
	f.l = new(sync.Mutex)
	f.name = name
	go f.loop()
	go f.redisLoop()
	return f
}

// broadcast loop
func (f *Facility) loop() {
	// for every message in channel
	for s := range f.channel {
		log.Println("facility: got message; broadcasting")
		// async broadcast to all clients of facility
		f.l.Lock()
		for client := range f.clients {
			client <- s
		}
		f.l.Unlock()
		log.Println("facility: broadcast ended")
	}
}

// listen to facility key in redis and broadcast all data
func (f *Facility) listenRedis() error {
	conn, err := redis.Dial(redisNetwork, redisAddr)
	if err != nil {
		return err
	}
	conn.Do("select", redisDatabase)
	psc := redis.PubSubConn{Conn: conn}
	k := strings.Join([]string{redisPrefix, facilityPrefix, f.name}, keySeparator)
	log.Println("redis: listening to", k)
	psc.Subscribe(k)
	for {
		switch v := psc.Receive().(type) {
		case redis.Message:
			log.Println("redis: got message; broadcasting")
			f.channel <- v.Data
		case error:
			return err
		}
	}
}

// redis reconnection loop
func (f *Facility) redisLoop() {
	for {
		if err := f.listenRedis(); err != nil {
			log.Println("Redis error:", err)
			time.Sleep(attemptWait)
		}
	}
}

// Subscribe creates new subscription channel and returns it
// adding it to facility clients
func (f *Facility) Subscribe() (m MessageChan) {
	m = make(MessageChan)
	f.l.Lock()
	f.clients[m] = true
	f.l.Unlock()
	return m
}

// Unsubscibe removes channel from facility clients and closes it
func (f *Facility) Unsubscibe(m MessageChan) {
	f.l.Lock()
	delete(f.clients, m)
	f.l.Unlock()
	close(m)
}
