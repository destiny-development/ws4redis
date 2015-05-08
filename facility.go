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
	name    string      // name of facility
	channel MessageChan // broadcast channel
	clients Clients     // client channels
	l       sync.Locker // lock for clients
}

// NewFacility creates facility, starts redis and broadcast loops
// and returns initialized *Facility
func NewFacility(name string) *Facility {
	f := new(Facility)
	f.channel = make(MessageChan)
	f.clients = make(Clients)
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
		log.Printf("[%s] got message", f.name)
		start := time.Now()
		// async broadcast to all clients of facility
		// locking changes to client list
		f.l.Lock()
		for client := range f.clients {
			client <- s
		}
		f.l.Unlock()
		duration := time.Now().Sub(start)
		log.Printf("[%s] broadcasted for %v", f.name, duration)
	}
}

// listen to facility key in redis and broadcast all data
func (f *Facility) listenRedis() error {
	// one connection per facility (for simplification purposes)
	conn, err := redis.Dial(redisNetwork, redisAddr)
	if err != nil {
		return err
	}
	// selecting database
	_, err = conn.Do("select", redisDatabase)
	if err != nil {
		return err
	}
	psc := redis.PubSubConn{Conn: conn}
	// creating
	k := strings.Join([]string{redisPrefix, facilityPrefix, f.name}, keySeparator)
	psc.Subscribe(k)
	log.Printf("[%s] [redis] listening to channel %s", f.name, k)
	for {
		switch v := psc.Receive().(type) {
		case redis.Message:
			log.Printf("[%s] [redis] message: %s", f.name, string(v.Data))
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
			log.Printf("[%s] [redis] error: %s; sleeping for: %v;", f.name, err, attemptWait)
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
