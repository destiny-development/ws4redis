package main

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
)

const (
	messagesChanSize = 10
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

type RedisFactory struct {
	conn       redis.PubSubConn
	key        string
	messages   map[string]MessageChan
	facilities Facilities
	access     sync.Locker // lock for clients
}

func NewRedisFactory() (*RedisFactory, error) {
	f := new(RedisFactory)
	if err := f.connect(); err != nil {
		return nil, err
	}
	f.access = new(sync.Mutex)
	f.messages = make(map[string]MessageChan)
	f.facilities = make(Facilities)

	go f.loop()
	go f.gc()

	return f, nil
}

func (f *RedisFactory) connect() error {
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
	log.Println(redisNetwork, redisAddr, redisDatabase)
	f.conn = psc
	return nil
}

func (p *RedisFactory) Facility(name string) *Facility {
	p.access.Lock()
	facility, ok := p.facilities[name]
	if !ok {
		key := getFacilityKey(name)
		log.Println("listening", key)
		if err := p.conn.Subscribe(key); err != nil {
			panic(err)
		}
		messages := make(chan Message, messagesChanSize)
		facility = new(Facility)
		facility.channel = messages
		facility.clients = make(Clients)
		facility.access = new(sync.Mutex)
		facility.name = name
		p.messages[key] = messages
		go facility.loop()
		p.facilities[name] = facility
	}
	p.access.Unlock()
	return facility
}

const (
	gcTime = time.Second
)

func (p RedisFactory) gc() {
	ticker := time.NewTicker(gcTime)
	for _ = range ticker.C {
		p.access.Lock()
		for _, f := range p.facilities {
			if len(f.clients) == 0 {
				p.Remove(f.name)
			}
		}
		p.access.Unlock()
	}
}

func (p RedisFactory) loop() {
	for {
		switch v := p.conn.Receive().(type) {
		case redis.Message:
			log.Println(v.Channel, v.Data)
			messages, ok := p.messages[v.Channel]
			if !ok {
				log.Println(v.Channel, "not found in channels")
				continue
			}
			messages <- v.Data
		case error:
			if err := p.connect(); err != nil {
				log.Println("RedisMessageProvider.loop: connect", err)
			}
			time.Sleep(attemptWait)
		}
	}
}

func (p *RedisFactory) Remove(name string) {
	key := getFacilityKey(name)
	delete(p.facilities, name)
	delete(p.messages, key)
	p.conn.Unsubscribe(key)
}

func (p *RedisFactory) RemoveClient(f *Facility, c MessageChan) {
	f.access.Lock()
	p.access.Lock()
	delete(f.clients, c)
	close(c)
	if len(f.clients) == 0 {
		p.Remove(f.name)
	}
	p.access.Unlock()
	f.access.Unlock()
}

func getFacilityKey(name string) string {
	return strings.Join([]string{redisPrefix, facilityPrefix, name}, keySeparator)
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
	log.Println(redisAddr, redisDatabase, p.key)
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
	if f == nil {
		panic("facility is nil")
	}
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
