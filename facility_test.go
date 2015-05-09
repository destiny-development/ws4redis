package main

import (
	"testing"
	"time"

	"github.com/garyburd/redigo/redis"
	. "github.com/smartystreets/goconvey/convey"
)

const (
	timeout = time.Millisecond * 1000
)

func init() {
	redisPrefix = "ws-test"
}

func TestMemoryFacility(t *testing.T) {
	Convey("Create", t, func() {
		c := make(MessageChan)
		p := MemoryMessageProvider{c}
		f := NewFacility("test", p)
		message := []byte("testmessage")
		Convey("Subscribe", func() {
			m := f.Subscribe()
			Convey("Broadcast", func() {
				c <- message
				Convey("The message should be recieved", func() {
					var recieved []byte
					t := time.NewTimer(timeout)
					var timedOut bool
					select {
					case recievedMessage, ok := <-m:
						So(ok, ShouldBeTrue)
						recieved = recievedMessage
					case _ = <-t.C:
						recieved = nil
						timedOut = true
					}
					Convey("Not timed out", func() {
						So(timedOut, ShouldBeFalse)
					})
					So(string(recieved), ShouldEqual, string(message))
				})
			})
			Convey("Unsubscribe", func() {
				f.Unsubscibe(m)
				t := time.NewTimer(timeout)
				timedOut := false
				select {
				case _, ok := <-m:
					So(ok, ShouldBeFalse)
				case _ = <-t.C:
					timedOut = true
				}
				Convey("Not timed out", func() {
					So(timedOut, ShouldBeFalse)
				})
			})
		})
	})
}

func TestRedisFacility(t *testing.T) {
	Convey("Create", t, func() {
		So(NewRedisFacility("test"), ShouldNotBeNil)
	})
}

func TestRedisProvider(t *testing.T) {
	Convey("Create", t, func() {
		k := "redis-test:provider"
		p := NewRedisProvider(k)
		c := p.Channel()
		conn, err := redis.Dial(redisNetwork, redisAddr)
		So(err, ShouldBeNil)
		message := []byte("testmessage")
		_, err = conn.Do("select", redisDatabase)
		So(err, ShouldBeNil)
		Convey("Send", func() {
			_, err := conn.Do("publish", k, message)
			So(err, ShouldBeNil)
			Convey("Recieved", func() {
				var recieved []byte
				t := time.NewTimer(timeout)
				var timedOut bool
				select {
				case m, ok := <-c:
					So(ok, ShouldBeTrue)
					recieved = m
				case _ = <-t.C:
					timedOut = true
				}
				Convey("Not timed out", func() {
					So(timedOut, ShouldBeFalse)
				})
				So(string(recieved), ShouldEqual, string(message))
			})
		})

	})
}
