package main

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
)

// MessageProvider is source of messages
type MessageProvider interface {
	Channel() MessageChan
}

// RedisMessageProvider implements MessageProvider
type RedisMessageProvider struct {
	conn     redis.PubSubConn
	key      string
	messages MessageChan
}

func (p *RedisMessageProvider) connect() error {
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
	if err := psc.Subscribe(p.key); err != nil {
		return err
	}
	p.conn = psc
	return nil
}

func (p RedisMessageProvider) loop() {
	for {
		switch v := p.conn.Receive().(type) {
		case redis.Message:
			p.messages <- v.Data
		case error:
			if err := p.connect(); err != nil {
				log.Println("RedisMessageProvider.loop: connect", err)
			}
			time.Sleep(attemptWait)
		}
	}
}

// Channel returns MessageChan with messages from redis channel
func (p RedisMessageProvider) Channel() MessageChan {
	return p.messages
}

// NewRedisProvider creates new redis connection and sends new messages
// to channel
func NewRedisProvider(name string) MessageProvider {
	p := &RedisMessageProvider{key: name}
	p.messages = make(MessageChan)
	if err := p.connect(); err != nil {
		panic(err)
	}
	go p.loop()
	return p
}

// Facility represents channel, which users can listen
// and server can broadcast message to all subscribed users
type Facility struct {
	name     string      // name of facility
	channel  MessageChan // broadcast channel
	clients  Clients     // client channels
	access   sync.Locker // lock for clients
	provider MessageProvider
}

// NewFacility creates facility, starts redis and broadcast loops
// and returns initialized *Facility
func NewFacility(name string, provider MessageProvider) *Facility {
	f := new(Facility)
	f.channel = provider.Channel()
	f.clients = make(Clients)
	f.access = new(sync.Mutex)
	f.name = name
	go f.loop()
	return f
}

// NewRedisFacility creates facility with redis provider
func NewRedisFacility(name string) *Facility {
	key := strings.Join([]string{redisPrefix, facilityPrefix, name}, keySeparator)
	provider := NewRedisProvider(key)
	return NewFacility(name, provider)
}

// broadcast loop
func (f *Facility) loop() {
	// for every message in channel
	for s := range f.channel {
		log.Printf("[%s] broadcasting", f.name)
		// async broadcast to all clients of facility
		// locking changes to client list
		f.access.Lock()
		for client := range f.clients {
			client <- s
		}
		f.access.Unlock()
		log.Printf("[%s] broadcasted", f.name)
	}
}

// Get creates new subscription channel and returns it
// adding it to facility clients
func (f *Facility) Get() (m MessageChan) {
	m = make(MessageChan)
	f.access.Lock()
	f.clients[m] = true
	f.access.Unlock()
	return m
}

// Remove removes channel from facility clients and closes it
func (f *Facility) Remove(m MessageChan) {
	f.access.Lock()
	delete(f.clients, m)
	f.access.Unlock()
	close(m)
}
